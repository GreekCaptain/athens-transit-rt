package server

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/model"
	"github.com/angelospk/athens-transit-rt/internal/telematics"
	"github.com/angelospk/athens-transit-rt/internal/usual"
)

const (
	// usualAfter: a service day is built this long after it ends, when its last hourly
	// archive is sealed.
	usualAfter = 90 * time.Minute
	// usualGuardS: a usual next time is shown only with an Arrival of the same line and
	// headsign this close to it (today's timetable has such a run, or a bus is coming).
	usualGuardS = 15 * 60
)

// usualPasses keeps the stop passes of the past days (contract rev 18 `usual`, map#24): at
// start, then hourly, it builds each finished day of history that has no day file yet (one
// read of 25-30 archives per day, no OASA call) and drops the days older than
// usual.TrainDays.
func (a *App) usualPasses(ctx context.Context) {
	st, err := usual.OpenStore(filepath.Join(a.cfg.StateDir, "usual"))
	if err != nil {
		a.log.Warn("usual passes", "err", err)
		return
	}
	a.usual.Store(st)
	tried := map[time.Time]string{}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		a.buildUsual(st, tried)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// buildUsual builds the missing finished days of the training window. tried remembers, per
// day, the GTFS version a build found no rows of, so a day without history is read once per
// feed; a failed build is tried again at the next call.
func (a *App) buildUsual(st *usual.Store, tried map[time.Time]string) {
	w := a.world()
	feed := worldVersion(w)
	if feed == "" {
		return
	}
	now := a.now()
	today := usual.ServiceDate(now)
	first := today.AddDate(0, 0, -usual.TrainDays)
	st.Prune(first)
	var routes usual.Routes
	for d := first; d.Before(today); d = d.AddDate(0, 0, 1) {
		if st.Has(d) || tried[d] == feed || now.Before(usual.DayEnd(d).Add(usualAfter)) {
			continue
		}
		if routes == nil {
			routes = usual.FeedRoutes(w.feed)
		}
		start := time.Now()
		a.histBuild.Lock()
		day, err := usual.BuildDay(filepath.Join(a.cfg.StateDir, "history"), d, feed, routes)
		if err == nil && day != nil {
			err = usual.WriteDay(filepath.Join(a.cfg.StateDir, "usual"), day)
		}
		a.histBuild.Unlock()
		if err == nil && day != nil {
			err = st.Load(d)
		}
		if err != nil {
			a.log.Warn("usual passes", "day", d.Format(time.DateOnly), "err", err) // retried next hour
			continue
		}
		if day == nil {
			tried[d] = feed
		} else {
			a.log.Info("usual passes built", "day", d.Format(time.DateOnly), "passes", len(day.Passes),
				"stops", len(day.Stops), "outages", len(day.Outages), "took", time.Since(start).Round(time.Millisecond))
		}
	}
}

// usualRower is an Adapter whose Stops have a usual service (OASA buses only).
type usualRower interface {
	Usual(stopID string, now time.Time, arrivals []model.Arrival) []model.Usual
}

// Usual is the usual service at an OASA stop, guarded by the stop's Arrivals.
func (o *oasaAdapter) Usual(stopID string, now time.Time, arrivals []model.Arrival) []model.Usual {
	w, i, err := o.stop(stopID)
	if err != nil {
		return nil
	}
	out := o.a.usualAt(w, i, now, func() map[[2]string][]int64 {
		near := map[[2]string][]int64{}
		for _, ar := range arrivals {
			k := [2]string{strings.TrimPrefix(ar.Line, oasaPrefix), ar.Headsign}
			near[k] = append(near[k], ar.At)
		}
		return near
	})
	for k := range out {
		out[k].Line = oasaPrefix + out[k].Line
	}
	return out
}

// usualAt is the usual service at stop i, lines without the namespace (as /v1). A next time
// stays only when guard (asked only then) has a time of the same line and headsign within
// usualGuardS of it: the past weeks do not know that today has no such run. nil without a
// store.
func (a *App) usualAt(w *world, i int32, now time.Time, guard func() map[[2]string][]int64) []model.Usual {
	st := a.usual.Load()
	if st == nil {
		return nil
	}
	var near map[[2]string][]int64
	out := []model.Usual{}
	for _, r := range st.Query(w.feed.Stops[i].ID, now) {
		u := model.Usual{Line: r.Line, Headsign: r.Headsign, DayType: r.DayType, Days: r.Days,
			HeadwayMin: r.HeadwayMin, NextHourHeadwayMin: r.NextHourHeadwayMin, NextAt: r.NextAt, SpreadS: r.SpreadS}
		if u.NextAt != nil {
			if near == nil {
				near = guard()
			}
			if !guarded(near[[2]string{u.Line, u.Headsign}], *u.NextAt) {
				u.NextAt, u.SpreadS = nil, nil
			}
		}
		if u.NextAt != nil || u.HeadwayMin != nil {
			out = append(out, u)
		}
	}
	return out
}

// usualV1 is usualAt for /v1/stops/{id}, guarded by the stop's rows with today's daily
// schedules (cached only: no OASA call), as /v2 builds its Arrivals.
func (a *App) usualV1(w *world, i int32, lines []string, now time.Time) []model.Usual {
	out := a.usualAt(w, i, now, func() map[[2]string][]int64 {
		near := map[[2]string][]int64{}
		for _, row := range a.stopRows(w, i, lines, now, v2Horizon,
			func(l string) (telematics.DailySchedule, bool) { return a.dailyCached(l, gtfs.Midnight(now)) }) {
			k := [2]string{row.Line, row.Headsign}
			near[k] = append(near[k], row.At)
		}
		return near
	})
	if out == nil {
		return []model.Usual{}
	}
	return out
}

func guarded(ats []int64, t int64) bool {
	for _, at := range ats {
		if at >= t-usualGuardS && at <= t+usualGuardS {
			return true
		}
	}
	return false
}
