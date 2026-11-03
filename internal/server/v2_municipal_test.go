package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
	"github.com/angelospk/athens-transit-rt/internal/municipal"
)

type v2Municipal struct {
	Routes      []municipal.Route         `json:"routes"`
	Stops       map[string]municipal.Stop `json:"stops"`
	UpdatedAt   *int64                    `json:"updated_at"`
	Attribution string                    `json:"attribution"`
}

func TestV2Municipal(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, gtfs.Athens)
	a := newApp(t, now)

	// Before the first good poll: empty lists, a null updated_at, cached 5 s, no ETag.
	res, body := get(t, a, "/v2/municipal")
	var before v2Municipal
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatalf("%v %s", err, body)
	}
	if res.StatusCode != 200 || maxAge(t, res) != 5 || res.Header.Get("ETag") != "" ||
		before.Routes == nil || len(before.Routes) != 0 || before.Stops == nil || before.UpdatedAt != nil {
		t.Fatalf("before the first poll: %d %d %s", res.StatusCode, maxAge(t, res), body)
	}

	fx, err := os.ReadFile("../municipal/testdata/overpass.json")
	if err != nil {
		t.Fatal(err)
	}
	a.municipal.Get = func(context.Context) ([]byte, error) { return fx, nil }
	if err := a.municipal.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	res, body = get(t, a, "/v2/municipal")
	var after v2Municipal
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("%v %s", err, body)
	}
	if res.StatusCode != 200 || maxAge(t, res) != 86400 || len(after.Routes) != 4 || len(after.Stops) != 4 ||
		after.UpdatedAt == nil || *after.UpdatedAt != now.Unix() || after.Attribution != "© OpenStreetMap contributors (ODbL)" {
		t.Fatalf("after a poll: %d %d %s", res.StatusCode, maxAge(t, res), body)
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if r, _ := do(a, "GET", "/v2/municipal", map[string]string{"If-None-Match": etag}); r.StatusCode != 304 {
		t.Fatalf("If-None-Match: %d", r.StatusCode)
	}
}
