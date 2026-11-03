package usual

import (
	"sort"
	"sync"

	"github.com/angelospk/athens-transit-rt/internal/geo"
	"github.com/angelospk/athens-transit-rt/internal/gtfs"
)

// FeedRoutes reads trips and shapes from a GTFS feed. A shape's stops are those of its trip
// with the most stops, snapped onto the shape as the matcher does (so they agree with s_m).
func FeedRoutes(f *gtfs.Feed) Routes {
	return &feedRoutes{f: f, shapes: map[string]*ShapeInfo{}}
}

type feedRoutes struct {
	f      *gtfs.Feed
	once   sync.Once
	byID   map[string][]int32 // shape id -> trips
	mu     sync.Mutex
	shapes map[string]*ShapeInfo
}

func (r *feedRoutes) Trip(id string) (string, string, bool) {
	i := r.f.TripIndex(id)
	if i < 0 {
		return "", "", false
	}
	t := r.f.Trip(i)
	return r.f.Line(t), r.f.Headsign(t), true
}

func (r *feedRoutes) Shape(id string) *ShapeInfo {
	r.once.Do(func() {
		r.byID = map[string][]int32{}
		for i := range r.f.Trips {
			if s := r.f.ShapeID(&r.f.Trips[i]); s != "" {
				r.byID[s] = append(r.byID[s], int32(i))
			}
		}
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.shapes[id]; ok {
		return info
	}
	info := r.build(id)
	r.shapes[id] = info
	return info
}

func (r *feedRoutes) build(id string) *ShapeInfo {
	f := r.f
	trips := r.byID[id]
	if len(trips) == 0 {
		return nil
	}
	best := trips[0]
	durs := make([]int32, 0, len(trips))
	for _, ti := range trips {
		t := f.Trip(ti)
		if f.NumStops(t) > f.NumStops(f.Trip(best)) {
			best = ti
		}
		durs = append(durs, f.End(t)-f.Start(t))
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	trip := durs[len(durs)/2]
	if trip <= 0 {
		trip = defaultTrip
	}
	t := f.Trip(best)
	pts := f.Shapes[t.Shape].Points
	xy := make([][2]float64, len(pts))
	for i, p := range pts {
		xy[i] = geo.XY(p[0], p[1])
	}
	pat := f.Patterns[t.Pattern].Stops
	stops := make([][2]float64, len(pat))
	for i, s := range pat {
		stops[i] = geo.XY(f.Stops[s].Lat, f.Stops[s].Lon)
	}
	along := geo.SnapStops(geo.NewPolyline(xy), stops)
	if along == nil {
		return nil // s_m of this shape is along the stops, not the shape
	}
	info := &ShapeInfo{MinGap: int64(trip) * 6 / 10}
	for k := 2; k < len(pat); k++ {
		info.Stops = append(info.Stops, ShapeStop{f.Stops[pat[k]].ID, along[k]})
	}
	sort.SliceStable(info.Stops, func(i, j int) bool { return info.Stops[i].Along < info.Stops[j].Along })
	return info
}
