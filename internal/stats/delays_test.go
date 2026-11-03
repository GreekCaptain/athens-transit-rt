package stats

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/history"
)

// oldHeader is the history header before the run columns (rt#55 start delays): files written
// before the upgrade have it, and stats must read them as before.
const oldHeader = "fix_t,line,route_code,veh,lat,lon,gtfs,trip_id,shape_id,s_m,delay_s\n"

func writeRaw(t *testing.T, dir, name, header string, rows ...string) {
	t.Helper()
	out, err := os.Create(filepath.Join(dir, name+".csv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(out)
	zw.Write([]byte(header + strings.Join(rows, "")))
	zw.Close()
	out.Close()
}

// drow is a new-format row of vehicle veh on line; run and delay are empty when run is zero.
func drow(at time.Time, line, veh string, run time.Time, delay int) string {
	r, d := "", ""
	if !run.IsZero() {
		r, d = fmt.Sprint(run.Unix()), fmt.Sprint(delay)
	}
	return fmt.Sprintf("%d,%s,R,%s,37980000,23700000,g,T,SH,10,5,%s,%s\n", at.Unix(), line, veh, r, d)
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A day of files without the run columns has exactly the fields it had before start delays.
func TestDayOfOldFilesKeepsItsFields(t *testing.T) {
	dir := t.TempDir()
	t0 := local(2026, 10, 6, 8, 0, 0)
	writeHour(t, dir, hourName(t0), ".csv.gz", fix{t0, "040", "1", 37.98, 23.72})
	d, err := Compute("2026-10-06", athens, inputs(t, dir, "2026-10-06"), time.Unix(0, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"by_line", "date", "files", "first_fix", "fixes", "generated_at", "hours", "last_fix", "lines", "tz", "vehicles", "version"}
	if got := jsonKeys(t, d); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %v", got)
	}
	dir, plan := tripsDay(t)
	d, err = Compute("2026-10-06", athens, inputs(t, dir, "2026-10-06"), time.Unix(0, 0), plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonKeys(t, d); !reflect.DeepEqual(got, sortedWith(want, "trips")) {
		t.Fatalf("keys with trips %v", got)
	}
}

func sortedWith(keys []string, k string) []string {
	out := append(append([]string{}, keys...), k)
	sort.Strings(out)
	return out
}

// Each run of OASA's daily schedule whose start delay was seen counts once, for the day of its
// scheduled departure: mean, p90 (nearest rank), the sum and a histogram of whole minutes.
func TestComputeStartDelaysPerLine(t *testing.T) {
	dir := t.TempDir()
	h8 := local(2026, 10, 6, 8, 0, 0)
	run := func(h, m int) time.Time { return local(2026, 10, 6, h, m, 0) }
	// An old file of the same day: its fixes count, it has no delays.
	writeRaw(t, dir, hourName(h8), oldHeader, row(fix{h8, "040", "Z", 37.98, 23.72}))
	h10 := local(2026, 10, 6, 10, 0, 0)
	writeRaw(t, dir, hourName(h10), history.Header,
		drow(h10.Add(1*time.Minute), "040", "A", run(10, 0), 60),
		drow(h10.Add(2*time.Minute), "040", "A", run(10, 0), 60), // the same run again
		drow(h10.Add(3*time.Minute), "040", "A", time.Time{}, 0), // no run known
		drow(h10.Add(12*time.Minute), "040", "B", run(10, 10), 180),
		drow(h10.Add(31*time.Minute), "040", "A", run(10, 30), -30),
		drow(h10.Add(40*time.Minute), "050", "D", run(10, 40), 0),
	)
	h12 := local(2026, 10, 6, 12, 0, 0)
	writeRaw(t, dir, hourName(h12), history.Header, drow(h12.Add(10*time.Minute), "040", "C", run(12, 0), 600))
	// Past midnight: a run of the 6th counts, a run of the 7th does not.
	h0 := local(2026, 10, 7, 0, 0, 0)
	writeRaw(t, dir, hourName(h0), history.Header,
		drow(h0.Add(5*time.Minute), "050", "E", run(23, 55), 120),
		drow(h0.Add(15*time.Minute), "050", "F", h0.Add(10*time.Minute), 300),
	)
	// Before the day: a run of the 5th does not count.
	hp := local(2026, 10, 5, 23, 0, 0)
	writeRaw(t, dir, hourName(hp), history.Header, drow(hp.Add(55*time.Minute), "050", "G", hp.Add(50*time.Minute), 900))

	d, err := Compute("2026-10-06", athens, inputs(t, dir, "2026-10-06"), time.Unix(0, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Fixes != 8 || byLine(d, "040").Fixes != 7 {
		t.Fatalf("fixes %d, 040 %d", d.Fixes, byLine(d, "040").Fixes)
	}
	want := &StartDelays{Runs: 6, ByLine: []LineDelay{
		{Line: "040", Runs: 4, MeanS: 203, P90S: 600, SumS: 810, Minutes: [][2]int{{-1, 1}, {1, 1}, {3, 1}, {10, 1}}},
		{Line: "050", Runs: 2, MeanS: 60, P90S: 120, SumS: 120, Minutes: [][2]int{{0, 1}, {2, 1}}},
	}}
	if !reflect.DeepEqual(d.StartDelay, want) {
		b, _ := json.Marshal(d.StartDelay)
		t.Fatalf("start_delay = %s", b)
	}
	b, _ := json.Marshal(d.StartDelay.ByLine[0])
	if string(b) != `{"line":"040","runs":4,"mean_s":203,"p90_s":600,"sum_s":810,"minutes":[[-1,1],[1,1],[3,1],[10,1]]}` {
		t.Fatalf("json %s", b)
	}
}
