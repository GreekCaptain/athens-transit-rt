package server

import (
	"strings"

	"github.com/angelospk/athens-transit-rt/internal/geo"
	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/staticsite"
)

// shapeEnds is the first and last stop of a shape, projected in metres.
type shapeEnds struct {
	first, last [2]float64
}

// shapeDirs is the direction of every shape of every line of the world's feed, built once per
// feed: line -> shape index -> 0 or 1.
func (w *world) shapeDirs(line string) map[int32]int {
	w.dirsOnce.Do(func() {
		w.dirs = map[string]map[int32]int{}
		for _, l := range w.feed.Lines() {
			w.dirs[l] = oasaDirections(w.feed, l, staticsite.LineInfo(w.feed, nil, l).Name)
		}
	})
	return w.dirs[line]
}

// oasaDirections is the direction (0 or 1) of every shape of a line. The OASA GTFS has no
// direction_id (rt#45): every trip says 0, so the direction comes from where the shape runs.
// The shape with the most trips is the reference; every other shape is the same way when its
// two ends are nearer that way's ends than the other way's: Dist(first,ref.first) +
// Dist(last,ref.last) <= Dist(first,ref.last) + Dist(last,ref.first). The triangle inequality
// keeps a short turn on the way it belongs to, and a loop ties to the reference (0).
//
// The reference then takes the group that heads to the place after the last " - " of the line
// name, which is the direction /v1's daily schedule calls 0 ("Go"); the other group is 1. A
// headsign naming both places or neither does not vote, and a name without " - " cannot tell
// the groups apart, so the reference stays 0. A line whose shapes all run one way is 0.
func oasaDirections(f *gtfs.Feed, line, name string) map[int32]int {
	trips := map[int32]int{}
	pats := map[int32]map[int32]int{} // shape -> pattern -> trips
	for _, ti := range f.TripsForLine(line) {
		t := f.Trip(ti)
		if t.Shape < 0 || len(f.Patterns[t.Pattern].Stops) == 0 {
			continue
		}
		trips[t.Shape]++
		if pats[t.Shape] == nil {
			pats[t.Shape] = map[int32]int{}
		}
		pats[t.Shape][t.Pattern]++
	}
	// The most common stop pattern names the ends of a shape (as staticsite.LineFileOf): a trip
	// that runs only part of it must not move them.
	ends := map[int32]shapeEnds{}
	for s, byPat := range pats {
		best, most := int32(-1), 0
		for p, n := range byPat {
			if n > most || n == most && p < best {
				best, most = p, n
			}
		}
		stops := f.Patterns[best].Stops
		first, last := f.Stops[stops[0]], f.Stops[stops[len(stops)-1]]
		ends[s] = shapeEnds{first: geo.XY(first.Lat, first.Lon), last: geo.XY(last.Lat, last.Lon)}
	}
	ref, most := int32(-1), 0
	for s := range ends {
		if ref < 0 || trips[s] > most || (trips[s] == most && s < ref) {
			ref, most = s, trips[s]
		}
	}
	if ref < 0 {
		return map[int32]int{}
	}
	same := make(map[int32]bool, len(ends))
	r := ends[ref]
	for s, e := range ends {
		same[s] = s == ref ||
			geo.Dist(e.first, r.first)+geo.Dist(e.last, r.last) <= geo.Dist(e.first, r.last)+geo.Dist(e.last, r.first)
	}

	vote := 0
	if i := strings.LastIndex(name, " - "); i >= 0 {
		goPlace, comePlace := name[i+3:], name
		if j := strings.Index(comePlace, " - "); j >= 0 {
			comePlace = comePlace[:j]
		}
		heads := lineShapes(f, line)
		for s := range ends {
			g := endMatches(heads[s], goPlace) && !endMatches(heads[s], comePlace)
			c := endMatches(heads[s], comePlace) && !endMatches(heads[s], goPlace)
			switch {
			case g && same[s], c && !same[s]:
				vote++
			case c && same[s], g && !same[s]:
				vote--
			}
		}
	}
	oneWay := true
	for _, v := range same {
		oneWay = oneWay && v
	}
	sameDir := 0
	if vote < 0 && !oneWay { // a line with one way (a loop) is 0 whatever its headsigns say
		sameDir = 1
	}
	out := make(map[int32]int, len(ends))
	for s := range ends {
		if same[s] {
			out[s] = sameDir
		} else {
			out[s] = 1 - sameDir
		}
	}
	return out
}
