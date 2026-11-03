package plan

import (
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/geo"
)

// Golden cases of the design (docs/superpowers/specs/2026-11-03-trip-planner.md, Tests), on small
// hand-made timetables. Positions are metres east (x) and north (y) of a point in Athens.

var (
	athens = mustLoc("Europe/Athens")
	ref    = time.Date(2026, 11, 3, 0, 0, 0, 0, athens) // Tuesday; noon − 12 h is midnight
	origin = geo.XY(37.98, 23.73)
)

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func ll(x, y float64) LatLon {
	lat, lon := geo.LatLon([2]float64{origin[0] + x, origin[1] + y})
	return LatLon{lat, lon}
}

// hm is seconds of the service day; h may pass 24.
func hm(h, m int) int32 { return int32(h*3600 + m*60) }

func at(secs int32) int64 { return ref.Unix() + int64(secs) }

type builder struct{ in Input }

func newBuilder() *builder {
	return &builder{in: Input{Day: "2026-11-03", Ref: ref}}
}

func (b *builder) stop(id string, x, y float64) int32 { return b.addStop(id, x, y, false) }

func (b *builder) station(id string, x, y float64) int32 { return b.addStop(id, x, y, true) }

func (b *builder) addStop(id string, x, y float64, station bool) int32 {
	p := ll(x, y)
	b.in.Stops = append(b.in.Stops, Stop{ID: id, Lat: p.Lat, Lon: p.Lon, Station: station})
	return int32(len(b.in.Stops) - 1)
}

// trip adds a trip calling at stops at times (arrival = departure).
func (b *builder) trip(line, run string, stops []int32, times ...int32) {
	b.tripShape(line, run, -1, stops, times...)
}

func (b *builder) tripShape(line, run string, shape int32, stops []int32, times ...int32) {
	if len(stops) != len(times) {
		panic("stops and times differ")
	}
	t := Trip{Line: line, Mode: "bus", Headsign: "H " + line, Run: run, Pattern: "p:" + line, Shape: shape}
	for i, s := range stops {
		t.Times = append(t.Times, StopTime{Stop: s, Arr: times[i], Dep: times[i]})
	}
	b.in.Trips = append(b.in.Trips, t)
}

func (b *builder) build(t *testing.T) *Timetable {
	t.Helper()
	tt := Build(&b.in)
	if tt == nil {
		t.Fatal("Build returned nil")
	}
	return tt
}

func plan(tt *Timetable, from, to LatLon, after int32, last bool, n int) Result {
	return tt.Plan(Query{From: from, To: to, After: at(after), Last: last, N: n})
}

// walkOf is the walk of a straight distance: (metres × 1.3 rounded, seconds rounded up).
func walkOf(straight float64) (int, int64) {
	d := int(math.Round(straight * 1.3))
	return d, int64(math.Ceil(float64(d) / 1.2))
}

func summary(r Result) []string {
	var out []string
	for _, it := range r.Itineraries {
		var lines []string
		for _, l := range it.Legs {
			if l.Kind == KindRide {
				lines = append(lines, l.Line)
			}
		}
		out = append(out, strings.Join(lines, "+"))
	}
	return out
}

