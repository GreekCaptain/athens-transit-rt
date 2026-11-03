package municipal

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/overpass.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func str(s string) *string { return &s }

func TestParse(t *testing.T) {
	d, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// Only municipal routes with a shape; sorted by municipality, then ref, then name. The OASA
	// route (operator ΟΣΥ) and the route with no way are left out.
	var ids []string
	for _, r := range d.Routes {
		ids = append(ids, r.ID)
	}
	want := []string{"osm:r17502627", "osm:r20611425", "osm:r999", "osm:r20462642"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("route ids = %v, want %v", ids, want)
	}

	// Νέα Σμύρνη: no ref; only platforms, so the platforms are its stops; a nameless stop has a
	// null name.
	smyrni := d.Routes[0]
	if smyrni.Ref != nil || smyrni.Name != "Τοπική κυκλική γραμμή" || smyrni.Municipality != "Δήμος Νέας Σμύρνης" {
		t.Errorf("smyrni = %+v", smyrni)
	}
	if smyrni.TimetableURL == nil || *smyrni.TimetableURL == "" {
		t.Errorf("smyrni has no timetable url")
	}
	if !reflect.DeepEqual(smyrni.Stops, []string{"osm:n101", "osm:n102"}) {
		t.Errorf("smyrni stops = %v", smyrni.Stops)
	}
	if s := d.Stops["osm:n101"]; s.Name == nil || *s.Name != "Πλατεία" || s.Lat != 37.94 || s.Lon != 23.71 {
		t.Errorf("stop n101 = %+v", s)
	}
	if s, ok := d.Stops["osm:n102"]; !ok || s.Name != nil {
		t.Errorf("stop n102 = %+v %v", s, ok)
	}

	// Παπάγου-Χολαργού: the operator's spaces are normalised; way 2 runs backwards and joins
	// way 1, way 3 does not touch it; the platform way is not part of the shape. 5 decimals.
	pap := d.Routes[1]
	if pap.Municipality != "Δήμος Παπάγου-Χολαργού" || pap.Ref == nil || *pap.Ref != "1" || pap.TimetableURL == nil {
		t.Errorf("papagou = %+v", pap)
	}
	wantShape := [][][2]float64{
		{{37.99, 23.79}, {37.991, 23.791}, {37.992, 23.792}},
		{{37.995, 23.795}, {37.996, 23.796}},
	}
	if !reflect.DeepEqual(pap.Shape, wantShape) {
		t.Errorf("papagou shape = %v", pap.Shape)
	}
	if len(pap.Stops) != 0 || pap.Stops == nil {
		t.Errorf("papagou stops = %#v, want [] (not null)", pap.Stops)
	}

	// An operator not in the table keeps its name and has no timetable link.
	if r := d.Routes[2]; r.Municipality != "Δήμος Πουθενά" || r.TimetableURL != nil {
		t.Errorf("unknown municipality = %+v", r)
	}

	// Χαλάνδρι: stop roles win over platforms; a stop visited twice (a loop's start and end) is
	// listed twice.
	chal := d.Routes[3]
	if !reflect.DeepEqual(chal.Stops, []string{"osm:n201", "osm:n203", "osm:n201"}) {
		t.Errorf("chalandri stops = %v", chal.Stops)
	}
	if _, ok := d.Stops["osm:n202"]; ok {
		t.Errorf("platform n202 listed though the route has stops")
	}
	if len(d.Stops) != 4 {
		t.Errorf("stops = %v", d.Stops)
	}
}

func TestParseRejects(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `<html>busy</html>`,
		"no routes":     `{"elements": []}`,
		"runtime error": `{"remark": "runtime error: Query timed out in \"query\" at line 3 after 90 seconds.", "elements": [` + `{"type":"relation","id":1,"tags":{"operator":"Δήμος Χαλανδρίου","name":"x"},"members":[{"type":"way","ref":1,"role":"","geometry":[{"lat":38,"lon":23},{"lat":38.1,"lon":23.1}]}]}]}`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestEveryKnownMunicipalityHasALink(t *testing.T) {
	if len(timetables) != 12 {
		t.Errorf("timetables has %d municipalities, want the 12 of OSM", len(timetables))
	}
	for m, u := range timetables {
		if m != canonical(m) || len(u) < 12 || u[:4] != "http" {
			t.Errorf("%q → %q", m, u)
		}
	}
}

func TestPoller(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	fail := true
	p := &Poller{
		Get: func(ctx context.Context) ([]byte, error) {
			if fail {
				return nil, errors.New("down")
			}
			return fixture(t), nil
		},
		Now: func() time.Time { return now },
	}
	if d, at := p.Snapshot(); d != nil || at != 0 {
		t.Fatalf("before any poll: %v %d", d, at)
	}
	if err := p.Poll(context.Background()); err == nil {
		t.Fatal("failed poll: no error")
	}
	if d, at := p.Snapshot(); d != nil || at != 0 {
		t.Fatalf("after a failed poll: %v %d", d, at)
	}
	fail = false
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, at := p.Snapshot()
	if d == nil || len(d.Routes) != 4 || at != now.Unix() {
		t.Fatalf("after a good poll: %v %d", d, at)
	}
	// A failed poll keeps the last good data.
	fail, now = true, now.Add(time.Hour)
	_ = p.Poll(context.Background())
	if d2, at2 := p.Snapshot(); d2 != d || at2 != at {
		t.Fatalf("a failed poll changed the data: %d", at2)
	}
}
