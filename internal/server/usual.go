package server

import (
	"context"
	"path/filepath"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/model"
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
// feed.
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
		tried[d] = feed
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
			a.log.Warn("usual passes", "day", d.Format(time.DateOnly), "err", err)
			continue
		}
		if day != nil {
			a.log.Info("usual passes built", "day", d.Format(time.DateOnly), "passes", len(day.Passes),
				"stops", len(day.Stops), "outages", len(day.Outages), "took", time.Since(start).Round(time.Millisecond))
		}
	}
}

// usualRower is an Adapter whose Stops have a usual service (OASA buses only).
type usualRower interface {
	Usual(stopID string, now time.Time, arrivals []model.Arrival) []model.Usual
}

// Usual is the usual service at an OASA stop. A next time stays only when an Arrival of the
// same line and headsign (timetable or live) is within usualGuardS of it: the past weeks do
// not know that today has no such run.
func (o *oasaAdapter) Usual(stopID string, now time.Time, arrivals []model.Arrival) []model.Usual {
	st := o.a.usual.Load()
	_, i, err := o.stop(stopID)
	if st == nil || err != nil {
		return nil
	}
	w := o.a.world()
	native := w.feed.Stops[i].ID
	near := map[[2]string][]int64{}
	for _, ar := range arrivals {
		k := [2]string{ar.Line, ar.Headsign}
		near[k] = append(near[k], ar.At)
	}
	var out []model.Usual
	for _, r := range st.Query(native, now) {
		u := model.Usual{Line: oasaPrefix + r.Line, Headsign: r.Headsign, DayType: r.DayType, Days: r.Days,
			HeadwayMin: r.HeadwayMin, NextHourHeadwayMin: r.NextHourHeadwayMin, NextAt: r.NextAt, SpreadS: r.SpreadS}
		if u.NextAt != nil && !guarded(near[[2]string{u.Line, u.Headsign}], *u.NextAt) {
			u.NextAt, u.SpreadS = nil, nil
		}
		if u.NextAt != nil || u.HeadwayMin != nil {
			out = append(out, u)
		}
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