// checkShape checks the rules every itinerary keeps (contract §9).
func checkShape(t *testing.T, it Itinerary) {
	t.Helper()
	if len(it.Legs) < 3 || len(it.Legs)%2 == 0 {
		t.Fatalf("legs %d: want walk, (ride, walk)+", len(it.Legs))
	}
	rides, walk := 0, 0
	for i, l := range it.Legs {
		want := KindWalk
		if i%2 == 1 {
			want = KindRide
		}
		if l.Kind != want {
			t.Fatalf("leg %d is %s, want %s", i, l.Kind, want)
		}
		if l.ArriveAt < l.DepartAt {
			t.Errorf("leg %d arrives before it leaves", i)
		}
		if i > 0 && l.DepartAt < it.Legs[i-1].ArriveAt {
			t.Errorf("leg %d leaves before leg %d ends", i, i-1)
		}
		if len(l.Shape) < 2 {
			t.Errorf("leg %d shape has %d points", i, len(l.Shape))
		} else if first, last := l.Shape[0], l.Shape[len(l.Shape)-1]; first != [2]float64{l.From.Lat, l.From.Lon} ||
			last != [2]float64{l.To.Lat, l.To.Lon} {
			t.Errorf("leg %d shape %v..%v, want from %v to %v", i, first, last, l.From, l.To)
		}
		if l.Kind == KindRide {
			rides++
			if l.From.Stop == nil || l.To.Stop == nil {
				t.Errorf("ride %d without stops", i)
			}
			if i > 1 && l.DepartAt-it.Legs[i-1].ArriveAt < transferBuffer {
				t.Errorf("ride %d: wait %d s, want >= %d", i, l.DepartAt-it.Legs[i-1].ArriveAt, transferBuffer)
			}
		} else {
			walk += l.DistanceM
			if len(l.Shape) != 2 {
				t.Errorf("walk %d shape has %d points", i, len(l.Shape))
			}
		}
	}
	if it.Legs[1].DepartAt != it.Legs[0].ArriveAt {
		t.Errorf("first walk ends %d, first ride leaves %d: want no wait", it.Legs[0].ArriveAt, it.Legs[1].DepartAt)
	}
	if it.Transfers != rides-1 || it.WalkM != walk {
		t.Errorf("transfers %d walk_m %d, want %d %d", it.Transfers, it.WalkM, rides-1, walk)
	}
	if it.DepartAt != it.Legs[0].DepartAt || it.ArriveAt != it.Legs[len(it.Legs)-1].ArriveAt ||
		it.DurationS != it.ArriveAt-it.DepartAt {
		t.Errorf("itinerary times %d..%d (%d s) do not match its legs", it.DepartAt, it.ArriveAt, it.DurationS)
	}
}

// 1. One direct ride, walks at both ends.
func TestDirectRide(t *testing.T) {
	b := newBuilder()
	a, z := b.stop("A", 0, 0), b.stop("Z", 5000, 0)
	b.trip("L1", "r1", []int32{a, z}, hm(10, 0), hm(10, 10))
	tt := b.build(t)
	r := plan(tt, ll(-200, 0), ll(5000, 300), hm(9, 30), false, 3)
	if len(r.Itineraries) != 1 {
		t.Fatalf("itineraries %v", summary(r))
	}
	it := r.Itineraries[0]
	checkShape(t, it)
	d1, w1 := walkOf(200)
	d2, w2 := walkOf(300)
	if it.DepartAt != at(hm(10, 0))-w1 || it.ArriveAt != at(hm(10, 10))+w2 {
		t.Errorf("depart %d arrive %d, want %d %d", it.DepartAt, it.ArriveAt, at(hm(10, 0))-w1, at(hm(10, 10))+w2)
	}
	if it.WalkM != d1+d2 || it.Transfers != 0 || !slices.Equal(it.Modes, []string{"bus"}) {
		t.Errorf("walk_m %d transfers %d modes %v", it.WalkM, it.Transfers, it.Modes)
	}
	ride := it.Legs[1]
	if ride.Line != "L1" || ride.Run != "r1" || ride.Hops != 1 || *ride.From.Stop != "A" || *ride.To.Stop != "Z" ||
		ride.Headsign != "H L1" || ride.Mode != "bus" {
		t.Errorf("ride %+v", ride)
	}
	if it.Legs[0].From.Stop != nil || it.Legs[2].To.Stop != nil {
		t.Error("the end walks start and end at the query points, not at a Stop")
	}
	if r.WalkOnly != nil {
		t.Errorf("walk_only %+v for points 5 km apart", r.WalkOnly)
	}
}

