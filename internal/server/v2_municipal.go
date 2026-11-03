package server

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/municipal"
	"github.com/angelospk/athens-transit-rt/internal/rail"
)

// The /v2/municipal layer (contract rev 18): municipal bus routes and stops from OpenStreetMap,
// polled from Overpass at start and then weekly. No OASA request.

const (
	municipalMaxAge    = 86400
	municipalBodyLimit = 16 << 20
	municipalTimeout   = 2 * time.Minute // the query may run 90 s on Overpass
)

// municipalBody is the encoded body of one poll.
type municipalBody struct {
	updated int64
	enc     *encoded
}

type municipalCache struct {
	mu   sync.Mutex
	body *municipalBody
}

// handleV2Municipal serves the layer. Before the first good poll: empty lists, a null
// updated_at, max-age=5 and no ETag.
func (a *App) handleV2Municipal(w http.ResponseWriter, r *http.Request) {
	d, updated := a.municipal.Snapshot()
	if d == nil {
		writeJSON(w, http.StatusOK, v2IncompleteAge, map[string]any{"routes": []municipal.Route{},
			"stops": map[string]municipal.Stop{}, "updated_at": nil, "attribution": municipal.Attribution})
		return
	}
	c := &a.municipalBody
	c.mu.Lock()
	b := c.body
	if b == nil || b.updated != updated {
		enc, err := encode(gzip.NewWriter(nil), map[string]any{"routes": d.Routes, "stops": d.Stops,
			"updated_at": updated, "attribution": municipal.Attribution})
		if err != nil {
			c.mu.Unlock()
			writeJSON(w, http.StatusInternalServerError, minMaxAge, map[string]string{"error": "internal"})
			return
		}
		b = &municipalBody{updated, enc}
		c.body = b
	}
	c.mu.Unlock()
	serveEncoded(w, r, b.enc, municipalMaxAge, true)
}

// overpassGet runs municipal.Query on Overpass, with the polite User-Agent.
func (a *App) overpassGet(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, municipalTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		municipal.Endpoint+"?data="+url.QueryEscape(municipal.Query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", rail.UserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overpass: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, municipalBodyLimit))
}

// newMunicipalPoller is the /v2/municipal poller of this App.
func (a *App) newMunicipalPoller() *municipal.Poller {
	return &municipal.Poller{Get: a.overpassGet, Now: func() time.Time { return a.now() }, Log: a.log}
}
