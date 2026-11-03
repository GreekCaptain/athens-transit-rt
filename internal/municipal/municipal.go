// Package municipal reads the municipal bus routes of Attica (δημοτική συγκοινωνία) from
// OpenStreetMap through the Overpass API, for /v2/municipal (contract rev 17). No municipality
// publishes a feed; OSM (ODbL) has the route shapes for 12 municipalities and stops for most
// of them. There are no timetables: each route links to its municipality's own page.
package municipal

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoint is the public Overpass API instance.
const Endpoint = "https://overpass-api.de/api/interpreter"

// Query selects the bus and minibus route relations in the Attica box that a municipality
// runs, with their geometry, and then their member nodes with tags (the stop names).
const Query = `[out:json][timeout:90];
relation[type=route][route~"^(bus|minibus)$"](37.60,23.25,38.35,24.10)->.all;
(relation.all[operator~"Δήμος|ΔΗΜΟΣ"]; relation.all[network~"Δημοτικ|ΔΗΜΟΤΙΚ"];)->.r;
.r out geom;
node(r.r);
out body;`

// Attribution is the ODbL credit the clients show with the layer.
const Attribution = "© OpenStreetMap contributors (ODbL)"

const (
	pollEvery  = 7 * 24 * time.Hour // OSM edits to these routes are rare
	retryEvery = time.Hour          // after a failed poll
)

// timetables is the page of each municipality that lists its routes and times, by the
// canonical operator name (research in docs/research/municipal-buses.md; each link checked
// 2026-11-04. Βύρωνας has no stable page: the link is its site search for «δημοτική συγκοινωνία»).
var timetables = map[string]string{
	"Δήμος Αγίου Δημητρίου":        "https://dad.gr/environment/perivallon/dimotiki-sygkoinonia/",
	"Δήμος Αμαρουσίου":             "https://maroussi.gr/dimotes/dimotiki-sygkoinonia/",
	"Δήμος Βύρωνα":                 "https://www.dimosbyrona.gr/?s=%CE%B4%CE%B7%CE%BC%CE%BF%CF%84%CE%B9%CE%BA%CE%AE+%CF%83%CF%85%CE%B3%CE%BA%CE%BF%CE%B9%CE%BD%CF%89%CE%BD%CE%AF%CE%B1",
	"Δήμος Γαλατσίου":              "https://www.galatsi.gov.gr/dimotiki-sygkinonia/",
	"Δήμος Ελληνικού-Αργυρούπολης": "https://www.elliniko-argyroupoli.gr/dimotiki-sygkoinonia/",
	"Δήμος Ηρακλείου Αττικής":      "https://www.iraklio.gr/el/dimotiki-sygkoinonia",
	"Δήμος Λυκόβρυσης-Πεύκης":      "https://likovrisipefki.gr/images/dromologia_dimot_sygkoinwniasFINALv2.pdf",
	"Δήμος Μεταμόρφωσης":           "https://metamorfossi.gov.gr/katoikoi/metakinisi/dimotiki-sugkoinonia",
	"Δήμος Νέας Ιωνίας":            "https://neaionia.gr/dimosieuseis/perase-sti-nea-epochi-i-dimotiki-sygkoinonia-tis-neas-ionias",
	"Δήμος Νέας Σμύρνης":           "https://neasmyrni.gr/sygkoinwnies/",
	"Δήμος Παπάγου-Χολαργού":       "https://www.dpapxol.gov.gr/e-politis/2015-07-30-06-35-07/dimotiki-sigkoinwnia.html",
	"Δήμος Χαλανδρίου":             "https://www.chalandri.gr/yphresies/ypodomes-erga/dimotiki-sygkoinonia/",
}

// Route is one route relation: one direction of a line, or a loop.
type Route struct {
	ID           string         `json:"id"`  // "osm:r<relation id>"
	Ref          *string        `json:"ref"` // the line's short name; null when OSM has none
	Name         string         `json:"name"`
	Municipality string         `json:"municipality"`  // the operator, «Δήμος …»
	TimetableURL *string        `json:"timetable_url"` // the municipality's page; null when unknown
	Shape        [][][2]float64 `json:"shape"`         // lines of [lat, lon], 5 decimals
	Stops        []string       `json:"stops"`         // Stop ids in route order; [] when OSM has none
}