// 2. One transfer with a 300 m walk; a 600 m transfer is not taken.
func TestTransferWalk(t *testing.T) {
	for _, tc := range []struct {
		gap  float64
		want int
	}{{300, 1}, {600, 0}} {
		b := newBuilder()
		a, m := b.stop("A", 0, 0), b.stop("M", 3000, 0)
		c, z := b.stop("C", 3000, tc.gap), b.stop("Z", 3000, 6000)
		b.trip("L1", "r1", []int32{a, m}, hm(10, 0), hm(10, 10))
		b.trip("L2", "r2", []int32{c, z}, hm(10, 20), hm(10, 40))
		tt := b.build(t)
		r := plan(tt, ll(0, 0), ll(3000, 6000), hm(9, 30), false, 3)
		if len(r.Itineraries) != tc.want {
			t.Fatalf("gap %v m: itineraries %v, want %d", tc.gap, summary(r), tc.want)
		}
		if tc.want == 0 {
			continue
		}
		it := r.Itineraries[0]
		checkShape(t, it)
		walk := it.Legs[2]
		d, w := walkOf(tc.gap)
		if walk.DistanceM != d || walk.ArriveAt-walk.DepartAt != w || walk.DepartAt != at(hm(10, 10)) {
			t.Errorf("transfer walk %+v, want %d m in %d s from 10:10", walk, d, w)
		}
		if it.Transfers != 1 || it.WalkM != d {
			t.Errorf("transfers %d walk_m %d", it.Transfers, it.WalkM)
		}
	}
}

// 3. The 60 s buffer: a connection with 30 s of slack is missed; one with 60 s is caught.
func TestTransferBuffer(t *testing.T) {
	for _, tc := range []struct {
		slack int32
		want  int
	}{{30, 0}, {60, 1}} {
		b := newBuilder()
		a, m, z := b.stop("A", 0, 0), b.stop("M", 3000, 0), b.stop("Z", 3000, 6000)
		b.trip("L1", "r1", []int32{a, m}, hm(10, 0), hm(10, 10))
		b.trip("L2", "r2", []int32{m, z}, hm(10, 10)+tc.slack, hm(10, 40))
		tt := b.build(t)
		r := plan(tt, ll(0, 0), ll(3000, 6000), hm(9, 30), false, 3)
		if len(r.Itineraries) != tc.want {
			t.Fatalf("slack %d s: itineraries %v, want %d", tc.slack, summary(r), tc.want)
		}
		if tc.want == 1 {
			checkShape(t, r.Itineraries[0])
			if w := r.Itineraries[0].Legs[2]; w.DistanceM != 0 || w.ArriveAt != w.DepartAt {
				t.Errorf("same-stop change at a bus stop %+v, want 0 m in 0 s", w)
			}
		}
	}
}

// 4. Pareto: a fast 2-transfer option and a slow direct one both returned.
func TestParetoTransfers(t *testing.T) {
	b := newBuilder()
	a, p, q, z := b.stop("A", 0, 0), b.stop("P", 2000, 0), b.stop("Q", 4000, 0), b.stop("Z", 6000, 0)
	b.trip("SLOW", "s", []int32{a, z}, hm(10, 0), hm(11, 30))
	b.trip("F1", "f1", []int32{a, p}, hm(10, 0), hm(10, 10))
	b.trip("F2", "f2", []int32{p, q}, hm(10, 12), hm(10, 20))
	b.trip("F3", "f3", []int32{q, z}, hm(10, 22), hm(10, 40))
	tt := b.build(t)
	r := plan(tt, ll(0, 0), ll(6000, 0), hm(9, 30), false, 3)
	got := summary(r)
	slices.Sort(got)
	if !slices.Equal(got, []string{"F1+F2+F3", "SLOW"}) {
		t.Fatalf("itineraries %v", got)
	}
	for _, it := range r.Itineraries {
		checkShape(t, it)
	}
	// Sorted by depart_at, then arrive_at: the fast one first.
	if summary(r)[0] != "F1+F2+F3" {
		t.Errorf("order %v", summary(r))
	}
}

