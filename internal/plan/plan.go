// Package plan is the trip planner of /v2/plan (docs/CONTRACT.md §9, design
// docs/superpowers/specs/2026-11-03-trip-planner.md): RAPTOR and its range version (rRAPTOR) on
// flat arrays of stop times, built from one service day of every Mode. It knows nothing of the
// Sources: the server's adapters hand it Stops and Trips, and it answers with contract legs.
package plan

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/geo"
)

// Limits of contract §9 and the design's "Limits and their reasons".
const (
	AccessM        = 1000.0 // walk from `from` and to `to`, straight line
	TransferM      = 500.0  // walk between two rides, straight line
	walkFactor     = 1.3    // straight line × 1.3 for the streets
	stationExtra   = 120    // a walk that starts or ends at a metro or suburban Stop
	transferBuffer = 60     // the least wait between a walk and the next ride
	MaxRides       = 4
	Window         = 3 * 3600 // last=false looks this far past after
	MaxN           = 5
	cellM          = 500.0 // grid cell for the walk searches

	KindWalk = "walk"
	KindRide = "ride"
)

const inf = math.MaxInt32

// Stop is a Stop of any Mode. Station: a metro or suburban Stop (its walks take 120 s more).
type Stop struct {
	ID       string
	Lat, Lon float64
	Station  bool
}

// StopTime is one call of a trip: a Stop index of Input.Stops and times in seconds from Input.Ref.
type StopTime struct {
	Stop     int32
	Arr, Dep int32
}

// Trip is one run of a vehicle with its calls in order. Shape is an index into Input.Shapes, -1
// when the trip has none.
type Trip struct {
	Line, Mode, Headsign, Run, Pattern string
	Shape                              int32
	Times                              []StopTime
}

// Input is one service day of every Source, Mode-neutral. Ref is the service day's reference
// ("noon minus 12 h", as gtfs.ServiceDay); every time counts from it, past 24 h after midnight.
type Input struct {
	Day    string
	Ref    time.Time
	Stops  []Stop
	Trips  []Trip
	Shapes [][][2]float64 // (lat, lon) points; shared with the Sources, never changed
}

// Stats are the sizes of a Timetable, for the log and the benchmarks.
type Stats struct {
	Stops, Trips, Routes, Events, Transfers, Skipped int
}

type tripMeta struct {
	line, mode, headsign, run, pattern string
	shape                              int32
	route, slot                        int32
}

// Timetable is the planner's read-only data of one service day; Plan may run concurrently.
type Timetable struct {
	day    string
	ref    time.Time
	stops  []Stop
	xy     [][2]float64
	trips  []tripMeta
	shapes [][][2]float64

	rStopAt []int32 // route r's stops: rStops[rStopAt[r]:rStopAt[r+1]]
	rStops  []int32
	rTripAt []int32 // route r's trips (by slot): rTrips[rTripAt[r]:rTripAt[r+1]]
	rTrips  []int32
	rTimeAt []int32 // times of route r, slot j, stop i: [rTimeAt[r] + j*len + i]
	arr     []int32
	dep     []int32

	srAt    []int32 // stop p's routes: srRoute/srPos[srAt[p]:srAt[p+1]]
	srRoute []int32
	srPos   []int32

	tfAt   []int32 // stop p's transfers: tfTo/tfSecs[tfAt[p]:tfAt[p+1]]
	tfTo   []int32
	tfSecs []uint16 // walk seconds, station extras included, buffer not

	grid map[[2]int32][]int32

	stats Stats
	pool  sync.Pool
}

// Day is the service day ("2026-11-03") and Ref its reference.
func (t *Timetable) Day() string    { return t.day }
func (t *Timetable) Ref() time.Time { return t.ref }
func (t *Timetable) Stats() Stats   { return t.stats }

func walkDist(straight float64) int32 { return int32(math.Round(straight * walkFactor)) }

// walkSecs is ceil(d / 1.2 m/s).
func walkSecs(d int32) int32 { return (d*5 + 5) / 6 }

func cellOf(xy [2]float64) [2]int32 {
	return [2]int32{int32(math.Floor(xy[0] / cellM)), int32(math.Floor(xy[1] / cellM))}
}

