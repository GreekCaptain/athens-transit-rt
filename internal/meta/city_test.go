package meta

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/angelospk/athens-transit-rt/internal/telematics"
)

// oasthAPI: Thessaloniki-like answers (Latin line ids, a StopID key plus an arrival code).
type oasthAPI struct{}

func (oasthAPI) Lines(context.Context) ([]telematics.Line, error) {
	return []telematics.Line{{LineCode: "1", LineID: "39S"}, {LineCode: "2", LineID: "01A"}}, nil
}
func (oasthAPI) Routes(_ context.Context, lc string) ([]telematics.Route, error) {
	return []telematics.Route{{RouteCode: "r" + lc, LineCode: lc}}, nil
}
func (oasthAPI) Stops(context.Context, string) ([]telematics.RouteStop, error) {
	return []telematics.RouteStop{{StopCode: "11048", ArrivalCode: "4483"}, {StopCode: "11046", ArrivalCode: "4579"},
		{StopCode: "9", ArrivalCode: "9"}}, nil
}

func TestArrivalCodesArePersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")
	q := &queue{}
	s := Open(path, oasthAPI{}, q.submit, nil)
	if got := s.ArrivalCode("11048"); got != "11048" {
		t.Fatalf("unknown stop: %q, want the code itself", got)
	}
	s.Stops("r1")
	q.drain()
	if st, ok := s.Stops("r1"); !ok || len(st) != 3 || st[0] != "11048" {
		t.Fatalf("stops %v %v: keyed by the GTFS id", st, ok)
	}
	if got := s.ArrivalCode("11048"); got != "4483" {
		t.Fatalf("arrival code %q, want 4483", got)
	}
	// A reopened store (a restart) still knows it without asking OASTH again.
	s2 := Open(path, oasthAPI{}, (&queue{}).submit, nil)
	if got := s2.ArrivalCode("11046"); got != "4579" {
		t.Fatalf("after reload: %q, want 4579", got)
	}
	if got := s2.ArrivalCode("9"); got != "9" {
		t.Fatalf("same code: %q", got)
	}
}

func TestLineAliasRenamesTelematicsLines(t *testing.T) {
	q := &queue{}
	s := Open(filepath.Join(t.TempDir(), "meta.json"), oasthAPI{}, q.submit, nil)
	s.Refresh()
	q.drain()
	s.SetLineAlias(func(id string) string {
		if id == "39S" {
			return "39Σ" // the GTFS route_short_name
		}
		return id
	})
	if r := s.LineRoutes()["39Σ"]; len(r) != 1 || r[0].Code != "r1" {
		t.Fatalf("LineRoutes[39Σ] = %v", s.LineRoutes())
	}
	if _, ok := s.LineRoutes()["39S"]; ok {
		t.Fatal("the telematics spelling is still a key")
	}
	if lc := s.LineCodes("39Σ"); len(lc) != 1 || lc[0] != "1" {
		t.Fatalf("LineCodes(39Σ) = %v", lc)
	}
	ids := map[string]bool{}
	for _, id := range s.LineIDs() {
		ids[id] = true
	}
	if !ids["39Σ"] || !ids["01A"] || ids["39S"] {
		t.Fatalf("LineIDs = %v", s.LineIDs())
	}
}