// 5. last=true returns the latest n, sorted; last=false the first n.
func TestLastAndFirst(t *testing.T) {
	b := newBuilder()
	a, z := b.stop("A", 0, 0), b.stop("Z", 5000, 0)
	for m := 0; m < 24; m++ {
		dep := hm(20, 0) + int32(m*600)
		b.trip("L", "r"+itoa(m), []int32{a, z}, dep, dep+900)
	}
	tt := b.build(t)
	runs := func(r Result) []string {
		var out []string
		for _, it := range r.Itineraries {
			checkShape(t, it)
			out = append(out, it.Legs[1].Run)
		}
		return out
	}
	if got := runs(plan(tt, ll(0, 0), ll(5000, 0), hm(19, 0), false, 3)); !slices.Equal(got, []string{"r0", "r1", "r2"}) {
		t.Errorf("last=false: %v", got)
	}
	if got := runs(plan(tt, ll(0, 0), ll(5000, 0), hm(19, 0), true, 3)); !slices.Equal(got, []string{"r21", "r22", "r23"}) {
		t.Errorf("last=true: %v", got)
	}
	if got := runs(plan(tt, ll(0, 0), ll(5000, 0), hm(23, 25), true, 5)); !slices.Equal(got, []string{"r21", "r22", "r23"}) {
		t.Errorf("last=true after 23:25: %v", got)
	}
	// last=false looks 3 h past after only.
	if got := runs(plan(tt, ll(0, 0), ll(5000, 0), hm(16, 0), false, 3)); len(got) != 0 {
		t.Errorf("last=false 4 h before the first run: %v", got)
	}
}

// 6. Runs after midnight (times > 24 h) and an after at 01:30 on the previous service day; a ride
// whose stop departure is 10 min after after but whose stop is 10 min away is found (τ filter).
func TestAfterMidnightAndTau(t *testing.T) {
	b := newBuilder()
	// 10 min walk: 720 m along the streets, 553.85 m straight.
	straight := 720 / 1.3
	a, z := b.stop("A", straight, 0), b.stop("Z", 8000, 0)
	b.trip("N", "early", []int32{a, z}, hm(24, 30), hm(24, 50))
	b.trip("N", "tau", []int32{a, z}, hm(25, 40), hm(26, 0))
	b.trip("N", "late", []int32{a, z}, hm(26, 30), hm(26, 50))
	tt := b.build(t)
	r := plan(tt, ll(0, 0), ll(8000, 0), hm(25, 30), false, 3)
	var runs []string
	for _, it := range r.Itineraries {
		checkShape(t, it)
		runs = append(runs, it.Legs[1].Run)
	}
	if !slices.Equal(runs, []string{"tau", "late"}) {
		t.Fatalf("runs %v, want tau late", runs)
	}
	if it := r.Itineraries[0]; it.DepartAt != at(hm(25, 30)) || it.Legs[0].ArriveAt-it.Legs[0].DepartAt != 600 {
		t.Errorf("leaves %d (walk %d s), want %d (600 s)", it.DepartAt, it.Legs[0].ArriveAt-it.Legs[0].DepartAt, at(hm(25, 30)))
	}
	// One second later the 25:40 departure can no longer be reached.
	r = plan(tt, ll(0, 0), ll(8000, 0), hm(25, 30)+1, false, 3)
	if len(r.Itineraries) != 1 || r.Itineraries[0].Legs[1].Run != "late" {
		t.Errorf("after 25:30:01: %v", summary(r))
	}
}

// 7. FIFO split: a trip that overtakes another on the same stop sequence is still found.
func TestOvertakingTrip(t *testing.T) {
	b := newBuilder()
	a, m, z := b.stop("A", 0, 0), b.stop("B", 3000, 0), b.stop("C", 6000, 0)
	b.trip("L", "slow", []int32{a, m, z}, hm(10, 0), hm(10, 20), hm(10, 30))
	b.trip("L", "fast", []int32{a, m, z}, hm(10, 5), hm(10, 8), hm(10, 15))
	tt := b.build(t)
	r := plan(tt, ll(3000, 0), ll(6000, 0), hm(10, 6), false, 3)
	var runs []string
	for _, it := range r.Itineraries {
		checkShape(t, it)
		runs = append(runs, it.Legs[1].Run)
	}
	if !slices.Equal(runs, []string{"fast", "slow"}) {
		t.Fatalf("runs %v, want fast slow", runs)
	}
	if r.Itineraries[0].ArriveAt != at(hm(10, 15)) {
		t.Errorf("arrives %d, want 10:15", r.Itineraries[0].ArriveAt)
	}
}

