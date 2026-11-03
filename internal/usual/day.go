// Package usual learns from the fix history (internal/history) when buses usually pass a stop:
// per stop, line and headsign, the usual gap between buses at this hour of this kind of day,
// and, when the past weeks agree, the usual time of the next pass
// (docs/superpowers/specs/2026-11-03-usual-headway.md, athens-transit-map#24).
//
// A nightly build turns each finished service day of history into one immutable day file of
// stop passes (BuildDay, WriteDay). The Store keeps only the files' indexes in memory and reads
// one stop's passes per request (Store.Query).
package usual

import (
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
)

// DayStartH is the local hour a service day starts at: a pass at 01:30 belongs to the evening
// before.
const DayStartH = 4

// Day types.
const (
	Weekday  = "weekday"
	Saturday = "saturday"
	Sunday   = "sunday" // also public holidays: OASA runs the Sunday timetable
)

// ServiceDate is the service day of t: its local calendar date, or the day before before
// DayStartH. The result is midnight UTC of that date (a date, not an instant).
func ServiceDate(t time.Time) time.Time {
	lt := t.In(gtfs.Athens)
	d := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.UTC)
	if lt.Hour() < DayStartH {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// wallClock is the local instant of `secs` wall-clock seconds after midnight of date
// (secs may exceed 24 h). DST safe: 08:00 is 08:00 on either side of a change.
func wallClock(date time.Time, secs int) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, secs, 0, gtfs.Athens)
}

// wallSecs is the wall-clock seconds of t after midnight of its service date (≥ DayStartH h).
func wallSecs(t time.Time) int {
	lt := t.In(gtfs.Athens)
	s := lt.Hour()*3600 + lt.Minute()*60 + lt.Second()
	if lt.Hour() < DayStartH {
		s += 24 * 3600
	}
	return s
}

// dayBounds is [start, end) of a service date in Unix seconds.
func dayBounds(date time.Time) (int64, int64) {
	return wallClock(date, DayStartH*3600).Unix(), wallClock(date, (24+DayStartH)*3600).Unix()
}

// DayEnd is the end of a service date (DayStartH of the next calendar day).
func DayEnd(date time.Time) time.Time { return wallClock(date, (24+DayStartH)*3600) }

// DayType is the day type of a service date: Sunday also on Greek public holidays.
func DayType(date time.Time) string {
	if holiday(date) {
		return Sunday
	}
	switch date.Weekday() {
	case time.Saturday:
		return Saturday
	case time.Sunday:
		return Sunday
	}
	return Weekday
}

// holiday reports the Greek public holidays: the fixed ones and those that follow Orthodox
// Easter (Clean Monday, Easter Monday, Whit Monday). Good Friday and Holy Saturday run special
// timetables and are left as they are.
func holiday(date time.Time) bool {
	switch [2]int{int(date.Month()), date.Day()} {
	case [2]int{1, 1}, [2]int{1, 6}, [2]int{3, 25}, [2]int{5, 1}, [2]int{8, 15}, [2]int{10, 28},
		[2]int{12, 25}, [2]int{12, 26}:
		return true
	}
	e := orthodoxEaster(date.Year())
	for _, off := range []int{-48, 1, 50} {
		if e.AddDate(0, 0, off).Equal(date) {
			return true
		}
	}
	return false
}

// orthodoxEaster is the Gregorian date of Orthodox Easter (Meeus' Julian algorithm, + 13 days:
// valid 1900-2099), midnight UTC.
func orthodoxEaster(y int) time.Time {
	a, b, c := y%4, y%7, y%19
	d := (19*c + 15) % 30
	e := (2*a + 4*b - d + 34) % 7
	month := (d + e + 114) / 31
	day := (d+e+114)%31 + 1
	return time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 13)
}
