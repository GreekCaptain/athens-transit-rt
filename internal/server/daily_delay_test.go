package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/history"
	"github.com/angelospk/athens-transit-rt/internal/model"
	"github.com/angelospk/athens-transit-rt/internal/sched"
	"github.com/angelospk/athens-transit-rt/internal/telematics"
)

// departApp polls bus "b" of line L (stops 880 m and 5 min apart, GTFS trips every 10 min from
// 10:00) on day: at the start at 10:44 and 10:46, at S1 at 10:50, so it left at 10:46. With daily,
// OASA's daily schedule of the line is cached first. The clock moves with the polls.
func departApp(t *testing.T, day time.Time, daily *telematics.DailySchedule, fixes []float64, before ...func(*App)) *App {
	t.Helper()
	at := func(h, m int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, gtfs.Athens)
	}
	now := at(10, 44)
	a := oasaOnly(newAppWith(t, sched.DefaultConfig(), func() time.Time { return now }))
	if daily != nil {
		a.daily = func(context.Context, string) (telematics.DailySchedule, error) { return *daily, nil }
		if _, ok := a.dailyOfLine(context.Background(), "L", gtfs.Midnight(now)); !ok {
			t.Fatal("daily schedule not cached")
		}
	}
	for _, f := range before {
		f(a)
	}
	for i, m := range []int{44, 46, 50}[3-len(fixes):] {
		now = at(10, m)
		pollOne(a, now, "b", fixes[i], now)
	}
	return a
}

var leftAt1046 = []float64{23.7001, 23.7002, 23.71}

