package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/angelospk/athens-transit-rt/internal/geo"
	"github.com/angelospk/athens-transit-rt/internal/match"
	"github.com/angelospk/athens-transit-rt/internal/sched"
	"github.com/angelospk/athens-transit-rt/internal/telematics"
)

func TestCityByID(t *testing.T) {
	ath, err := CityByID("")
	if err != nil || ath.ID != "athens" || !ath.Rail || ath.TelematicsURL != telematics.BaseURL || ath.GTFSStopIDs {
		t.Fatalf("default city %+v %v", ath, err)
	}
	if ath.TileBox != tileBox {
		t.Errorf("Athens tile box %+v, want the current %+v", ath.TileBox, tileBox)
	}
	th, err := CityByID("thessaloniki")
	if err != nil || th.Rail || !th.GTFSStopIDs || th.TelematicsURL != "https://telematics.oasth.gr/api/" {
		t.Fatalf("thessaloniki %+v %v", th, err)
	}
	in := func(b TileBox, lat, lon float64) bool {
		return lat > b.South && lat < b.North && lon > b.West && lon < b.East
	}
	if !in(th.TileBox, 40.6401, 22.9444) || !in(th.TileBox, 40.52, 22.97) || in(th.TileBox, 37.98, 23.73) {
		t.Errorf("thessaloniki tile box %+v", th.TileBox)
	}
	if _, err := CityByID("patra"); err == nil {
		t.Error("unknown city accepted")
	}
}

func TestUseCityGlobals(t *testing.T) {
	ath, _ := CityByID("athens")
	th, _ := CityByID("thessaloniki")
	defer UseCityGlobals(ath)
	before := geo.XY(40.64, 22.94)
	UseCityGlobals(th)
	if tileBox != th.TileBox {
		t.Fatalf("tile box %+v", tileBox)
	}
	after := geo.XY(40.64, 22.94)
	if after[0] >= before[0] { // cos(40.6°) < cos(38°): east-west metres shrink
		t.Fatalf("geo origin not moved: %v -> %v", before, after)
	}
	lat, lon := geo.LatLon(after)
	if d := lat - 40.64 + lon - 22.94; d > 1e-9 || d < -1e-9 {
		t.Fatalf("LatLon(XY) = %v, %v", lat, lon)
	}
}

func TestLineAlias(t *testing.T) {
	alias := lineAlias([]string{"39Σ", "03Χ", "01A", "02Μ", "79H", "Τ1"})
	for in, want := range map[string]string{
		"39S": "39Σ", "03X": "03Χ", "01A": "01A", "02M": "02Μ", "79H": "79H", "T1": "Τ1",
		"39Σ": "39Σ", " 01A ": "01A", "99": "99", "06A": "06A",
	} {
		if got := alias(in); got != want {
			t.Errorf("alias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThessalonikiApp(t *testing.T) {
	a := New(Config{City: "thessaloniki", OASTHSalt: "salt-", StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory,
		Sched: sched.DefaultConfig()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(a.adapters) != 1 {
		t.Fatalf("%d adapters, want only the bus one", len(a.adapters))
	}
	if a.client.BaseURL != "https://telematics.oasth.gr/api/" || a.client.Auth == nil || !a.client.GTFSStopIDs {
		t.Fatalf("client %q auth %v stopIDs %v", a.client.BaseURL, a.client.Auth, a.client.GTFSStopIDs)
	}
	if a.rail() {
		t.Fatal("rail on in Thessaloniki")
	}
	// Athens: unchanged.
	b := New(Config{StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory, Sched: sched.DefaultConfig()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(b.adapters) != 3 || b.client.Auth != nil || b.client.GTFSStopIDs || !b.rail() {
		t.Fatalf("athens app changed: %d adapters, auth %v", len(b.adapters), b.client.Auth)
	}
}

// Line ids from requests follow the city's spelling: Athens turns Latin look-alikes into Greek
// (A1 -> Α1); Thessaloniki uses its GTFS spelling, which mixes both (01A, 39Σ).
func TestCanonLinePerCity(t *testing.T) {
	ath := New(Config{StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory, Sched: sched.DefaultConfig()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := ath.canonLine(" a1 "); got != "Α1" {
		t.Errorf("athens canonLine(a1) = %q", got)
	}
	th := New(Config{City: "thessaloniki", OASTHSalt: "s", StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory,
		Sched: sched.DefaultConfig()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := th.canonLine("01a"); got != "01A" {
		t.Errorf("before a feed: canonLine(01a) = %q, want 01A", got)
	}
	th.setLineAlias(lineAlias([]string{"01A", "39Σ"}))
	for in, want := range map[string]string{"01a": "01A", "01Α": "01A", "39S": "39Σ", "39σ": "39Σ", "99": "99"} {
		if got := th.canonLine(in); got != want {
			t.Errorf("thessaloniki canonLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// Thessaloniki: a stop whose arrival code is not known yet is not asked; its GTFS id is another
// stop's code there.
func TestArrivalCodeUnknownInThessaloniki(t *testing.T) {
	th := New(Config{City: "thessaloniki", OASTHSalt: "s", StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory,
		Sched: sched.DefaultConfig()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := th.arrivalCode("11048"); err == nil {
		t.Fatal("unknown Thessaloniki stop asked with its GTFS id")
	}
	ath := New(Config{StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory, Sched: sched.DefaultConfig()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c, err := ath.arrivalCode("400075"); err != nil || c != "400075" {
		t.Fatalf("athens arrivalCode = %q %v, want the stop code itself", c, err)
	}
}

// OASTH has no getDailySchedule ("Nothing Requested!", HTTP 400): Thessaloniki never asks for it
// and its timetables come from the GTFS, as when OASA's daily schedule is down.
func TestThessalonikiHasNoDailySchedule(t *testing.T) {
	th := New(Config{City: "thessaloniki", OASTHSalt: "s", StateDir: t.TempDir(), RPS: 1, Matcher: match.Memory,
		Sched: sched.DefaultConfig()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	before := th.client.Requests()
	for _, f := range []func(context.Context, string) (telematics.DailySchedule, error){th.daily, th.dailyBg} {
		if _, err := f(context.Background(), "434"); err == nil {
			t.Fatal("a daily schedule in Thessaloniki")
		}
	}
	if th.client.Requests() != before {
		t.Fatal("asked OASTH for a daily schedule")
	}
}
