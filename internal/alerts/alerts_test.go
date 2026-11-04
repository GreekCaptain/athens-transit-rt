package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLineCodes(t *testing.T) {
	cases := map[string][]string{
		"Μερική προσωρινή τροποποίηση της διαδρομής  των λεωφορειακών γραμμών  305, 319 και 326,  στον Δήμο Σπάτων &#8211; Αρτέμιδος": {"305", "319", "326"},
		"Μερική προσωρινή τροποποίηση της λεωφ. γραμμής 418 λόγω έργων στην οδό Σαρανταπόρου":                                         {"418"},
		"Μερική προσωρινή τροποποίηση της λεωφορειακής γραμμής 046 στον Δήμο Αθηναίων":                                                {"046"},
		"Τροποποίηση των γραμμών 724 και 727, λόγω εργασιών στην λεωφόρο Καραμανλή μεταξύ των οδών 3 και 5":                           {"724", "727"},
		"Λειτουργία των ΜΜΜ λόγω των εορταστικών εκδηλώσεων για την Εθνική Επέτειο της 28ης Οκτωβρίου":                                nil,
		"Τροποποίηση της γραμμής Α1 και της γραμμής Χ95":                                                                              {"Α1", "Χ95"},
		"Τροποποίηση της γραμμής A1 (Latin A) και B12":                                                                                {"Α1", "Β12"},
		"Τροποποίηση των γραμμών 305,319 και 326":                                                                                     {"305", "319", "326"},
		"Τροποποίηση των γραμμών 040, 040 και 049":                                                                                    {"040", "049"},
	}
	for title, want := range cases {
		if got := LineCodes(title); !reflect.DeepEqual(got, want) {
			t.Errorf("LineCodes(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestDatesAndStops(t *testing.T) {
	cases := []struct {
		file, from, until string
		stops             []string
	}{
		{"post-112.html", "2026-10-31", "2026-11-01", []string{"040054"}}, // «στις 31.10.2026 και τη 1.11.2026», «ΚΣ 040054»
		{"post-109.html", "2026-10-31", "2026-10-31", nil},                // «Σάββατο 31.10.26»
		{"post-046.html", "2026-10-31", "2027-04-30", nil},                // «από 31/10/2026 έως 30/04/2027»
		{"post-409.html", "2026-10-28", "2026-10-28", nil},                // «Τετάρτη 28 Οκτωβρίου 2026»
	}
	for _, c := range cases {
		text := ArticleText(read(t, c.file))
		if !strings.Contains(text, "Με στόχο") {
			t.Fatalf("%s: ArticleText lost the body: %.200q", c.file, text)
		}
		if strings.Contains(text, "<") {
			t.Errorf("%s: ArticleText kept a tag", c.file)
		}
		from, until, ok := Dates(text)
		if !ok || from != c.from || until != c.until {
			t.Errorf("%s: Dates = %q %q %v, want %q %q", c.file, from, until, ok, c.from, c.until)
		}
		if got := StopCodes(text); !reflect.DeepEqual(got, c.stops) {
			t.Errorf("%s: StopCodes = %q, want %q", c.file, got, c.stops)
		}
	}
	// The page header (author, published and updated dates) is not part of the notice.
	if text := ArticleText(read(t, "post-112.html")); strings.Contains(text, "On 30 Οκτωβρίου") {
		t.Errorf("ArticleText kept the header: %.200q", text)
	}
	// A non-breaking space (&nbsp;) between day and month is a space.
	if from, _, ok := Dates(ArticleText([]byte("<article><p>Με στόχο την Τετάρτη 28&nbsp;Οκτωβρίου&nbsp;2026 ως εξής</p></article>"))); !ok || from != "2026-10-28" {
		t.Errorf("nbsp date = %q %v", from, ok)
	}
	if _, _, ok := Dates("Ενημερώνουμε το επιβατικό κοινό ότι λόγω των εκδηλώσεων της 28ης Οκτωβρίου, ως εξής"); ok {
		t.Error("a day without month and year must not be a date")
	}
	// Only the opening paragraph counts: a date after «ως εξής» is part of the route text.
	from, until, ok := Dates("Ο ΟΑΣΑ ανακοινώνει για τις 2/11/2026 ως εξής: η στάση του 2010 και 5/5/2030")
	if !ok || from != "2026-11-02" || until != "2026-11-02" {
		t.Errorf("Dates after «ως εξής» = %q %q %v", from, until, ok)
	}
}

func TestParseFeed(t *testing.T) {
	items, err := ParseFeed(read(t, "oasa-feed.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 {
		t.Fatalf("got %d items, want 10", len(items))
	}
	it := items[1]
	if it.GUID != "https://www.oasa.gr/?p=14909" || !strings.HasPrefix(it.Link, "https://www.oasa.gr/blog/") {
		t.Errorf("item 2 = %+v", it)
	}
	if !strings.Contains(it.Title, "γραμμής 112") {
		t.Errorf("title %q", it.Title)
	}
	if want := time.Date(2026, 10, 30, 4, 42, 0, 0, time.UTC); !it.Published.Truncate(time.Minute).Equal(want) {
		t.Errorf("published %v, want %v", it.Published, want)
	}
	if !reflect.DeepEqual(items[8].Categories[:2], []string{"helpdesk", "Διαγωνισμοί"}) {
		t.Errorf("categories %q", items[8].Categories)
	}
	if strings.Contains(items[5].Title, "&#8211;") {
		t.Errorf("title not unescaped: %q", items[5].Title)
	}
}

func TestFromWP(t *testing.T) {
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	items, err := FromWP(read(t, "athenstransport-posts.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d strike items, want 1: %+v", len(items), items)
	}
	it := items[0]
	want := Item{
		ID: "athenstransport:80765", Source: "athenstransport",
		Title:     "Hellenic Train: Απεργία στο σιδηρόδρομο την Παρασκευή παρά την απόφαση του δικαστηρίου",
		URL:       "https://www.athenstransport.com/2026/10/apergia-hellenic-train-30-oktobriou/",
		Published: time.Date(2026, 10, 29, 17, 20, 7, 0, time.UTC).Unix(),
		Modes:     []string{"suburban"}, Lines: []string{}, Stops: []string{}, Urgent: true,
	}
	if !reflect.DeepEqual(it, want) {
		t.Errorf("got  %+v\nwant %+v", it, want)
	}
	// Category ids → modes; a strike with no mode category concerns every mode.
	if got := ModesOf([]int{231, 224, 227, 218}); !reflect.DeepEqual(got, []string{"bus", "metro"}) {
		t.Errorf("ModesOf = %q", got)
	}
	if got := ModesOf([]int{231}); !reflect.DeepEqual(got, []string{"bus", "trolley", "metro", "tram", "suburban"}) {
		t.Errorf("ModesOf(strike only) = %q", got)
	}
}

var lines = map[string]string{"101": "bus", "109": "bus", "112": "bus", "046": "bus", "418": "bus", "409": "bus", "724": "bus", "727": "bus", "305": "bus", "319": "bus", "326": "bus", "10": "trolley"}

func knownLine(code string) (string, string, bool) {
	m, ok := lines[code]
	return "oasa:" + code, m, ok
}

// stopIDs maps a stop code to GTFS stop ids, like the 2026-07-08 OASA GTFS (code 040054 is
// stop_id 40054).
func stopIDs(code string) []string {
	if code == "040054" {
		return []string{"40054"}
	}
	return nil
}

func TestOASAItem(t *testing.T) {
	items, _ := ParseFeed(read(t, "oasa-feed.xml"))
	it, keep := OASAItem(items[1], read(t, "post-112.html"), knownLine, stopIDs)
	if !keep {
		t.Fatal("item 2 dropped")
	}
	from, until := "2026-10-31", "2026-11-01"
	want := Item{
		ID: "oasa:14909", Source: "oasa", Title: items[1].Title, URL: items[1].Link,
		Published: items[1].Published.Unix(), From: &from, Until: &until,
		Modes: []string{"bus"}, Lines: []string{"oasa:112"}, Stops: []string{"oasa:40054"},
	}
	if !reflect.DeepEqual(it, want) {
		t.Errorf("got  %+v\nwant %+v", it, want)
	}
	// A code of a temporary stop the GTFS lacks (999999) stays out, without error; a code
	// shared by two stops gives both; a Stop named twice is listed once.
	shared := func(code string) []string {
		if code == "01" {
			return []string{"10152", "230021"}
		}
		return stopIDs(code)
	}
	page := []byte(`<html><body><article><p>Νέα στάση ΚΣ 999999, κατάργηση ΚΣ 040054 και ΚΣ 01, ξανά ΚΣ 040054.</p></article></body></html>`)
	if it, _ := OASAItem(items[1], page, knownLine, shared); !reflect.DeepEqual(it.Stops, []string{"oasa:40054", "oasa:10152", "oasa:230021"}) {
		t.Errorf("stops %v", it.Stops)
	}
	if _, keep := OASAItem(items[8], nil, knownLine, stopIDs); keep {
		t.Error("a tender (Διαγωνισμοί) must be dropped")
	}
	// No article (fetch failed): kept, without dates or stops.
	it, keep = OASAItem(items[4], nil, knownLine, stopIDs)
	if !keep || it.From != nil || it.Until != nil || !reflect.DeepEqual(it.Lines, []string{"oasa:046"}) || len(it.Stops) != 0 {
		t.Errorf("no article: %+v %v", it, keep)
	}
	// Network-wide notice: no lines, both road modes.
	it, _ = OASAItem(items[7], nil, knownLine, stopIDs)
	if len(it.Lines) != 0 || !reflect.DeepEqual(it.Modes, []string{"bus", "trolley"}) {
		t.Errorf("network-wide: %+v", it)
	}
	// Unknown line codes are dropped; a trolley line adds the trolley mode.
	fi := items[1]
	fi.Title = "Τροποποίηση των γραμμών 112, 999 και 10"
	it, _ = OASAItem(fi, nil, knownLine, stopIDs)
	if !reflect.DeepEqual(it.Lines, []string{"oasa:112", "oasa:10"}) || !reflect.DeepEqual(it.Modes, []string{"bus", "trolley"}) {
		t.Errorf("mixed: %+v", it)
	}
}

func TestPoller(t *testing.T) {
	feed := read(t, "oasa-feed.xml")
	parsed, _ := ParseFeed(feed)
	pages := map[string][]byte{
		Feed:              feed,
		parsed[0].Link:    read(t, "post-109.html"),
		parsed[1].Link:    read(t, "post-112.html"),
		parsed[4].Link:    read(t, "post-046.html"),
		parsed[9].Link:    read(t, "post-409.html"),
		AthensTransportWP: read(t, "athenstransport-posts.json"),
	}
	gets := map[string]int{}
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	p := &Poller{
		Get: func(_ context.Context, url string) ([]byte, error) {
			gets[url]++
			if b, ok := pages[url]; ok {
				return b, nil
			}
			return nil, errors.New("404")
		},
		KnownLine: knownLine, StopIDs: stopIDs,
		Now: func() time.Time { return now },
	}
	if items, updated := p.Snapshot(); updated != 0 || len(items) != 0 {
		t.Fatal("not empty before the first poll")
	}
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := p.Items()
	if len(items) != 10 { // 9 OASA notices (no tender) + 1 strike
		t.Fatalf("got %d items, want 10", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i].Published > items[i-1].Published {
			t.Fatal("items not newest first")
		}
	}
	if items[0].ID != "oasa:14914" || items[0].From == nil || *items[0].From != "2026-10-31" {
		t.Errorf("first item %+v", items[0])
	}
	if p.Updated() != now.Unix() {
		t.Errorf("Updated %d", p.Updated())
	}

	// A second poll fetches only the feeds and the articles that failed, never a parsed one again.
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gets[parsed[1].Link] != 1 || gets[Feed] != 2 || gets[AthensTransportWP] != 2 {
		t.Errorf("gets after two polls: %v", gets)
	}
	if gets[parsed[2].Link] != 2 {
		t.Errorf("a failed article is retried: %d", gets[parsed[2].Link])
	}

	// 9 days later: only the notice that is still valid (046 until 2027-04-30) is left.
	now = now.Add(9 * 24 * time.Hour)
	items = p.Items()
	if len(items) != 1 || !reflect.DeepEqual(items[0].Lines, []string{"oasa:046"}) {
		t.Errorf("after 9 days: %+v", items)
	}

	// A notice that left the feed (it shows only the last 10) stays until it expires.
	now = time.Date(2026, 10, 30, 20, 30, 0, 0, time.UTC)
	short := strings.Replace(string(feed), "<item>", "<!--", 1)
	short = strings.Replace(short, "</item>", "-->", 1)
	pages[Feed] = []byte(short)
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.Items(); len(got) != 10 || got[0].ID != "oasa:14914" {
		t.Errorf("after the feed rolled over: %d items, first %+v", len(got), got[0])
	}
	pages[Feed] = feed

	// One source failing keeps the other's items and the old ones.
	now = time.Date(2026, 10, 30, 21, 0, 0, 0, time.UTC)
	delete(pages, AthensTransportWP)
	if err := p.Poll(context.Background()); err == nil {
		t.Error("a failed source must be reported")
	}
	if len(p.Items()) != 10 {
		t.Errorf("after a failed source: %d items", len(p.Items()))
	}
}

func TestItemJSON(t *testing.T) {
	b, _ := json.Marshal(Item{ID: "x", Modes: []string{}, Lines: []string{}, Stops: []string{}})
	for _, k := range []string{`"from":null`, `"until":null`, `"lines":[]`, `"urgent":false`, `"published":0`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("%s missing in %s", k, b)
		}
	}
}

// oasa.gr can serve another notice at a notice's link (X95 → 861Β, 2026-11-04): the text then
// comes from the WordPress API, and the link goes to the news list.
func TestPollerWrongNoticePage(t *testing.T) {
	item := func(id, link string) string {
		return `<item><title>Μερική προσωρινή τροποποίηση της λεωφορειακής γραμμής Χ95</title><link>` + link +
			`</link><pubDate>Wed, 04 Nov 2026 10:47:19 +0000</pubDate><guid isPermaLink="false">https://www.oasa.gr/?p=` + id + `</guid></item>`
	}
	feed := `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>` +
		item("14967", "https://www.oasa.gr/blog/x95/") + item("14970", "https://www.oasa.gr/blog/ok/") + `</channel></rss>`
	page := func(id, dates string) []byte {
		return []byte(`<html><head><link rel='shortlink' href='https://www.oasa.gr/?p=` + id + `' /></head><body class="post-template-default single postid-` +
			id + `"><article><p>Με στόχο την ενημέρωση ο ΟΑΣΑ ανακοινώνει ` + dates + ` ως εξής:</p></article></body></html>`)
	}
	pages := map[string][]byte{
		Feed:                              []byte(feed),
		"https://www.oasa.gr/blog/x95/":   page("14947", "από 04/11/2026 έως 23/11/2026"),
		"https://www.oasa.gr/blog/ok/":    page("14970", "στις 06/11/2026"),
		PostAPI + "14967?_fields=content": []byte(`{"content":{"rendered":"<p>Με στόχο την ενημέρωση ο ΟΑΣΑ ανακοινώνει την τροποποίηση στις 04/11/2026, για τις ώρες από 23:30 έως και 06:00, ως εξής:</p>"}}`),
		AthensTransportWP:                 []byte(`[]`),
	}
	gets := map[string]int{}
	p := &Poller{
		Get: func(_ context.Context, url string) ([]byte, error) {
			gets[url]++
			if b, ok := pages[url]; ok {
				return b, nil
			}
			return nil, errors.New("404")
		},
		KnownLine: knownLine,
		Now:       func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) },
	}
	for range 2 {
		if err := p.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]Item{}
	for _, it := range p.Items() {
		got[it.ID] = it
	}
	x := got["oasa:14967"]
	if x.URL != NewsList || x.From == nil || *x.From != "2026-11-04" || *x.Until != "2026-11-04" {
		t.Errorf("wrong page: url %q from %v until %v", x.URL, x.From, x.Until)
	}
	ok := got["oasa:14970"]
	if ok.URL != "https://www.oasa.gr/blog/ok/" || ok.From == nil || *ok.From != "2026-11-06" {
		t.Errorf("right page: %+v", ok)
	}
	if gets["https://www.oasa.gr/blog/x95/"] != 1 || gets[PostAPI+"14967?_fields=content"] != 1 {
		t.Errorf("each page is read once: %v", gets)
	}
}
