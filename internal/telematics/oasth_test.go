package telematics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
)

func TestOASTHTokenFollowsTheAthensDay(t *testing.T) {
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	// 23:30 in Athens on 4 Nov.
	late := time.Date(2026, 11, 4, 23, 30, 0, 0, gtfs.Athens)
	if got, want := OASTHToken("salt-", late), sum("salt-20261104"); got != want {
		t.Errorf("token %s, want %s", got, want)
	}
	// 22:30 UTC on 4 Nov is 00:30 on 5 Nov in Athens: the token is the next day's.
	utc := time.Date(2026, 11, 4, 22, 30, 0, 0, time.UTC)
	if got, want := OASTHToken("salt-", utc), sum("salt-20261105"); got != want {
		t.Errorf("token %s, want %s", got, want)
	}
}

// oasthFake answers like telematics.oasth.gr/api: webGetLangs starts a session (one cookie value
// per session), every other act needs that cookie and the day's token.
type oasthFake struct {
	mu       sync.Mutex
	sessions int    // webGetLangs calls
	valid    string // the cookie the server accepts now
	salt     string
	now      time.Time
}

func (f *oasthFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Query().Get("act") == "webGetLangs" {
		f.sessions++
		f.valid = "s" + string(rune('0'+f.sessions))
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: f.valid, Path: "/"})
		w.Write([]byte(`[{"lang":"el"}]`))
		return
	}
	c, err := r.Cookie("PHPSESSID")
	if err != nil || c.Value != f.valid || r.Header.Get("X-CSRF-Token") != OASTHToken(f.salt, f.now) ||
		r.Header.Get("Referer") == "" {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"Error, User not authorized!"}`))
		return
	}
	w.Write([]byte(`[{"VEH_NO":"1313","CS_DATE":"2026-11-04 20:25:23","CS_LAT":"40.6727","CS_LNG":"22.8957","ROUTE_CODE":"2680"}]`))
}

func TestOASTHSessionStartsRenewsAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 4, 20, 26, 0, 0, gtfs.Athens)
	fake := &oasthFake{salt: "salt-", now: now}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c := New(srv.URL+"/api/", nil)
	auth := NewOASTHAuth(srv.URL+"/api/", "salt-", c.HTTP)
	auth.Now = func() time.Time { return now }
	c.Auth = auth
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		v, err := c.BusLocations(ctx, "2680")
		if err != nil || len(v) != 1 || v[0].VehNo != "1313" {
			t.Fatalf("call %d: %v %v", i, v, err)
		}
	}
	if fake.sessions != 1 {
		t.Fatalf("sessions %d after 3 calls, want 1 (the cookie is reused)", fake.sessions)
	}

	// The cookie lives one hour: after 55 minutes a new session starts before the call.
	now = now.Add(55 * time.Minute)
	fake.now = now
	if _, err := c.BusLocations(ctx, "2680"); err != nil {
		t.Fatal(err)
	}
	if fake.sessions != 2 {
		t.Fatalf("sessions %d after 55 min, want 2", fake.sessions)
	}

	// The server drops the session early: that call fails with 401, the next one starts a new
	// session and works. One request per call: the failed one is not retried in the same slot.
	fake.valid = "gone"
	_, err := c.BusLocations(ctx, "2680")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("err %v, want HTTP 401", err)
	}
	if _, err := c.BusLocations(ctx, "2680"); err != nil {
		t.Fatalf("after 401: %v", err)
	}
	if fake.sessions != 3 {
		t.Fatalf("sessions %d, want 3", fake.sessions)
	}
}

func TestOASTHSessionFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // no cookie, no session
	}))
	defer srv.Close()
	c := New(srv.URL+"/api/", nil)
	c.Auth = NewOASTHAuth(srv.URL+"/api/", "salt-", c.HTTP)
	if _, err := c.BusLocations(context.Background(), "2680"); err == nil {
		t.Fatal("no error without a session")
	}
}

func TestGTFSStopIDsAndTrimmedLineIDs(t *testing.T) {
	srv := fakeAPI(t, map[string]string{
		"webGetLines:": `[{"LineCode":"620","LineID":" 1N","LineIDGR":" 1Ν","LineDescr":"Κ.Τ.Ε.Λ. - ΑΕΡΟΔΡΟΜΙΟ","LineDescrEng":"AIRPORT"}]`,
		"webGetStops:2680": `[{"StopCode":"4483","StopID":"11048","StopDescr":"Τ.Σ. ΕΥΚΑΡΠΙΑΣ","StopLat":"40.679442","StopLng":"22.957476","RouteStopOrder":"1"},
			{"StopCode":"4579","StopID":"11046","StopDescr":"ΓΙΑΝΝΗ ΡΙΤΣΟΥ","StopLat":"40.6817637","StopLng":"22.955218","RouteStopOrder":"2"}]`,
	})
	defer srv.Close()
	ctx := context.Background()

	c := New(srv.URL, nil)
	lines, err := c.Lines(ctx)
	if err != nil || len(lines) != 1 || lines[0].LineID != "1N" {
		t.Fatalf("lines %+v %v: LineID must be trimmed", lines, err)
	}
	// OASA (default): StopCode is the stop's key, no arrival code.
	st, err := c.Stops(ctx, "2680")
	if err != nil || st[0].StopCode != "4483" || st[0].ArrivalCode != "" {
		t.Fatalf("OASA stops %+v %v", st, err)
	}
	// OASTH: the GTFS stop_id is the StopID; getStopArrivals still wants the StopCode.
	c.GTFSStopIDs = true
	st, err = c.Stops(ctx, "2680")
	if err != nil || len(st) != 2 || st[0].StopCode != "11048" || st[0].ArrivalCode != "4483" ||
		st[1].StopCode != "11046" || st[1].ArrivalCode != "4579" || st[0].Order != 1 {
		t.Fatalf("OASTH stops %+v %v", st, err)
	}
}