// Build makes the Timetable of one service day. Trips with fewer than 2 calls, an unknown Stop
// or times that go back are skipped (Stats.Skipped).
func Build(in *Input) *Timetable {
	t := &Timetable{day: in.Day, ref: in.Ref, stops: in.Stops, shapes: in.Shapes, grid: map[[2]int32][]int32{}}
	t.xy = make([][2]float64, len(in.Stops))
	for i, s := range in.Stops {
		t.xy[i] = geo.XY(s.Lat, s.Lon)
		c := cellOf(t.xy[i])
		t.grid[c] = append(t.grid[c], int32(i))
	}
	t.trips = make([]tripMeta, len(in.Trips))
	for i := range in.Trips {
		tr := &in.Trips[i]
		t.trips[i] = tripMeta{line: tr.Line, mode: tr.Mode, headsign: tr.Headsign, run: tr.Run, pattern: tr.Pattern,
			shape: tr.Shape, route: -1}
	}
	t.buildRoutes(in)
	t.buildStopRoutes()
	t.buildTransfers()
	t.stats.Stops, t.stats.Routes, t.stats.Transfers = len(t.stops), len(t.rStopAt)-1, len(t.tfTo)
	t.stats.Events = len(t.arr)
	return t
}

func validTrip(tr *Trip, nStops int) bool {
	if len(tr.Times) < 2 {
		return false
	}
	for i, st := range tr.Times {
		if st.Stop < 0 || int(st.Stop) >= nStops || st.Dep < st.Arr || i > 0 && st.Arr < tr.Times[i-1].Dep {
			return false
		}
	}
	return true
}

// buildRoutes groups the trips by their sequence of Stops and splits each group so that no trip
// overtakes another on a route (RAPTOR's FIFO rule): a trip goes to the first sub-route whose
// last trip it does not overtake at any Stop, else to a new one.
func (t *Timetable) buildRoutes(in *Input) {
	groups := map[string][]int32{}
	var keys []string
	key := make([]byte, 0, 256)
	for i := range in.Trips {
		tr := &in.Trips[i]
		if !validTrip(tr, len(in.Stops)) {
			t.stats.Skipped++
			continue
		}
		t.stats.Trips++
		key = key[:0]
		for _, st := range tr.Times {
			key = append(key, byte(st.Stop), byte(st.Stop>>8), byte(st.Stop>>16), byte(st.Stop>>24))
		}
		k := string(key)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], int32(i))
	}
	events := 0
	for _, g := range groups {
		events += len(g) * len(in.Trips[g[0]].Times)
	}
	t.arr, t.dep = make([]int32, 0, events), make([]int32, 0, events)
	t.rTrips = make([]int32, 0, t.stats.Trips)
	t.rStopAt, t.rTripAt, t.rTimeAt = []int32{0}, []int32{0}, []int32{0}
	for _, k := range keys {
		g := groups[k]
		sort.SliceStable(g, func(a, b int) bool {
			ta, tb := in.Trips[g[a]].Times, in.Trips[g[b]].Times
			if ta[0].Dep != tb[0].Dep {
				return ta[0].Dep < tb[0].Dep
			}
			return ta[len(ta)-1].Arr < tb[len(tb)-1].Arr
		})
		var subs [][]int32
		for _, ti := range g {
			placed := false
			for s := range subs {
				if !overtakes(in.Trips[ti].Times, in.Trips[subs[s][len(subs[s])-1]].Times) {
					subs[s] = append(subs[s], ti)
					placed = true
					break
				}
			}
			if !placed {
				subs = append(subs, []int32{ti})
			}
		}
		for _, sub := range subs {
			r := int32(len(t.rStopAt) - 1)
			first := in.Trips[sub[0]].Times
			for _, st := range first {
				t.rStops = append(t.rStops, st.Stop)
			}
			t.rStopAt = append(t.rStopAt, int32(len(t.rStops)))
			for slot, ti := range sub {
				t.rTrips = append(t.rTrips, ti)
				t.trips[ti].route, t.trips[ti].slot = r, int32(slot)
				for _, st := range in.Trips[ti].Times {
					t.arr = append(t.arr, st.Arr)
					t.dep = append(t.dep, st.Dep)
				}
			}
			t.rTripAt = append(t.rTripAt, int32(len(t.rTrips)))
			t.rTimeAt = append(t.rTimeAt, int32(len(t.arr)))
		}
	}
}