// Stop is a stop node of a route.
type Stop struct {
	Name *string `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// Data is the whole layer.
type Data struct {
	Routes []Route
	Stops  map[string]Stop
}

type element struct {
	Type    string            `json:"type"`
	ID      int64             `json:"id"`
	Lat     float64           `json:"lat"`
	Lon     float64           `json:"lon"`
	Tags    map[string]string `json:"tags"`
	Members []struct {
		Type     string  `json:"type"`
		Ref      int64   `json:"ref"`
		Role     string  `json:"role"`
		Lat      float64 `json:"lat"`
		Lon      float64 `json:"lon"`
		Geometry []struct {
			Lat float64 `json:"lat"`
			Lon float64 `json:"lon"`
		} `json:"geometry"`
	} `json:"members"`
}

var spaces = regexp.MustCompile(`\s+`)
var hyphen = regexp.MustCompile(`\s*-\s*`)

// canonical is an operator name with its spaces collapsed and none around a hyphen:
// «Δήμος  Παπάγου - Χολαργού» → «Δήμος Παπάγου-Χολαργού».
func canonical(op string) string {
	return hyphen.ReplaceAllString(spaces.ReplaceAllString(strings.TrimSpace(op), " "), "-")
}

func municipalRoute(tags map[string]string) bool {
	op := tags["operator"]
	return strings.HasPrefix(op, "Δήμος") || strings.HasPrefix(op, "ΔΗΜΟΣ") ||
		strings.Contains(tags["network"], "Δημοτικ") || strings.Contains(tags["network"], "ΔΗΜΟΤΙΚ")
}

func round5(v float64) float64 { return math.Round(v*1e5) / 1e5 }

// Parse reads an Overpass answer to Query. A route needs a municipality and at least one way;
// its stops are the members with a stop role, or the platforms when it has no stop role. An
// answer with no route, or with a runtime error remark (a partial answer), is an error.
func Parse(body []byte) (*Data, error) {
	var doc struct {
		Remark   string    `json:"remark"`
		Elements []element `json:"elements"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("municipal: %w", err)
	}
	if strings.Contains(doc.Remark, "error") {
		return nil, fmt.Errorf("municipal: overpass: %s", doc.Remark)
	}
	names := map[int64]string{}
	for _, e := range doc.Elements {
		if e.Type == "node" && e.Tags["name"] != "" {
			names[e.ID] = e.Tags["name"]
		}
	}
	d := &Data{Routes: []Route{}, Stops: map[string]Stop{}}
	for _, e := range doc.Elements {
		if e.Type != "relation" || !municipalRoute(e.Tags) {
			continue
		}
		r := Route{ID: "osm:r" + strconv.FormatInt(e.ID, 10), Name: e.Tags["name"], Stops: []string{}}
		if ref := e.Tags["ref"]; ref != "" {
			r.Ref = &ref
		}
		r.Municipality = canonical(e.Tags["operator"])
		if r.Municipality == "" {
			r.Municipality = canonical(e.Tags["network"])
		}
		if u, ok := timetables[r.Municipality]; ok {
			r.TimetableURL = &u
		}
		var stops, platforms []string
		stopAt := map[string]Stop{}
		for _, m := range e.Members {
			switch {
			case m.Type == "way" && !strings.HasPrefix(m.Role, "platform") && !strings.HasPrefix(m.Role, "stop"):
				line := make([][2]float64, 0, len(m.Geometry))
				for _, g := range m.Geometry {
					line = append(line, [2]float64{round5(g.Lat), round5(g.Lon)})
				}
				r.Shape = join(r.Shape, line)
			case m.Type == "node" && (strings.HasPrefix(m.Role, "stop") || strings.HasPrefix(m.Role, "platform")):
				id := "osm:n" + strconv.FormatInt(m.Ref, 10)
				s := Stop{Lat: round5(m.Lat), Lon: round5(m.Lon)}
				if n, ok := names[m.Ref]; ok {
					s.Name = &n
				}
				stopAt[id] = s
				if strings.HasPrefix(m.Role, "stop") {
					stops = appendNew(stops, id)
				} else {
					platforms = appendNew(platforms, id)
				}
			}
		}
		if len(r.Shape) == 0 {
			continue
		}
		if len(stops) == 0 {
			stops = platforms
		}
		for _, id := range stops {
			r.Stops = append(r.Stops, id)
			d.Stops[id] = stopAt[id]
		}
		d.Routes = append(d.Routes, r)
	}
	if len(d.Routes) == 0 {
		return nil, errors.New("municipal: no route in the answer")
	}
	slices.SortFunc(d.Routes, func(a, b Route) int {
		return cmp.Or(cmp.Compare(a.Municipality, b.Municipality), cmp.Compare(deref(a.Ref), deref(b.Ref)),
			cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return d, nil
}

// join adds a way's points to a shape: onto its last line when the way starts (or, reversed,
// ends) where that line ends, else as a new line. Ways of fewer than 2 points are dropped.
func join(shape [][][2]float64, way [][2]float64) [][][2]float64 {
	if len(way) < 2 {
		return shape
	}
	if n := len(shape); n > 0 {
		last := shape[n-1]
		end := last[len(last)-1]
		if way[len(way)-1] == end && way[0] != end {
			slices.Reverse(way)
		}
		if way[0] == end {
			shape[n-1] = append(last, way[1:]...)
			return shape
		}
	}
	return append(shape, way)
}

func appendNew(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Poller keeps the last good Data and refreshes it. Snapshot is safe for concurrent use; Poll
// must not run twice at once.
type Poller struct {
	Get func(ctx context.Context) ([]byte, error) // one Overpass answer to Query
	Now func() time.Time
	Log *slog.Logger // nil: slog.Default()

	mu      sync.Mutex
	data    *Data
	updated int64
}

// Snapshot is the last good Data and the Unix time of its poll; nil and 0 before the first.
func (p *Poller) Snapshot() (*Data, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data, p.updated
}

// Poll fetches and parses the routes. On a failure the last good Data stays.
func (p *Poller) Poll(ctx context.Context) error {
	if p.Get == nil {
		return errors.New("municipal: no Get")
	}
	b, err := p.Get(ctx)
	if err != nil {
		return err
	}
	d, err := Parse(b)
	if err != nil {
		return err
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	p.mu.Lock()
	p.data, p.updated = d, now.Unix()
	p.mu.Unlock()
	return nil
}

// Run polls at once, then weekly (hourly after a failure) until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	log := p.Log
	if log == nil {
		log = slog.Default()
	}
	for {
		wait := pollEvery
		if err := p.Poll(ctx); err != nil {
			log.Warn("municipal poll failed", "err", err)
			wait = retryEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
