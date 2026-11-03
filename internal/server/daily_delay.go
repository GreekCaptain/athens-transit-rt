package server

import (
	"slices"
	"sort"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/match"
)

// dailyShifts says, for each result of one poll of line, how many seconds the run of OASA's daily
// schedule its bus left on starts after the GTFS trip it was matched to (0: unknown). Only on an
// expired GTFS, and only for a bus seen leaving whose run is clear (plannedOf); the delay shown is
// then against that run, the GTFS trip giving the times between stops. nil when none is known.
// The raw match (GTFS-RT, the history) keeps the GTFS delay. runs says, for the same results, the
// run each bus left on and how late it left (also when the shift is 0); nil when none is known.
func (a *App) dailyShifts(f *gtfs.Feed, line string, results []match.Result, now time.Time) (shifts []int64, runs []leftRun) {
	if !expired(f, now) {
		return nil, nil
	}
	today := gtfs.Midnight(now)
	e, ok := a.dailyEntryOf(line, today)
	if !ok || e.partial { // a line code failed: its runs may be missing, and a bus paired with another
		return nil, nil
	}
	d := e.d
	resp := dailyToSchedule(line, today.Format(time.DateOnly), d)
	shapes := lineShapes(f, line)
	loop := d.Circular || len(shapes) == 1 && len(resp.Directions) == 1
	type dirRuns struct {
		starts, times []time.Time
		took          []int
	}
	dirs := make([]dirRuns, len(resp.Directions))
	dirOf := map[int32]int{} // shape -> its direction; -1 when more than one
	for i, dir := range resp.Directions {
		var starts []time.Time
		for _, s := range shapesOf(shapes, dir.Headsign, loop) {
			if _, dup := dirOf[s]; dup {
				dirOf[s] = -1
			} else {
				dirOf[s] = i
			}
			got, _ := a.starts.of(f, line, s, now)
			starts = append(starts, got...)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
		times, took := pairStarts(dir.Departures, today, starts, now)
		dirs[i] = dirRuns{starts, times, took}
	}
	for i := range results {
		r := &results[i]
		if !r.Matched() || r.Waiting {
			continue
		}
		t := f.Trip(r.Trip)
		key, left, atEnd, ok := a.starts.departed(f, r.VehicleID, now)
		di, one := dirOf[t.Shape]
		if !ok || key != (startKey{line, t.Shape}) || !one || di < 0 || atEnd && 2*r.NextIndex < f.NumStops(t) {
			continue // at the end, matched to a trip barely begun: a loop's next run, not the one that left
		}
		k := onlyStart(dirs[di].starts, left)
		if k < 0 {
			continue
		}
		run, ok := plannedOf(dirs[di].times, dirs[di].took, dirs[di].starts, k)
		if !ok {
			continue
		}
		shift := int64(run.Sub(r.Day.Add(time.Duration(f.Start(t))*time.Second)) / time.Second)
		if dl := int64(r.Delay) - shift; dl < match.MinDelay || dl > match.MaxDelay {
			continue
		}
		if runs == nil {
			runs = make([]leftRun, len(results))
		}
		runs[i] = leftRun{start: run, late: int(left.Sub(run) / time.Second)}
		if shift == 0 {
			continue
		}
		if shifts == nil {
			shifts = make([]int64, len(results))
		}
		shifts[i] = shift
	}
	return shifts, runs
}

// leftRun is the run of OASA's daily schedule a bus left on (zero when not known) and how many
// seconds after its departure it left.
type leftRun struct {
	start time.Time
	late  int
}

// onlyStart is the index of the start at t, -1 when there is none or more than one (two buses
// that left at one time: which run each made is not known).
func onlyStart(starts []time.Time, t time.Time) int {
	k := slices.IndexFunc(starts, t.Equal)
	if k < 0 || slices.IndexFunc(starts[k+1:], t.Equal) >= 0 {
		return -1
	}
	return k
}

// shiftOf is the daily shift of result i of a line's data (dailyShifts), 0 when none.
func (d *lineData) shiftOf(i int) int64 {
	if i < len(d.daily) {
		return d.daily[i]
	}
	return 0
}