// 8. No route: an empty list; points 800 m apart: walk_only set.
func TestNoRouteAndWalkOnly(t *testing.T) {
	b := newBuilder()
	a, z := b.stop("A", 0, 0), b.stop("Z", 5000, 0)
	b.trip("L", "r", []int32{a, z}, hm(10, 0), hm(10, 10))
	tt := b.build(t)
	r := plan(tt, ll(0, 3000), ll(800, 3000), hm(9, 0), false, 3)
	if r.Itineraries == nil || len(r.Itineraries) != 0 {
		t.Errorf("itineraries %v, want [] (not nil)", r.Itineraries)
	}
	d, w := walkOf(800)
	if r.WalkOnly == nil || r.WalkOnly.DistanceM != d || r.WalkOnly.DurationS != w {
		t.Errorf("walk_only %+v, want %d m %d s", r.WalkOnly, d, w)
	}
	if r = plan(tt, ll(0, 3000), ll(1100, 3000), hm(9, 0), false, 3); r.WalkOnly != nil {
		t.Errorf("walk_only %+v for 1100 m", r.WalkOnly)
	}
}

// 9. Access limit: a stop 1100 m away is not used; one 990 m away is.
func TestAccessLimit(t *testing.T) {
	for _, tc := range []struct {
		dist float64
		want int
	}{{1100, 0}, {990, 1}} {
		b := newBuilder()
		a, z := b.stop("A", tc.dist, 0), b.stop("Z", 8000, 0)
		b.trip("L", "r", []int32{a, z}, hm(10, 0), hm(10, 10))
		tt := b.build(t)
		if r := plan(tt, ll(0, 0), ll(8000, 0), hm(9, 0), false, 3); len(r.Itineraries) != tc.want {
			t.Errorf("stop %v m away: %v", tc.dist, summary(r))
		}
		// Egress too.
		if r := plan(tt, ll(tc.dist, 0), ll(8000+tc.dist, 0), hm(9, 0), false, 3); len(r.Itineraries) != tc.want {
			t.Errorf("stop %v m from the end: %v", tc.dist, summary(r))
		}
	}
}

// 10. Same-stop changes: 60 s at a bus stop, 180 s inside a metro station.
func TestStationChange(t *testing.T) {
	for _, tc := range []struct {
		gap  int32
		want int
	}{{179, 0}, {180, 1}} {
		b := newBuilder()
		a, m, z := b.station("A", 0, 0), b.station("M", 3000, 0), b.station("Z", 3000, 6000)
		b.trip("M1", "r1", []int32{a, m}, hm(10, 0), hm(10, 10))
		b.trip("M2", "r2", []int32{m, z}, hm(10, 10)+tc.gap, hm(10, 40))
		tt := b.build(t)
		r := plan(tt, ll(0, 0), ll(3000, 6000), hm(9, 30), false, 3)
		if len(r.Itineraries) != tc.want {
			t.Fatalf("gap %d s: %v", tc.gap, summary(r))
		}
		if tc.want == 0 {
			continue
		}
		it := r.Itineraries[0]
		checkShape(t, it)
		if w := it.Legs[0]; w.DistanceM != 0 || w.ArriveAt-w.DepartAt != 120 {
			t.Errorf("walk into the station %+v, want 0 m in 120 s", w)
		}
		if w := it.Legs[2]; w.DistanceM != 0 || w.ArriveAt-w.DepartAt != 120 {
			t.Errorf("change inside the station %+v, want 0 m in 120 s", w)
		}
	}
}