// overtakes reports whether trip a, leaving after b, is earlier than b at some Stop.
func overtakes(a, b []StopTime) bool {
	for i := range a {
		if a[i].Arr < b[i].Arr || a[i].Dep < b[i].Dep {
			return true
		}
	}
	return false
}

func (t *Timetable) buildStopRoutes() {
	n := len(t.stops)
	count := make([]int32, n+1)
	for r := 0; r+1 < len(t.rStopAt); r++ {
		for _, p := range t.rStops[t.rStopAt[r]:t.rStopAt[r+1]] {
			count[p+1]++
		}
	}
	for p := 1; p <= n; p++ {
		count[p] += count[p-1]
	}
	t.srAt = slices.Clone(count)
	t.srRoute = make([]int32, count[n])
	t.srPos = make([]int32, count[n])
	fill := count[:n]
	for r := 0; r+1 < len(t.rStopAt); r++ {
		for i, p := range t.rStops[t.rStopAt[r]:t.rStopAt[r+1]] {
			t.srRoute[fill[p]], t.srPos[fill[p]] = int32(r), int32(i)
			fill[p]++
		}
	}
}

// near calls fn for every Stop within maxM (straight line) of xy, with the distance.
func (t *Timetable) near(xy [2]float64, maxM float64, fn func(p int32, d float64)) {
	c := cellOf(xy)
	reach := int32(math.Ceil(maxM / cellM))
	for dx := -reach; dx <= reach; dx++ {
		for dy := -reach; dy <= reach; dy++ {
			for _, p := range t.grid[[2]int32{c[0] + dx, c[1] + dy}] {
				if d := geo.Dist(xy, t.xy[p]); d <= maxM {
					fn(p, d)
				}
			}
		}
	}
}

func (t *Timetable) served(p int32) bool { return t.srAt[p+1] > t.srAt[p] }

func (t *Timetable) buildTransfers() {
	n := len(t.stops)
	t.tfAt = make([]int32, n+1)
	for p := range int32(n) {
		if t.served(p) {
			t.near(t.xy[p], TransferM, func(q int32, d float64) {
				if q != p && t.served(q) {
					t.tfTo = append(t.tfTo, q)
					t.tfSecs = append(t.tfSecs, uint16(t.walkTime(p, q, walkDist(d))))
				}
			})
		}
		t.tfAt[p+1] = int32(len(t.tfTo))
	}
	t.tfTo, t.tfSecs = slices.Clip(t.tfTo), slices.Clip(t.tfSecs)
}

// walkTime is the time of a walk of d metres between Stops p and q (p == q: a change inside one
// Stop, 0 m): 120 s more for each metro or suburban end, once when both ends are the same Stop.
func (t *Timetable) walkTime(p, q int32, d int32) int32 {
	s := walkSecs(d)
	if t.stops[p].Station {
		s += stationExtra
	}
	if q != p && t.stops[q].Station {
		s += stationExtra
	}
	return s
}

func (t *Timetable) stationExtraOf(p int32) int32 {
	if t.stops[p].Station {
		return stationExtra
	}
	return 0
}

// ---- Query ----

// LatLon is a point in decimal degrees.
type LatLon struct{ Lat, Lon float64 }

// Query is one /v2/plan question. After is Unix seconds; N is 1-5.
type Query struct {
	From, To LatLon
	After    int64
	Last     bool
	N        int
}

