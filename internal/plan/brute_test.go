package plan

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/angelospk/athens-transit-rt/internal/geo"
)

// The rRAPTOR search against a brute force over every journey of up to 4 rides, on random small
// networks (overtaking trips, stations, loops of transfers): the same (departure, arrival,
// rides) options must come back, for last=false and last=true.

type triple struct {
	dep, arr int64
	rides    int
}

func bruteForce(in *Input, q Query) []triple {
	xy := make([][2]float64, len(in.Stops))
	for i, s := range in.Stops {
		xy[i] = geo.XY(s.Lat, s.Lon)
	}
	extra := func(p int) int32 {
		if in.Stops[p].Station {
			return stationExtra
		}
		return 0
	}
	from, to := geo.XY(q.From.Lat, q.From.Lon), geo.XY(q.To.Lat, q.To.Lon)
	access, egress := map[int]int32{}, map[int]int32{}
	for p := range in.Stops {
		if d := geo.Dist(from, xy[p]); d <= AccessM {
			access[p] = walkSecs(walkDist(d)) + extra(p)
		}
		if d := geo.Dist(to, xy[p]); d <= AccessM {
			egress[p] = walkSecs(walkDist(d)) + extra(p)
		}
	}
	after := int32(q.After - in.Ref.Unix())
	hi := int32(inf)
	if !q.Last {
		hi = after + Window
	}
	var all []triple
	var ride func(dep int32, rides int, p int, ready int32)
	ride = func(dep int32, rides int, p int, ready int32) {
		if rides == MaxRides {
			return
		}
		for ti := range in.Trips {
			tr := &in.Trips[ti]
			for i := 0; i+1 < len(tr.Times); i++ {
				if int(tr.Times[i].Stop) != p || tr.Times[i].Dep < ready {
					continue
				}
				d := dep
				if rides == 0 {
					d = tr.Times[i].Dep - access[p]
					if d < after || d > hi {
						continue
					}
				}
				for j := i + 1; j < len(tr.Times); j++ {
					a, s := tr.Times[j].Arr, int(tr.Times[j].Stop)
					if e, ok := egress[s]; ok {
						all = append(all, triple{in.Ref.Unix() + int64(d), in.Ref.Unix() + int64(a+e), rides + 1})
					}
					ride(d, rides+1, s, a+extra(s)+transferBuffer)
					for q := range in.Stops {
						if dist := geo.Dist(xy[s], xy[q]); q != s && dist <= TransferM {
							w := walkSecs(walkDist(dist)) + extra(s) + extra(q)
							ride(d, rides+1, q, a+w+transferBuffer)
						}
					}
				}
			}
		}
	}
	for p := range access {
		ride(0, 0, p, -inf)
	}
	js := make([]journey, len(all))
	for i, t := range all {
		js[i] = journey{dep: t.dep, arr: t.arr, rides: t.rides}
	}
	js = pareto(js)
	n := min(max(q.N, 1), MaxN)
	if !q.Last {
		sortJourneys(js, false)
	}
	js = js[:min(n, len(js))]
	sortJourneys(js, false)
	out := make([]triple, len(js))
	for i, j := range js {
		out[i] = triple{j.dep, j.arr, j.rides}
	}
	return out
}

func randomInput(rng *rand.Rand) *Input {
	b := newBuilder()
	nStops := 6 + rng.IntN(8)
	stops := make([]int32, nStops)
	for i := range stops {
		x, y := rng.Float64()*3000, rng.Float64()*3000
		if rng.IntN(4) == 0 {
			stops[i] = b.station(fmt.Sprint("S", i), x, y)
		} else {
			stops[i] = b.stop(fmt.Sprint("S", i), x, y)
		}
	}
	for line := range 3 + rng.IntN(5) {
		seq := slices.Clone(stops)
		rng.Shuffle(len(seq), func(i, j int) { seq[i], seq[j] = seq[j], seq[i] })
		seq = seq[:2+rng.IntN(4)]
		for k := range 2 + rng.IntN(6) {
			tm := hm(10, rng.IntN(120))
			times := make([]int32, len(seq))
			for i := range seq {
				times[i] = tm
				tm += int32(60 + rng.IntN(600))
			}
			b.trip(fmt.Sprint("L", line), fmt.Sprint("L", line, "-", k), seq, times...)
		}
	}
	return &b.in
}

func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	checked := 0
	for c := range 400 {
		in := randomInput(rng)
		tt := Build(in)
		for range 5 {
			q := Query{From: ll(rng.Float64()*3000, rng.Float64()*3000), To: ll(rng.Float64()*3000, rng.Float64()*3000),
				After: at(hm(9, 30+rng.IntN(120))), Last: rng.IntN(2) == 0, N: 1 + rng.IntN(5)}
			want := bruteForce(in, q)
			var got []triple
			for _, it := range tt.Plan(q).Itineraries {
				checkShape(t, it)
				got = append(got, triple{it.DepartAt, it.ArriveAt, it.Transfers + 1})
			}
			if !slices.Equal(got, want) {
				t.Fatalf("case %d, query %+v:\n got  %v\n want %v", c, q, got, want)
			}
			if len(want) > 0 {
				checked++
			}
		}
	}
	if checked < 300 {
		t.Errorf("only %d queries with options: the random networks are too sparse", checked)
	}
}