// Walks between a station and a bus stop add 120 s once; at most 4 rides.
func TestStationWalkAndRideLimit(t *testing.T) {
	b := newBuilder()
	s, bus, z := b.station("S", 0, 0), b.stop("B", 0, 100), b.stop("Z", 0, 5000)
	b.trip("M", "m", []int32{b.station("S0", -5000, 0), s}, hm(10, 0), hm(10, 10))
	b.trip("B", "b", []int32{bus, z}, hm(10, 20), hm(10, 30))
	tt := b.build(t)
	r := plan(tt, ll(-5000, 0), ll(0, 5000), hm(9, 0), false, 1)
	if len(r.Itineraries) != 1 {
		t.Fatalf("%v", summary(r))
	}
	d, w := walkOf(100)
	if l := r.Itineraries[0].Legs[2]; l.DistanceM != d || l.ArriveAt-l.DepartAt != w+120 {
		t.Errorf("station to bus stop %+v, want %d m in %d s", l, d, w+120)
	}

	// Five lines in a chain: five rides are one too many; four are found.
	for _, n := range []int{4, 5} {
		b := newBuilder()
		prev := b.stop("S0", 0, 0)
		for i := 1; i <= n; i++ {
			next := b.stop("S"+itoa(i), float64(i)*3000, 0)
			b.trip("L"+itoa(i), "r"+itoa(i), []int32{prev, next}, hm(10, i*10), hm(10, i*10+5))
			prev = next
		}
		tt := b.build(t)
		r := plan(tt, ll(0, 0), ll(float64(n)*3000, 0), hm(9, 0), false, 3)
		if want := map[int]int{4: 1, 5: 0}[n]; len(r.Itineraries) != want {
			t.Errorf("%d rides in a chain: %v", n, summary(r))
		}
	}
}

// 12. Leg shape: a ride's shape starts and ends at its board and alight stops; a loop line whose
// shape passes the board stop twice is cut at the right pass; without a shape, the stops' points.
func TestLegShapes(t *testing.T) {
	b := newBuilder()
	// Out east along y = 0 to x = 4000, back west along y = 20.
	var pts [][2]float64
	for x := 0.0; x <= 4000; x += 250 {
		p := ll(x, 0)
		pts = append(pts, [2]float64{p.Lat, p.Lon})
	}
	for x := 4000.0; x >= 0; x -= 250 {
		p := ll(x, 20)
		pts = append(pts, [2]float64{p.Lat, p.Lon})
	}
	b.in.Shapes = append(b.in.Shapes, pts)
	s := []int32{b.stop("S0", 0, 0), b.stop("S1", 1000, 0), b.stop("S2", 3000, 0), b.stop("S3", 4000, 10),
		b.stop("S4", 3000, 20), b.stop("S5", 1000, 20), b.stop("S6", 0, 20)}
	b.tripShape("LOOP", "loop", 0, s, hm(10, 0), hm(10, 5), hm(10, 15), hm(10, 20), hm(10, 25), hm(10, 35), hm(10, 40))
	far := b.stop("FAR", 20000, 0)
	b.trip("NOSHAPE", "ns", []int32{far, b.stop("F1", 21000, 0), b.stop("F2", 22000, 0)}, hm(11, 0), hm(11, 5), hm(11, 10))
	tt := b.build(t)

	length := func(shape [][2]float64) float64 {
		m := 0.0
		for i := 1; i < len(shape); i++ {
			m += geo.Dist(geo.XY(shape[i-1][0], shape[i-1][1]), geo.XY(shape[i][0], shape[i][1]))
		}
		return m
	}
	// S4 (inbound, 20 m from S2) to S6: the second pass, about 3000 m.
	r := plan(tt, ll(3000, 60), ll(-300, 20), hm(10, 0), false, 1)
	if len(r.Itineraries) != 1 {
		t.Fatalf("%v", summary(r))
	}
	ride := r.Itineraries[0].Legs[1]
	if *ride.From.Stop != "S4" || *ride.To.Stop != "S6" {
		t.Fatalf("ride %v -> %v, want S4 -> S6", *ride.From.Stop, *ride.To.Stop)
	}
	checkShape(t, r.Itineraries[0])
	if m := length(ride.Shape); math.Abs(m-3000) > 50 {
		t.Errorf("S4 -> S6 shape %.0f m, want ~3000", m)
	}
	// S1 to S2 outbound: about 2000 m.
	r = plan(tt, ll(1000, -60), ll(3000, -300), hm(10, 0), false, 1)
	if len(r.Itineraries) != 1 {
		t.Fatalf("%v", summary(r))
	}
	if ride := r.Itineraries[0].Legs[1]; *ride.From.Stop != "S1" || *ride.To.Stop != "S2" || math.Abs(length(ride.Shape)-2000) > 50 {
		t.Errorf("S1 -> S2: %v -> %v, %.0f m", *ride.From.Stop, *ride.To.Stop, length(ride.Shape))
	}
	// No shape: the stops' points, board to alight.
	r = plan(tt, ll(20000, 0), ll(22000, 0), hm(10, 0), false, 1)
	if len(r.Itineraries) != 1 {
		t.Fatalf("%v", summary(r))
	}
	if ride := r.Itineraries[0].Legs[1]; len(ride.Shape) != 3 || ride.Hops != 2 {
		t.Errorf("no-shape ride: %d points, %d hops; want 3, 2", len(ride.Shape), ride.Hops)
	}
}

