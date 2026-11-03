package server

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/history"
	"github.com/angelospk/athens-transit-rt/internal/model"
	"github.com/angelospk/athens-transit-rt/internal/usual"
)

type v2StopUsual struct {
	Arrivals []model.Arrival `json:"arrivals"`
	Usual    []model.Usual   `json:"usual"`
}

// usualDays writes day files where line L «EAST» passed S3 at the given local times on the
// four weekdays before monday1020, and opens them as the app's store.
func usualDays(t *testing.T, a *App, hhmm ...string) {
	t.Helper()
	dir := filepath.Join(a.cfg.StateDir, "usual")
	for _, d := range []time.Time{
		time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	} {
		day := usual.NewDay(d)
		for _, s := range hhmm {
			var h, m int
			fmt.Sscanf(s, "%d:%d", &h, &m)
			day.Add("S3", "L", "EAST", time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, gtfs.Athens).Unix())
		}
		if err := usual.WriteDay(dir, day); err != nil {
			t.Fatal(err)
		}
	}
	st, err := usual.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	a.usual.Store(st)
}

func local(hh, mm int) int64 {
	return time.Date(2026, 10, 5, hh, mm, 0, 0, gtfs.Athens).Unix()
}

// map#24: /v2/stops/{id} lists the usual service per line and headsign; the time of the next
// pass only when the past weeks agree and a timetable Arrival of today is near it.
func TestV2StopUsual(t *testing.T) {
	a := newApp(t, monday1020)
	// Every 10 min from 10:00 to 11:00 but 10:20: the next pass after 10:20 is 10:30.
	usualDays(t, a, "10:00", "10:10", "10:30", "10:40", "10:50", "11:00")
	var s v2StopUsual
	v2get(t, a, "/v2/stops/"+esc("oasa:S3"), &s)
	if len(s.Usual) != 1 {
		t.Fatalf("usual %s", asJSON(s.Usual))
	}
	u := s.Usual[0]
	if u.Line != "oasa:L" || u.Headsign != "EAST" || u.DayType != "weekday" || u.Days != 4 ||
		u.HeadwayMin == nil || *u.HeadwayMin != 10 || u.NextAt == nil || *u.NextAt != local(10, 30) ||
		u.SpreadS == nil || *u.SpreadS != 0 {
		t.Fatalf("usual %s (arrivals %s)", asJSON(u), asJSON(s.Arrivals))
	}
}

func TestV2StopUsualGuard(t *testing.T) {
	a := newApp(t, monday1020)
	// The past weeks agree on 12:30, but today's timetable has no run near it (the last one
	// reaches S3 at 11:05): no time.
	usualDays(t, a, "12:30")
	var s v2StopUsual
	v2get(t, a, "/v2/stops/"+esc("oasa:S3"), &s)
	for _, u := range s.Usual {
		if u.NextAt != nil {
			t.Fatalf("next_at without a timetable run near it: %s (arrivals %s)", asJSON(u), asJSON(s.Arrivals))
		}
	}
}

func TestV2StopUsualEmpty(t *testing.T) {
	a := newApp(t, monday1020)
	_, body := get(t, a, "/v2/stops/"+esc("oasa:S3"))
	if !strings.Contains(string(body), `"usual":[]`) {
		t.Fatalf("no store: want \"usual\":[], got %s", body)
	}
}

// The nightly build turns a finished day of fix history into a day file, once.
func TestBuildUsual(t *testing.T) {
	now := monday1020
	a := newApp(t, now)
	w := a.world()
	feed := worldVersion(w)
	info := usual.FeedRoutes(w.feed).Shape("SH")
	if info == nil || len(info.Stops) == 0 {
		t.Fatal("no stops on SH")
	}
	s3 := info.Stops[len(info.Stops)-2] // S3
	if s3.Stop != "S3" {
		t.Fatalf("stops %+v", info.Stops)
	}
	// Friday 2026-10-02: bus b1 on trip T00 crosses S3 at 09:00:10 local; a keep-alive bus
	// reports every minute all day.
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	hist := filepath.Join(a.cfg.StateDir, "history")
	os.MkdirAll(hist, 0o755)
	files := map[string]*strings.Builder{}
	add := func(at time.Time, veh, trip, shape string, s int) {
		h := at.UTC().Format("2006-01-02T15")
		if files[h] == nil {
			files[h] = &strings.Builder{}
			files[h].WriteString(history.Header)
		}
		fmt.Fprintf(files[h], "%d,L,SH,%s,37980000,23720000,%s,%s,%s,%d,0\n", at.Unix(), veh, feed, trip, shape, s)
	}
	for at := time.Date(2026, 10, 2, 4, 0, 0, 0, gtfs.Athens); at.Before(time.Date(2026, 10, 3, 4, 0, 0, 0, gtfs.Athens)); at = at.Add(time.Minute) {
		add(at, "alive", "", "", -1)
	}
	at9 := time.Date(2026, 10, 2, 9, 0, 0, 0, gtfs.Athens)
	add(at9.Add(-5*time.Minute), "b1", "T00", "SH", 0)
	add(at9, "b1", "T00", "SH", int(s3.Along)-100)
	add(at9.Add(20*time.Second), "b1", "T00", "SH", int(s3.Along)+100)
	for h, b := range files {
		f, _ := os.Create(filepath.Join(hist, h+".csv.gz"))
		zw := gzip.NewWriter(f)
		zw.Write([]byte(b.String()))
		zw.Close()
		f.Close()
	}
	st, err := usual.OpenStore(filepath.Join(a.cfg.StateDir, "usual"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tried := map[time.Time]string{}
	a.buildUsual(st, tried)
	if !st.Has(day) {
		t.Fatalf("day not built; tried %v", tried)
	}
	// The other days had no history: tried once for this feed, not again.
	if len(tried) != usual.TrainDays {
		t.Errorf("tried %d days, want %d", len(tried), usual.TrainDays)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.StateDir, "usual", "20261002.pass")); err != nil {
		t.Fatal(err)
	}
}

// The web's stop card reads /v1/stops/{id}: it gets the same `usual`, lines without the
// namespace (as its arrivals).
func TestV1StopUsual(t *testing.T) {
	a := newApp(t, monday1020)
	usualDays(t, a, "10:00", "10:10", "10:30", "10:40", "10:50", "11:00")
	var s struct {
		Usual []model.Usual `json:"usual"`
	}
	res, body := get(t, a, "/v1/stops/S3")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Usual) != 1 || s.Usual[0].Line != "L" || s.Usual[0].NextAt == nil || *s.Usual[0].NextAt != local(10, 30) ||
		s.Usual[0].HeadwayMin == nil || *s.Usual[0].HeadwayMin != 10 {
		t.Fatalf("usual %s", body)
	}
	a3 := newApp(t, monday1020)
	usualDays(t, a3, "12:30") // no run of today near it
	if _, body = get(t, a3, "/v1/stops/S3"); strings.Contains(string(body), `"next_at":1`) {
		t.Fatalf("guard: %s", body)
	}
	a2 := newApp(t, monday1020)
	_, body = get(t, a2, "/v1/stops/S3")
	if !strings.Contains(string(body), `"usual":[]`) {
		t.Fatalf("no store: %s", body)
	}
}