func lineVehicle(t *testing.T, a *App, id string) Vehicle {
	t.Helper()
	_, body := get(t, a, "/v1/lines/L")
	var r LineResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	for _, v := range r.Vehicles {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no vehicle %s: %s", id, body)
	return Vehicle{}
}

// On an expired GTFS a bus that left on a run of OASA's daily schedule is late or early against
// that run: the GTFS trip's times move to its departure. Every endpoint says the same.
func TestDelayAgainstDailyRun(t *testing.T) {
	a := departApp(t, expiredMonday, &telematics.DailySchedule{Descr: "WEST - EAST", Go: []string{"10:45", "11:30"}}, leftAt1046)
	raw := departApp(t, expiredMonday, nil, leftAt1046)
	v, rv := lineVehicle(t, a, "b"), lineVehicle(t, raw, "b")
	if v.TripID == nil || rv.DelayS == nil || *rv.DelayS == 0 {
		t.Fatalf("test setup: matched %v, raw delay %v", v.TripID, rv.DelayS)
	}
	// At S1 at 10:50: the 10:45 run is there on time.
	if v.DelayS == nil || *v.DelayS != 0 {
		t.Fatalf("delay %v (GTFS %d)", v.DelayS, *rv.DelayS)
	}
	// The stop board: S3 is 15 min after the start, so 11:00.
	_, body := get(t, a, "/v1/stops/S3")
	var sr StopResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range sr.Arrivals {
		if x.Vehicle == "b" {
			found = true
			if x.Scheduled == nil || *x.Scheduled != oct12(11, 0) || x.DelayS != 0 {
				t.Fatalf("board %s", asJSON(x))
			}
		}
	}
	if !found {
		t.Fatalf("no arrival of b: %s", body)
	}
	// /v2: the vehicle, and its Run.
	var vs v2Vehicles
	v2get(t, a, "/v2/lines/"+esc("oasa:L")+"/vehicles", &vs)
	if len(vs.Vehicles) != 1 || vs.Vehicles[0].DelayS == nil || *vs.Vehicles[0].DelayS != 0 || vs.Vehicles[0].Run == nil {
		t.Fatalf("v2 vehicles %s", asJSON(vs.Vehicles))
	}
	var r struct {
		Run *model.Run `json:"run"`
	}
	v2get(t, a, "/v2/runs/"+esc(*vs.Vehicles[0].Run), &r)
	if r.Run == nil || len(r.Run.Stops) != 5 || *r.Run.Stops[3].ScheduledAt != oct12(11, 0) || *r.Run.Stops[3].DelayS != 0 ||
		r.Run.Stops[3].At != oct12(11, 0) || r.Run.Status != model.StatusOf(true, model.Ptr(0)) {
		t.Fatalf("run %s", asJSON(r.Run))
	}
}

// Without a departure seen, with no daily schedule cached, or on a valid GTFS, the GTFS delay stands.
func TestDelayAgainstDailyRunFallbacks(t *testing.T) {
	daily := &telematics.DailySchedule{Descr: "WEST - EAST", Go: []string{"10:45", "11:30"}}
	for _, c := range []struct {
		name  string
		day   time.Time
		daily *telematics.DailySchedule
		fixes []float64
	}{
		{"first seen on its way", expiredMonday, daily, []float64{23.71}},
		{"no daily schedule", expiredMonday, nil, leftAt1046},
		{"a run nearer and free", expiredMonday, &telematics.DailySchedule{Descr: "WEST - EAST", Go: []string{"10:42", "10:47"}}, leftAt1046},
		{"valid GTFS", time.Date(2026, 10, 5, 0, 0, 0, 0, gtfs.Athens), daily, leftAt1046},
		{"partial daily schedule", expiredMonday, daily, leftAt1046},
	} {
		var partial []func(*App)
		if c.name == "partial daily schedule" { // a line code failed: its runs may be missing
			partial = append(partial, func(a *App) {
				e := a.dailyCache["L"]
				e.partial = true
				a.dailyCache["L"] = e
			})
		}
		got := lineVehicle(t, departApp(t, c.day, c.daily, c.fixes, partial...), "b")
		want := lineVehicle(t, departApp(t, c.day, nil, c.fixes), "b")
		if got.DelayS == nil || want.DelayS == nil || *got.DelayS != *want.DelayS {
			t.Errorf("%s: delay %v, GTFS %v", c.name, got.DelayS, want.DelayS)
		}
	}
}

// historyOf runs departApp with a fix history and returns its rows by fix time (columns split).
func historyOf(t *testing.T, day time.Time, daily *telematics.DailySchedule, fixes []float64) map[int64][]string {
	t.Helper()
	dir := t.TempDir()
	var h *history.Writer
	departApp(t, day, daily, fixes, func(a *App) {
		var err error
		if h, err = history.Open(dir, 1<<30, 24*time.Hour, a.now, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
			t.Fatal(err)
		}
		a.hist = h
	})
	h.Close()
	names, _ := filepath.Glob(filepath.Join(dir, "*.csv"))
	if len(names) != 1 {
		t.Fatalf("files %v", names)
	}
	b, _ := os.ReadFile(names[0])
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if lines[0]+"\n" != history.Header {
		t.Fatalf("header %q", lines[0])
	}
	rows := map[int64][]string{}
	for _, l := range lines[1:] {
		f := strings.Split(l, ",")
		at, _ := strconv.ParseInt(f[0], 10, 64)
		rows[at] = f
	}
	return rows
}

// The history keeps the GTFS delay, and adds the run of OASA's daily schedule a bus left on and
// its start delay (left 10:46 on the 10:45 run: 60 s), for the daily stats (web#32).
func TestHistoryKeepsTheDailyRunAndItsStartDelay(t *testing.T) {
	daily := &telematics.DailySchedule{Descr: "WEST - EAST", Go: []string{"10:45", "11:30"}}
	rows := historyOf(t, expiredMonday, daily, leftAt1046)
	raw := historyOf(t, expiredMonday, nil, leftAt1046)
	at := oct12(10, 50)
	r := rows[at]
	if len(r) != 13 || r[11] != strconv.FormatInt(oct12(10, 45), 10) || r[12] != "60" || r[10] != raw[at][10] || r[10] == "" {
		t.Fatalf("row at 10:50 %q (raw %q)", r, raw[at])
	}
	for _, m := range []int{44, 46} { // not left yet
		if r := rows[oct12(10, m)]; len(r) != 13 || r[11] != "" || r[12] != "" {
			t.Fatalf("row at 10:%d %q", m, r)
		}
	}
	if r := raw[at]; r[11] != "" || r[12] != "" {
		t.Fatalf("without a daily schedule %q", r)
	}
}