// Ties: equal departure, arrival and transfers return one option; dominated options are dropped.
func TestDominance(t *testing.T) {
	b := newBuilder()
	a, z := b.stop("A", 0, 0), b.stop("Z", 5000, 0)
	b.trip("X", "x", []int32{a, z}, hm(10, 0), hm(10, 20))
	b.trip("Y", "y", []int32{a, z}, hm(10, 0), hm(10, 20))
	// Leaves earlier and arrives later: dominated by both.
	b.trip("W", "w", []int32{a, z}, hm(9, 55), hm(10, 25))
	tt := b.build(t)
	r := plan(tt, ll(0, 0), ll(5000, 0), hm(9, 0), false, 5)
	if len(r.Itineraries) != 1 {
		t.Errorf("itineraries %v, want one of X, Y", summary(r))
	}
}

// The JSON of the legs: walk and ride keys as the contract shows them; 5-decimal shapes.
func TestLegJSON(t *testing.T) {
	b := newBuilder()
	a, z := b.stop("A", 0, 0), b.stop("Z", 5000, 0)
	b.trip("L1", "r1", []int32{a, z}, hm(10, 0), hm(10, 10))
	tt := b.build(t)
	r := plan(tt, ll(-200, 0), ll(5000, 300), hm(9, 30), false, 3)
	raw, err := json.Marshal(r.Itineraries[0])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Legs []map[string]json.RawMessage `json:"legs"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	walk := []string{"arrive_at", "depart_at", "distance_m", "from", "kind", "shape", "to"}
	ride := []string{"arrive_at", "depart_at", "from", "headsign", "hops", "kind", "line", "mode", "run", "shape", "source", "to"}
	if k := keys(got.Legs[0]); !slices.Equal(k, walk) {
		t.Errorf("walk keys %v", k)
	}
	if k := keys(got.Legs[1]); !slices.Equal(k, ride) {
		t.Errorf("ride keys %v", k)
	}
	if string(got.Legs[1]["source"]) != `"timetable"` || string(got.Legs[0]["from"]) == "" {
		t.Errorf("ride source %s", got.Legs[1]["source"])
	}
	if !strings.Contains(string(got.Legs[0]["from"]), `"stop":null`) {
		t.Errorf("walk from %s: want stop null", got.Legs[0]["from"])
	}
	for _, leg := range got.Legs {
		if long := regexp.MustCompile(`\.\d{6,}`).FindString(string(leg["shape"])); long != "" {
			t.Errorf("shape %s: a coordinate with more than 5 decimals", leg["shape"])
		}
	}
}

// Run is a trip of the timetable by its Run id, with its times as instants.
func TestRunLookup(t *testing.T) {
	b := newBuilder()
	a, z := b.stop("A", 0, 0), b.stop("Z", 5000, 0)
	b.trip("L1", "oasa:run:T1@2415:2026-11-03", []int32{a, z}, hm(24, 15), hm(24, 30))
	tt := b.build(t)
	run, ok := tt.Run("oasa:run:T1@2415:2026-11-03")
	if !ok || run.Line != "L1" || run.Pattern != "p:L1" || len(run.Stops) != 2 || run.Stops[0].Stop != "A" ||
		run.Stops[1].Arr != at(hm(24, 30)) {
		t.Errorf("run %+v %v", run, ok)
	}
	if _, ok := tt.Run("oasa:run:nope:2026-11-03"); ok {
		t.Error("unknown run found")
	}
	if tt.Day() != "2026-11-03" {
		t.Errorf("day %q", tt.Day())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