// Place is a leg end: a Stop (with its point) or a query point (Stop nil).
type Place struct {
	Stop *string `json:"stop"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// Leg is a walk or a ride (contract §9); MarshalJSON writes the keys of its kind only.
type Leg struct {
	Kind      string
	From, To  Place
	DepartAt  int64
	ArriveAt  int64
	DistanceM int // walk
	Line      string
	Mode      string
	Headsign  string
	Run       string
	Hops      int
	Shape     [][2]float64

	trip       int32 // ride: the trip and its board and alight positions on its route
	board, off int32
}

func (l Leg) MarshalJSON() ([]byte, error) {
	if l.Kind == KindWalk {
		return json.Marshal(struct {
			Kind      string       `json:"kind"`
			From      Place        `json:"from"`
			To        Place        `json:"to"`
			DepartAt  int64        `json:"depart_at"`
			ArriveAt  int64        `json:"arrive_at"`
			DistanceM int          `json:"distance_m"`
			Shape     [][2]float64 `json:"shape"`
		}{l.Kind, l.From, l.To, l.DepartAt, l.ArriveAt, l.DistanceM, l.Shape})
	}
	return json.Marshal(struct {
		Kind     string       `json:"kind"`
		Line     string       `json:"line"`
		Mode     string       `json:"mode"`
		Headsign string       `json:"headsign"`
		Run      string       `json:"run"`
		From     Place        `json:"from"`
		To       Place        `json:"to"`
		DepartAt int64        `json:"depart_at"`
		ArriveAt int64        `json:"arrive_at"`
		Hops     int          `json:"hops"`
		Source   string       `json:"source"`
		Shape    [][2]float64 `json:"shape"`
	}{l.Kind, l.Line, l.Mode, l.Headsign, l.Run, l.From, l.To, l.DepartAt, l.ArriveAt, l.Hops, "timetable", l.Shape})
}

// Itinerary is one option from `from` to `to`.
type Itinerary struct {
	DepartAt  int64    `json:"depart_at"`
	ArriveAt  int64    `json:"arrive_at"`
	DurationS int64    `json:"duration_s"`
	Transfers int      `json:"transfers"`
	WalkM     int      `json:"walk_m"`
	Modes     []string `json:"modes"`
	Legs      []Leg    `json:"legs"`
}

// WalkOnly is the walk from `from` to `to` when they are at most 1000 m apart.
type WalkOnly struct {
	DistanceM int   `json:"distance_m"`
	DurationS int64 `json:"duration_s"`
}

// Result is the answer to a Query. Itineraries is never nil.
type Result struct {
	Itineraries []Itinerary
	WalkOnly    *WalkOnly
}

// Walk is the walk from `from` to `to` when they are at most 1000 m apart (straight line), else nil.
func Walk(from, to LatLon) *WalkOnly {
	d := geo.Dist(geo.XY(from.Lat, from.Lon), geo.XY(to.Lat, to.Lon))
	if d > AccessM {
		return nil
	}
	wd := walkDist(d)
	return &WalkOnly{DistanceM: int(wd), DurationS: int64(walkSecs(wd))}
}

// labels are one query's RAPTOR labels, reused through the Timetable's pool. Round k of r holds
// the arrivals by a k-th ride; round k of b the earliest boarding after k rides (0: the walk
// from `from`).
type labels struct {
	r, b                   [MaxRides + 1][]int32
	rTrip, rBoard, rAlight [MaxRides + 1][]int32
	rRound                 [MaxRides + 1][]int8
	bFrom                  [MaxRides + 1][]int32 // the Stop whose ride arrival led here
	access, egress         []int32               // walk seconds from `from` / to `to`, -1 out of reach
	accessD, egressD       []int32               // their distances
	markB, markR           []bool
	listB, listR, next     []int32
	routeQ                 []int32 // first position to scan per route, -1
	routes                 []int32
	target                 [MaxRides + 1]int32
}

func (t *Timetable) getLabels() *labels {
	if l, ok := t.pool.Get().(*labels); ok {
		return l
	}
	n, nr := len(t.stops), len(t.rStopAt)-1
	l := &labels{access: make([]int32, n), egress: make([]int32, n), accessD: make([]int32, n), egressD: make([]int32, n),
		markB: make([]bool, n), markR: make([]bool, n), routeQ: make([]int32, nr)}
	for k := range MaxRides + 1 {
		l.r[k], l.b[k] = make([]int32, n), make([]int32, n)
		l.rTrip[k], l.rBoard[k], l.rAlight[k] = make([]int32, n), make([]int32, n), make([]int32, n)
		l.rRound[k], l.bFrom[k] = make([]int8, n), make([]int32, n)
	}
	return l
}

func (l *labels) reset() {
	for k := range MaxRides + 1 {
		fill(l.r[k], inf)
		fill(l.b[k], inf)
		l.target[k] = inf
	}
	fill(l.access, -1)
	fill(l.egress, -1)
	fill(l.routeQ, -1)
}

func fill(s []int32, v int32) {
	for i := range s {
		s[i] = v
	}
}

type event struct {
	tau  int32
	stop int32
}

type journey struct {
	dep, arr int64
	rides    int
	legs     []Leg
}

func round5(v float64) float64 { return math.Round(v*1e5) / 1e5 }

func placeOf(p LatLon) Place { return Place{Lat: round5(p.Lat), Lon: round5(p.Lon)} }

func (t *Timetable) place(p int32) Place {
	id := t.stops[p].ID
	return Place{Stop: &id, Lat: round5(t.stops[p].Lat), Lon: round5(t.stops[p].Lon)}
}

// Plan answers a Query: the first n options leaving at or after After (at most 3 h later), or
// with Last the last n of the service day; Pareto over (later departure, earlier arrival, fewer
// transfers), sorted by departure, arrival, transfers.
func (t *Timetable) Plan(q Query) Result {
	res := Result{Itineraries: []Itinerary{}}
	from, to := geo.XY(q.From.Lat, q.From.Lon), geo.XY(q.To.Lat, q.To.Lon)
	res.WalkOnly = Walk(q.From, q.To)
	n := min(max(q.N, 1), MaxN)
	if len(t.rStopAt) < 2 {
		return res
	}
	l := t.getLabels()
	defer t.pool.Put(l)
	l.reset()

	var accessStops []int32
	t.near(from, AccessM, func(p int32, d float64) {
		if t.served(p) {
			wd := walkDist(d)
			l.access[p], l.accessD[p] = walkSecs(wd)+t.stationExtraOf(p), wd
			accessStops = append(accessStops, p)
		}
	})
	anyEgress := false
	t.near(to, AccessM, func(p int32, d float64) {
		if t.served(p) {
			wd := walkDist(d)
			l.egress[p], l.egressD[p] = walkSecs(wd)+t.stationExtraOf(p), wd
			anyEgress = true
		}
	})
	if len(accessStops) == 0 || !anyEgress {
		return res
	}
	after := clamp32(q.After - t.ref.Unix())
	hi := int32(inf)
	if !q.Last {
		hi = clamp32(int64(after) + Window)
	}
	events := t.events(l, accessStops, after, hi)
	ctx := &search{t: t, l: l, from: q.From, to: q.To}
	for i := 0; i < len(events); {
		tau := events[i].tau
		l.listB = l.listB[:0]
		for ; i < len(events) && events[i].tau == tau; i++ {
			p := events[i].stop
			if v := tau + l.access[p]; v < l.b[0][p] {
				l.b[0][p] = v
				if !l.markB[p] {
					l.markB[p] = true
					l.listB = append(l.listB, p)
				}
			}
		}
		ctx.run()
		if q.Last && len(ctx.found) >= n && len(pareto(slices.Clone(ctx.found))) >= n {
			break
		}
	}
	js := pareto(ctx.found)
	if q.Last {
		// js is sorted latest departure first (ties: earlier arrival, fewer rides).
		js = js[:min(n, len(js))]
	} else {
		sortJourneys(js, false)
		js = js[:min(n, len(js))]
	}
	sortJourneys(js, false)
	for _, j := range js {
		res.Itineraries = append(res.Itineraries, t.itinerary(j))
	}
	return res
}

func clamp32(v int64) int32 {
	return int32(min(max(v, -inf), inf-1))
}

// events are the origin times τ = departure − walk of every departure from an access Stop with
// after <= τ <= hi, latest first, each (τ, Stop) once.
func (t *Timetable) events(l *labels, access []int32, after, hi int32) []event {
	var out []event
	for _, p := range access {
		w := l.access[p]
		for k := t.srAt[p]; k < t.srAt[p+1]; k++ {
			r, pos := t.srRoute[k], t.srPos[k]
			length := t.rStopAt[r+1] - t.rStopAt[r]
			if pos == length-1 {
				continue
			}
			nt := t.rTripAt[r+1] - t.rTripAt[r]
			base := t.rTimeAt[r] + pos
			j := int32(sort.Search(int(nt), func(j int) bool { return t.dep[base+int32(j)*length]-w >= after }))
			for ; j < nt; j++ {
				tau := t.dep[base+j*length] - w
				if tau > hi {
					break
				}
				out = append(out, event{tau, p})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].tau != out[j].tau {
			return out[i].tau > out[j].tau
		}
		return out[i].stop < out[j].stop
	})
	return slices.Compact(out)
}

// search is one query's rRAPTOR state across its runs.
type search struct {
	t        *Timetable
	l        *labels
	from, to LatLon
	found    []journey
}

// best is the least of round 0..k of a label array: a label no earlier than it is dominated
// (same or fewer rides, leaving no earlier, arriving no earlier).
func best(a *[MaxRides + 1][]int32, k int, p int32) (int32, int) {
	v, at := int32(inf), -1
	for j := 0; j <= k; j++ {
		if a[j][p] < v {
			v, at = a[j][p], j
		}
	}
	return v, at
}

// run is one RAPTOR run from the Stops marked in listB (round 0), reusing the labels of the
// later runs as upper bounds.
func (s *search) run() {
	t, l := s.t, s.l
	for k := 1; k <= MaxRides && len(l.listB) > 0; k++ {
		// Routes through the Stops whose boarding improved in round k-1.
		l.routes = l.routes[:0]
		for _, p := range l.listB {
			l.markB[p] = false
			for x := t.srAt[p]; x < t.srAt[p+1]; x++ {
				r, pos := t.srRoute[x], t.srPos[x]
				switch q := l.routeQ[r]; {
				case q < 0:
					l.routeQ[r] = pos
					l.routes = append(l.routes, r)
				case pos < q:
					l.routeQ[r] = pos
				}
			}
		}
		l.listR = l.listR[:0]
		for _, r := range l.routes {
			s.scan(k, r, l.routeQ[r])
			l.routeQ[r] = -1
		}
		// The best arrival at `to` with k rides.
		bestArr, bestP := int32(inf), int32(-1)
		for _, p := range l.listR {
			if e := l.egress[p]; e >= 0 && l.r[k][p]+e < l.target[k] && l.r[k][p]+e < bestArr {
				bestArr, bestP = l.r[k][p]+e, p
			}
		}
		if bestP >= 0 {
			if j, ok := s.reconstruct(k, bestP); ok {
				s.found = append(s.found, j)
			}
			for j := k; j <= MaxRides; j++ {
				l.target[j] = min(l.target[j], bestArr)
			}
		}
		// Walks and changes to the next boarding.
		l.next = l.next[:0]
		for _, p := range l.listR {
			l.markR[p] = false
			if k == MaxRides {
				continue
			}
			arr := l.r[k][p]
			s.relax(k, p, p, arr+t.stationExtraOf(p)+transferBuffer)
			for x := t.tfAt[p]; x < t.tfAt[p+1]; x++ {
				s.relax(k, t.tfTo[x], p, arr+int32(t.tfSecs[x])+transferBuffer)
			}
		}
		l.listB, l.next = l.next, l.listB
	}
	for _, p := range l.listB {
		l.markB[p] = false
	}
	l.listB = l.listB[:0]
}

func (s *search) relax(k int, q, from, v int32) {
	l := s.l
	if b, _ := best(&l.b, k, q); v >= b || v >= l.target[k+1] {
		return
	}
	l.b[k][q], l.bFrom[k][q] = v, from
	if !l.markB[q] {
		l.markB[q] = true
		l.next = append(l.next, q)
	}
}

// scan runs route r in round k from position start: the trip in hand improves the arrivals; a
// Stop where an earlier trip can be caught switches to it.
func (s *search) scan(k int, r, start int32) {
	t, l := s.t, s.l
	stops := t.rStops[t.rStopAt[r]:t.rStopAt[r+1]]
	length := int32(len(stops))
	nt := t.rTripAt[r+1] - t.rTripAt[r]
	base := t.rTimeAt[r]
	cur, board, round := int32(-1), int32(0), int8(0)
	for i := start; i < length; i++ {
		p := stops[i]
		if cur >= 0 {
			arr := t.arr[base+cur*length+i]
			if b, _ := best(&l.r, k, p); arr < b && arr < l.target[k] {
				l.r[k][p] = arr
				l.rTrip[k][p], l.rBoard[k][p], l.rAlight[k][p], l.rRound[k][p] = t.rTrips[t.rTripAt[r]+cur], board, i, round
				if !l.markR[p] {
					l.markR[p] = true
					l.listR = append(l.listR, p)
				}
			}
		}
		if i == length-1 {
			break
		}
		bt, at := best(&l.b, k-1, p)
		if bt == inf || cur >= 0 && bt > t.dep[base+cur*length+i] {
			continue
		}
		hiSlot := nt
		if cur >= 0 {
			hiSlot = cur
		}
		j := int32(sort.Search(int(hiSlot), func(j int) bool { return t.dep[base+int32(j)*length+i] >= bt }))
		if j < hiSlot {
			cur, board, round = j, i, int8(at)
		}
	}
}

// reconstruct follows the labels back from a k-th ride arriving at p into a journey; ok is false
// when the chain does not hold (it never should).
func (s *search) reconstruct(k int, p int32) (journey, bool) {
	t, l := s.t, s.l
	var legs []Leg // reversed
	cur, round := p, k
	lastArr := l.r[k][p]
	legs = append(legs, Leg{Kind: KindWalk, From: t.place(p), To: placeOf(s.to),
		DepartAt: int64(lastArr), ArriveAt: int64(lastArr + l.egress[p]), DistanceM: int(l.egressD[p])})
	rides := 0
	for round > 0 {
		ti := l.rTrip[round][cur]
		m := &t.trips[ti]
		r := m.route
		length := t.rStopAt[r+1] - t.rStopAt[r]
		base := t.rTimeAt[r] + m.slot*length
		b, off := l.rBoard[round][cur], l.rAlight[round][cur]
		bStop := t.rStops[t.rStopAt[r]+b]
		dep, arr := t.dep[base+b], t.arr[base+off]
		if arr != l.r[round][cur] {
			return journey{}, false
		}
		legs = append(legs, Leg{Kind: KindRide, From: t.place(bStop), To: t.place(cur), DepartAt: int64(dep), ArriveAt: int64(arr),
			Line: m.line, Mode: m.mode, Headsign: m.headsign, Run: m.run, Hops: int(off - b), trip: ti, board: b, off: off})
		rides++
		br := int(l.rRound[round][cur])
		if br == 0 {
			w := l.access[bStop]
			if w < 0 {
				return journey{}, false
			}
			legs = append(legs, Leg{Kind: KindWalk, From: placeOf(s.from), To: t.place(bStop),
				DepartAt: int64(dep - w), ArriveAt: int64(dep), DistanceM: int(l.accessD[bStop])})
			break
		}
		prev := l.bFrom[br][bStop]
		prevArr := l.r[br][prev]
		d := int32(0)
		if prev != bStop {
			d = walkDist(geo.Dist(t.xy[prev], t.xy[bStop]))
		}
		walk := t.walkTime(prev, bStop, d)
		if prevArr+walk+transferBuffer > dep {
			return journey{}, false
		}
		legs = append(legs, Leg{Kind: KindWalk, From: t.place(prev), To: t.place(bStop),
			DepartAt: int64(prevArr), ArriveAt: int64(prevArr + walk), DistanceM: int(d)})
		cur, round = prev, br
	}
	slices.Reverse(legs)
	refUnix := t.ref.Unix()
	for i := range legs {
		legs[i].DepartAt += refUnix
		legs[i].ArriveAt += refUnix
	}
	return journey{dep: legs[0].DepartAt, arr: legs[len(legs)-1].ArriveAt, rides: rides, legs: legs}, true
}

// sortJourneys orders by departure (latest first when latest), then earlier arrival, then fewer
// rides.
func sortJourneys(js []journey, latest bool) {
	sort.SliceStable(js, func(a, b int) bool {
		x, y := js[a], js[b]
		if x.dep != y.dep {
			return x.dep > y.dep == latest
		}
		if x.arr != y.arr {
			return x.arr < y.arr
		}
		return x.rides < y.rides
	})
}

// pareto drops every journey another one dominates: it leaves at the same time or later,
// arrives at the same time or earlier, with no more rides (one of equals is kept). The result is
// sorted latest departure first.
func pareto(js []journey) []journey {
	sortJourneys(js, true)
	var out []journey
	for _, j := range js {
		dominated := false
		for _, k := range out {
			if k.dep >= j.dep && k.arr <= j.arr && k.rides <= j.rides {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, j)
		}
	}
	return out
}

func (t *Timetable) itinerary(j journey) Itinerary {
	it := Itinerary{DepartAt: j.dep, ArriveAt: j.arr, DurationS: j.arr - j.dep, Transfers: j.rides - 1,
		Modes: []string{}, Legs: j.legs}
	for i := range it.Legs {
		leg := &it.Legs[i]
		if leg.Kind == KindWalk {
			it.WalkM += leg.DistanceM
			leg.Shape = [][2]float64{{leg.From.Lat, leg.From.Lon}, {leg.To.Lat, leg.To.Lon}}
			continue
		}
		if !slices.Contains(it.Modes, leg.Mode) {
			it.Modes = append(it.Modes, leg.Mode)
		}
		leg.Shape = t.rideShape(leg)
	}
	return it
}

// rideShape is the trip's shape cut from the board Stop to the alight Stop (the Stops snapped
// onto it in ride order, so a loop is cut at the right pass), else the Stops' own points.
func (t *Timetable) rideShape(leg *Leg) [][2]float64 {
	m := &t.trips[leg.trip]
	stops := t.rStops[t.rStopAt[m.route]:t.rStopAt[m.route+1]]
	first, last := [2]float64{leg.From.Lat, leg.From.Lon}, [2]float64{leg.To.Lat, leg.To.Lon}
	if m.shape >= 0 && int(m.shape) < len(t.shapes) && len(t.shapes[m.shape]) >= 2 {
		pts := t.shapes[m.shape]
		xy := make([][2]float64, len(pts))
		for i, p := range pts {
			xy[i] = geo.XY(p[0], p[1])
		}
		line := geo.NewPolyline(xy)
		sxy := make([][2]float64, len(stops))
		for i, p := range stops {
			sxy[i] = t.xy[p]
		}
		if along := geo.SnapStops(line, sxy); along != nil {
			cut := line.Slice(along[leg.board], along[leg.off])
			out := [][2]float64{first}
			for _, p := range cut[1 : len(cut)-1] {
				lat, lon := geo.LatLon(p)
				out = append(out, [2]float64{round5(lat), round5(lon)})
			}
			return append(out, last)
		}
	}
	out := [][2]float64{first}
	for _, p := range stops[leg.board+1 : leg.off] {
		out = append(out, [2]float64{round5(t.stops[p].Lat), round5(t.stops[p].Lon)})
	}
	return append(out, last)
}

// RunStop is one call of a Run, times as Unix seconds.
type RunStop struct {
	Stop     string
	Arr, Dep int64
}

// RunInfo is a trip of the Timetable found by its Run id.
type RunInfo struct {
	Line, Mode, Headsign, Pattern string
	Stops                         []RunStop
}

// Run finds a trip by its Run id (a linear scan: for the rare lookups of /v2/runs after
// midnight).
func (t *Timetable) Run(id string) (RunInfo, bool) {
	for ti := range t.trips {
		m := &t.trips[ti]
		if m.run != id || m.route < 0 {
			continue
		}
		stops := t.rStops[t.rStopAt[m.route]:t.rStopAt[m.route+1]]
		base := t.rTimeAt[m.route] + m.slot*int32(len(stops))
		out := RunInfo{Line: m.line, Mode: m.mode, Headsign: m.headsign, Pattern: m.pattern, Stops: make([]RunStop, len(stops))}
		ref := t.ref.Unix()
		for i, p := range stops {
			out.Stops[i] = RunStop{Stop: t.stops[p].ID, Arr: ref + int64(t.arr[base+int32(i)]), Dep: ref + int64(t.dep[base+int32(i)])}
		}
		return out, true
	}
	return RunInfo{}, false
}
