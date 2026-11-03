package match

import (
	"strings"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/gtfs/gtfstest"
)

// The StraightLine feed runs on Mondays only and ends 2026-10-06. Once it has expired, a
// vehicle whose shape has no trip around now on today's weekday takes the trips of that
// shape from the nearest weekday that has some (rt#51: 309Β runs shape 5863 only Wed-Fri).

func dayAt(d time.Time, h, m, s int) time.Time {
	return d.Add(time.Duration(h*3600+m*60+s) * time.Second)
}

func TestExpiredFeedTakesShapeFromAnotherWeekday(t *testing.T) {
	tuesday := time.Date(2026, 11, 3, 0, 0, 0, 0, gtfs.Athens)
	for _, kind := range []Kind{Greedy, Hungarian, Memory} {
		m := newMatcher(t, kind)
		r := m.MatchLine("L", []Obs{obs("v1", 2.5, dayAt(tuesday, 10, 24, 30))})[0]
		if tripID(m, r) != "T01" || r.Delay != 120 || r.NextIndex != 3 {
			t.Fatalf("kind %d: %+v trip %q", kind, r, tripID(m, r))
		}
		if !r.Day.Equal(tuesday) {
			t.Fatalf("kind %d: day %v, want today %v", kind, r.Day, tuesday)
		}
	}
}

func TestValidFeedKeepsItsWeekdays(t *testing.T) {
	tuesday := time.Date(2026, 9, 29, 0, 0, 0, 0, gtfs.Athens) // inside 2026-07-06..2026-10-06
	m := newMatcher(t, Hungarian)
	if r := m.MatchLine("L", []Obs{obs("v1", 2.5, dayAt(tuesday, 10, 24, 30))})[0]; r.Matched() {
		t.Fatalf("matched %s on a weekday without service", tripID(m, r))
	}
}

func TestExpiredFeedPrefersNearestWeekday(t *testing.T) {
	// The same timetable on Mondays (T..) and on Thursdays (U..): a Wednesday is one day from
	// Thursday and two from Monday.
	files := gtfstest.StraightLine{Stops: 5, Trips: 6, First: 10 * 3600, Headway: 10, Leg: 5}.Files()
	var trips, st strings.Builder
	trips.WriteString(files["trips.txt"])
	st.WriteString(files["stop_times.txt"])
	for _, l := range strings.Split(strings.TrimSpace(files["trips.txt"]), "\n")[1:] {
		trips.WriteString(strings.Replace(strings.Replace(l, ",T", ",U", 1), "day_1", "day_4", 1) + "\n")
	}
	for _, l := range strings.Split(strings.TrimSpace(files["stop_times.txt"]), "\n")[1:] {
		st.WriteString("U" + l[1:] + "\n")
	}
	files["trips.txt"], files["stop_times.txt"] = trips.String(), st.String()
	files["calendar.txt"] += "day_4,0,0,0,1,0,0,0,20260706,20261006\n"
	f, err := gtfs.LoadZip(gtfstest.Zip(t, files), gtfs.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := New(f, NewRouteMapper(f, noStops), Hungarian)
	wednesday := time.Date(2026, 11, 4, 0, 0, 0, 0, gtfs.Athens)
	r := m.MatchLine("L", []Obs{obs("v1", 2.5, dayAt(wednesday, 10, 24, 30))})[0]
	if tripID(m, r) != "U01" || !r.Day.Equal(wednesday) {
		t.Fatalf("%+v trip %q", r, tripID(m, r))
	}
}
