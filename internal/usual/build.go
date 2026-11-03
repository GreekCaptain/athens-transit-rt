package usual

import (
	"compress/gzip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pass detection (the study's scripts/headway-study/passes.py).
const (
	maxInterpS  = 600  // a pass is interpolated between two fixes at most this far apart
	runGapS     = 900  // a longer silence starts a new run
	runBackM    = 300  // so does a jump back of more than this
	upstreamM   = 1500 // a run counts at a stop only if it starts this far before it,
	startM      = 200  // or this close to the start of the shape (matcher re-runs start mid route)
	minRepeatS  = 600  // the same bus at the same stop again sooner is jitter
	outageMinS  = 300  // this many seconds without any fix at all is a server outage
	defaultTrip = 3600 // trip time of a shape without one
	hourLayout  = "2006-01-02T15"
)

// ShapeStop is a stop's position along a shape, in metres (as history's s_m).
type ShapeStop struct {
	Stop  string
	Along float64
}

// ShapeInfo is what pass detection needs of a shape: its stops by Along (the first two of the
// pattern left out: buses wait there) and the shortest time between two passes of one bus at
// one stop (0.6 × the trip time).
type ShapeInfo struct {
	Stops  []ShapeStop
	MinGap int64
}

// Routes tells BuildDay what the trip and shape ids of the history rows mean.
type Routes interface {
	Trip(id string) (line, headsign string, ok bool)
	Shape(id string) *ShapeInfo // nil when unknown
}

// Key is a line and headsign.
type Key struct{ Line, Headsign string }

// Pass is one bus passing a stop: indices into Day.Stops and Day.Keys, Unix time.
type Pass struct {
	Stop, Key int32
	T         int64
}

// Day is one service day of passes. Outages are [from, to) Unix intervals with no fix at all.
type Day struct {
	Date    time.Time // service date, midnight UTC
	Outages [][2]int64
	Stops   []string
	Keys    []Key
	Passes  []Pass

	stopIdx map[string]int32
	keyIdx  map[Key]int32
}

func NewDay(date time.Time) *Day {
	return &Day{Date: date, stopIdx: map[string]int32{}, keyIdx: map[Key]int32{}}
}

// Add records a pass.
func (d *Day) Add(stop, line, headsign string, t int64) {
	s, ok := d.stopIdx[stop]
	if !ok {
		s = int32(len(d.Stops))
		d.stopIdx[stop] = s
		d.Stops = append(d.Stops, stop)
	}
	k := Key{line, headsign}
	ki, ok := d.keyIdx[k]
	if !ok {
		ki = int32(len(d.Keys))
		d.keyIdx[k] = ki
		d.Keys = append(d.Keys, k)
	}
	d.Passes = append(d.Passes, Pass{s, ki, t})
}

// run is one vehicle's progress along one shape.
type run struct {
	shape string
	info  *ShapeInfo
	s0    float64 // where the run started
	maxS  float64 // furthest position so far
	lastT int64
	lastS float64
}

// BuildDay detects the stop passes of one service date from the gzipped history archives in
// dir. Only rows of the GTFS version feed are used (trip and shape ids belong to it); every row
// counts for the outages. A nil Day means no row of that feed: nothing to learn from.
func BuildDay(dir string, date time.Time, feed string, r Routes) (*Day, error) {
	start, end := dayBounds(date)
	files, err := archives(dir, start-runGapS, end)
	if err != nil {
		return nil, err
	}
	d := NewDay(date)
	seen := make([]bool, (end-start+59)/60) // minutes with a fix
	runs := map[string]*run{}
	last := &lastPass{m: map[[3]string]int64{}, keep: minRepeatS}
	rows := 0
	for _, f := range files {
		err := readArchive(f, func(t int64, veh, rowFeed, trip, shape string, s float64) {
			if t >= start && t < end {
				seen[(t-start)/60] = true
			}
			if rowFeed != feed || shape == "" || s < 0 {
				return
			}
			rows++
			last.prune(t)
			step(d, runs, last, r, t, veh, trip, shape, s, start, end)
		})
		if err != nil {
			continue // a damaged archive: its minutes stay unseen
		}
	}
	if rows == 0 {
		return nil, nil
	}
	d.Outages = outages(seen, start, end)
	return d, nil
}

// step feeds one matched fix of a vehicle to its run and records the stops it crossed.
func step(d *Day, runs map[string]*run, last *lastPass, r Routes, t int64, veh, trip, shape string,
	s float64, start, end int64) {
	ru := runs[veh]
	if ru != nil && t <= ru.lastT {
		return // older or repeated fix
	}
	if ru == nil || ru.shape != shape || t-ru.lastT > runGapS || s < ru.lastS-runBackM {
		if ru != nil && ru.shape == shape {
			ru.s0, ru.maxS, ru.lastT, ru.lastS = s, s, t, s
			return
		}
		runs[veh] = &run{shape: shape, info: r.Shape(shape), s0: s, maxS: s, lastT: t, lastS: s}
		return
	}
	prevT, prevS, maxS := ru.lastT, ru.lastS, ru.maxS
	ru.lastT, ru.lastS = t, s
	if s <= maxS {
		return
	}
	ru.maxS = s
	if ru.info == nil || t-prevT > maxInterpS {
		return
	}
	line, head, ok := r.Trip(trip)
	if !ok {
		return
	}
	stops := ru.info.Stops
	i := sort.Search(len(stops), func(i int) bool { return stops[i].Along > maxS })
	for ; i < len(stops) && stops[i].Along <= s; i++ {
		a := stops[i].Along
		if !(ru.s0 <= a-upstreamM || ru.s0 <= startM) {
			continue
		}
		p := prevT + int64(math.Round((a-prevS)/(s-prevS)*float64(t-prevT)))
		k := [3]string{line, stops[i].Stop, veh}
		gap := max(ru.info.MinGap, minRepeatS)
		if lp, ok := last.m[k]; ok && p-lp < gap {
			continue
		}
		last.m[k] = p
		last.keep = max(last.keep, gap)
		if p >= start && p < end {
			d.Add(stops[i].Stop, line, head, p)
		}
	}
}

// lastPass is the last pass of each line, stop and vehicle, for the repeat rule. Passes older
// than the longest gap the rule needs (plus a run gap, as rows are only roughly in time order)
// are dropped every 10 min of fix time, so the map holds hours, not the whole day.
type lastPass struct {
	m         map[[3]string]int64
	keep      int64
	pruneNext int64
}

func (l *lastPass) prune(t int64) {
	if t < l.pruneNext {
		return
	}
	l.pruneNext = t + 600
	for k, v := range l.m {
		if v < t-l.keep-runGapS {
			delete(l.m, k)
		}
	}
}

// outages are the runs of at least outageMinS without a fix.
func outages(seen []bool, start, end int64) [][2]int64 {
	var out [][2]int64
	for i := 0; i < len(seen); {
		if seen[i] {
			i++
			continue
		}
		j := i
		for j < len(seen) && !seen[j] {
			j++
		}
		if int64(j-i)*60 >= outageMinS {
			out = append(out, [2]int64{start + int64(i)*60, min(start+int64(j)*60, end)})
		}
		i = j
	}
	return out
}

// archives lists the gzipped history archives whose UTC hour overlaps [from, to), in time order.
// Plain .csv files are still being written and are left out.
func archives(dir string, from, to int64) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type arch struct {
		name string
		h    int64
		n    int
	}
	var list []arch
	for _, e := range entries {
		rest, ok := strings.CutSuffix(e.Name(), ".csv.gz")
		if !ok {
			continue
		}
		hour, n := rest, 0
		if i := strings.LastIndexByte(rest, '.'); i >= 0 {
			if n, err = strconv.Atoi(rest[i+1:]); err != nil || n < 1 {
				continue
			}
			hour = rest[:i]
		}
		h, err := time.Parse(hourLayout, hour)
		if err != nil || h.Unix()+3600 <= from || h.Unix() >= to {
			continue
		}
		list = append(list, arch{e.Name(), h.Unix(), n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].h != list[j].h {
			return list[i].h < list[j].h
		}
		return list[i].n < list[j].n
	})
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = filepath.Join(dir, a.name)
	}
	return out, nil
}

// readArchive streams one archive's rows; s is -1 when unknown. A bad row is skipped.
func readArchive(path string, fn func(t int64, veh, feed, trip, shape string, s float64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	cr := csv.NewReader(zr)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	head, err := cr.Read()
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	for _, c := range []string{"fix_t", "veh", "gtfs", "trip_id", "shape_id", "s_m"} {
		if _, ok := col[c]; !ok {
			return fmt.Errorf("%s: no %s column", path, c)
		}
	}
	cT, cVeh, cFeed, cTrip, cShape, cS := col["fix_t"], col["veh"], col["gtfs"], col["trip_id"], col["shape_id"], col["s_m"]
	for {
		row, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue
			}
			return err
		}
		if len(row) != len(head) {
			continue // a row cut by a crash
		}
		t, err := strconv.ParseInt(row[cT], 10, 64)
		if err != nil {
			continue
		}
		s, err := strconv.ParseFloat(row[cS], 64)
		if err != nil || math.IsNaN(s) || math.IsInf(s, 0) {
			s = -1
		}
		fn(t, strings.Clone(row[cVeh]), row[cFeed], strings.Clone(row[cTrip]), strings.Clone(row[cShape]), s)
	}
}
