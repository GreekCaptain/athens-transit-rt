package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"runtime"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/match"
	"github.com/angelospk/athens-transit-rt/internal/plan"
	"github.com/angelospk/athens-transit-rt/internal/sched"
)

// The planner on real data (design, acceptance): ATRT_PLAN_STATE is a state directory with a GTFS
// snapshot (snapshot-*.bin + manifest.json) and the STASY files (stasy/), e.g. a copy of the
// service's volume. ATRT_PLAN_DAY picks the service day (default 2026-09-15, a Tuesday inside the
// 2026-07-08 GTFS). Skipped without it.
//
//	ATRT_PLAN_STATE=/path/state go test -run TestPlanRealData -v ./internal/server/
//	ATRT_PLAN_STATE=/path/state go test -run x -bench Plan -benchmem ./internal/server/

func realPlanApp(tb testing.TB) (*App, time.Time) {
	tb.Helper()
	dir := os.Getenv("ATRT_PLAN_STATE")
	if dir == "" {
		tb.Skip("ATRT_PLAN_STATE not set")
	}
	day := os.Getenv("ATRT_PLAN_DAY")
	if day == "" {
		day = "2026-09-15"
	}
	d, err := time.ParseInLocation(time.DateOnly, day, gtfs.Athens)
	if err != nil {
		tb.Fatal(err)
	}
	now := d.Add(18 * time.Hour)
	a := New(Config{StateDir: dir, RPS: 1, Matcher: match.Memory, Sched: sched.DefaultConfig(), STASYURL: "http://127.0.0.1:1/none"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.now = func() time.Time { return now }
	f, m, err := a.fetch.LoadLocal()
	if err != nil || f == nil {
		tb.Fatalf("snapshot: %v", err)
	}
	a.SetFeed(f, m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.stasy.Run(ctx)
	if _, ok := a.stasy.Feed(); !ok {
		tb.Fatal("no STASY timetable in " + dir + "/stasy")
	}
	return a, now
}

func liveHeap() uint64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func settledHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

type place struct {
	name     string
	lat, lon float64
}

var (
	syntagmaP   = place{"Σύνταγμα", 37.97527, 23.73552}
	glyfadaP    = place{"Γλυφάδα", 37.8739, 23.754}
	piraeusP    = place{"Πειραιάς", 37.9480, 23.6430}
	kifisiaP    = place{"Κηφισιά", 38.0737, 23.8083}
	chalandriP  = place{"Χαλάνδρι", 38.0213, 23.7990}
	peristeriP  = place{"Περιστέρι", 38.0130, 23.6950}
	omoniaP     = place{"Ομόνοια", 37.9842, 23.7280}
	kalamakiP   = place{"Καλαμάκι", 37.9150, 23.7110}
	nikaiaP     = place{"Νίκαια", 37.9660, 23.6480}
	patisiaP    = place{"Πατήσια", 38.0170, 23.7360}
	airportP    = place{"Αεροδρόμιο", 37.9370, 23.9450}
	kallitheaP  = place{"Καλλιθέα", 37.9550, 23.7020}
	marousiP    = place{"Μαρούσι", 38.0560, 23.8080}
	vyronasP    = place{"Βύρωνας", 37.9600, 23.7530}
	aigaleoP    = place{"Αιγάλεω", 37.9920, 23.6820}
	agDimitrios = place{"Άγ. Δημήτριος", 37.9340, 23.7290}
)

func clock(day time.Time, h, m int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, gtfs.Athens)
}

func describe(tt *plan.Timetable, res plan.Result) string {
	var b strings.Builder
	for _, it := range res.Itineraries {
		fmt.Fprintf(&b, "    %s → %s (%d min, walk %d m):", time.Unix(it.DepartAt, 0).In(gtfs.Athens).Format("15:04"),
			time.Unix(it.ArriveAt, 0).In(gtfs.Athens).Format("15:04"), it.DurationS/60, it.WalkM)
		for _, l := range it.Legs {
			if l.Kind == plan.KindRide {
				fmt.Fprintf(&b, " [%s %s %s→%s %s-%s]", l.Line, l.Headsign, *l.From.Stop, *l.To.Stop,
					time.Unix(l.DepartAt, 0).In(gtfs.Athens).Format("15:04"), time.Unix(l.ArriveAt, 0).In(gtfs.Athens).Format("15:04"))
			}
		}
		b.WriteByte('\n')
	}
	if len(res.Itineraries) == 0 {
		b.WriteString("    (none)\n")
	}
	return b.String()
}

func TestPlanRealData(t *testing.T) {
	a, now := realPlanApp(t)
	day := planNoon(now)

	base := settledHeap()
	start := time.Now()
	if err := a.buildPlan(now); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	steady := settledHeap() - base
	tt := a.planner.snap().tt
	st := tt.Stats()
	t.Logf("build %v: %d stops, %d trips, %d routes, %d stop events, %d transfers, %d skipped", took.Round(time.Millisecond),
		st.Stops, st.Trips, st.Routes, st.Events, st.Transfers, st.Skipped)
	t.Logf("steady heap growth (timetable): %.1f MB", float64(steady)/1e6)

	// Peak: a rebuild while 2 plans run, the live heap sampled every 10 ms.
	before := settledHeap()
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if h := liveHeap(); h > peak.Load() {
				peak.Store(h)
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				a.planner.snap().tt.Plan(plan.Query{From: plan.LatLon{Lat: syntagmaP.lat, Lon: syntagmaP.lon},
					To: plan.LatLon{Lat: kifisiaP.lat, Lon: kifisiaP.lon}, After: clock(day, 20, 0).Unix(), Last: true, N: 3})
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if err := a.buildPlan(now); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	t.Logf("peak heap growth (rebuild + 2 plans): %.1f MB over the settled heap of %.1f MB", float64(peak.Load()-before)/1e6,
		float64(before)/1e6)

	// Latency on random pairs inside the basin.
	rng := rand.New(rand.NewPCG(7, 11))
	for _, last := range []bool{false, true} {
		var ds []time.Duration
		empty := 0
		for range 300 {
			q := plan.Query{From: plan.LatLon{Lat: 37.92 + rng.Float64()*0.13, Lon: 23.64 + rng.Float64()*0.18},
				To:    plan.LatLon{Lat: 37.92 + rng.Float64()*0.13, Lon: 23.64 + rng.Float64()*0.18},
				After: clock(day, 6+rng.IntN(17), rng.IntN(60)).Unix(), Last: last, N: 3}
			s := time.Now()
			if len(tt.Plan(q).Itineraries) == 0 {
				empty++
			}
			ds = append(ds, time.Since(s))
		}
		slices.Sort(ds)
		t.Logf("last=%v: p50 %v p95 %v max %v; %d of 300 without options", last, ds[150].Round(time.Microsecond),
			ds[284].Round(time.Microsecond), ds[299].Round(time.Microsecond), empty)
	}

	for _, c := range []struct {
		from, to place
		h, m     int
		last     bool
	}{
		{syntagmaP, glyfadaP, 23, 30, true},
		{piraeusP, kifisiaP, 18, 0, false},
		{chalandriP, peristeriP, 18, 0, false},
		{syntagmaP, piraeusP, 25, 0, false},
		{omoniaP, kalamakiP, 25, 10, false},
		{patisiaP, nikaiaP, 22, 0, true},
		{airportP, syntagmaP, 21, 0, true},
	} {
		res := tt.Plan(plan.Query{From: plan.LatLon{Lat: c.from.lat, Lon: c.from.lon}, To: plan.LatLon{Lat: c.to.lat, Lon: c.to.lon},
			After: clock(day, c.h, c.m).Unix(), Last: c.last, N: 3})
		t.Logf("%s → %s after %02d:%02d last=%v:\n%s", c.from.name, c.to.name, c.h, c.m, c.last, describe(tt, res))
	}
}

func BenchmarkPlanBuild(b *testing.B) {
	a, now := realPlanApp(b)
	b.ResetTimer()
	for range b.N {
		if err := a.buildPlan(now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlan(b *testing.B) {
	a, now := realPlanApp(b)
	if err := a.buildPlan(now); err != nil {
		b.Fatal(err)
	}
	tt := a.planner.snap().tt
	day := planNoon(now)
	pairs := [][2]place{{syntagmaP, glyfadaP}, {piraeusP, kifisiaP}, {chalandriP, peristeriP}, {kallitheaP, marousiP},
		{vyronasP, aigaleoP}, {agDimitrios, patisiaP}}
	for _, last := range []bool{false, true} {
		b.Run(fmt.Sprint("last=", last), func(b *testing.B) {
			for i := range b.N {
				p := pairs[i%len(pairs)]
				tt.Plan(plan.Query{From: plan.LatLon{Lat: p[0].lat, Lon: p[0].lon}, To: plan.LatLon{Lat: p[1].lat, Lon: p[1].lon},
					After: clock(day, 8+i%14, 0).Unix(), Last: last, N: 3})
			}
		})
	}
}

// TestPlanCgroupPeak is the container check of the design, run in a scope of its own:
//
//	go test -c -o /tmp/server.test ./internal/server/
//	systemd-run --user --scope -p MemoryMax=200M env GOMEMLIMIT=100MiB ATRT_PLAN_STATE=… ATRT_PLAN_MODE=plan \
//	  /tmp/server.test -test.run TestPlanCgroupPeak -test.v
//
// ATRT_PLAN_MODE=baseline loads the same state with no planner. It logs the cgroup's memory.peak.
func TestPlanCgroupPeak(t *testing.T) {
	mode := os.Getenv("ATRT_PLAN_MODE")
	if mode == "" {
		t.Skip("ATRT_PLAN_MODE not set")
	}
	a, now := realPlanApp(t)
	day := planNoon(now)
	end := time.Now().Add(20 * time.Second)
	if mode == "plan" {
		if err := a.buildPlan(now); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		pairs := [][2]place{{syntagmaP, glyfadaP}, {piraeusP, kifisiaP}, {chalandriP, peristeriP}, {kallitheaP, marousiP}}
		var n atomic.Int64
		for g := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := g; time.Now().Before(end); i++ {
					p := pairs[i%len(pairs)]
					res, _ := get(t, a, fmt.Sprintf("/v2/plan?from=%f,%f&to=%f,%f&after=%d&last=%v", p[0].lat, p[0].lon,
						p[1].lat, p[1].lon, clock(day, 8+i%14, 0).Unix(), i%2 == 0))
					if res.StatusCode != 200 {
						t.Errorf("status %d", res.StatusCode)
					}
					n.Add(1)
				}
			}()
		}
		for range 3 { // rebuilds while the plans run
			time.Sleep(5 * time.Second)
			if err := a.buildPlan(now); err != nil {
				t.Fatal(err)
			}
		}
		wg.Wait()
		t.Logf("%d plans", n.Load())
	} else {
		time.Sleep(time.Until(end))
	}
	cg, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	path := "/sys/fs/cgroup" + strings.TrimSpace(strings.TrimPrefix(string(cg), "0::"))
	peak, err := os.ReadFile(path + "/memory.peak")
	if err != nil {
		t.Fatal(err)
	}
	var v int64
	fmt.Sscan(string(peak), &v)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("mode %s: cgroup memory.peak %.1f MB; Go heap now %.1f MB, sys %.1f MB", mode, float64(v)/1e6,
		float64(m.HeapAlloc)/1e6, float64(m.Sys)/1e6)
}
