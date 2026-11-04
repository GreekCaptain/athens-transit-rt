package telematics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/angelospk/athens-transit-rt/internal/gtfs"
)

// Auth prepares each request of a Client for an API that wants a session (OASTH).
type Auth interface {
	// Apply adds the session and headers to req, starting a session first when needed.
	Apply(ctx context.Context, req *http.Request) error
	// Reject tells Auth the API answered 401, so the session is gone and the next Apply starts a new one.
	Reject()
}

// OASTHSessionTTL is how long a session is reused. PHPSESSID lives one hour; renewing a little
// earlier keeps a call from landing on an expired one.
const OASTHSessionTTL = 50 * time.Minute

// OASTHRefusedWait: after two sessions in a row were refused (401), a new one starts at most
// this often. A wrong salt fails every call; a new session per call would double the load.
const OASTHRefusedWait = time.Minute

// OASTHToken is the X-CSRF-Token of a day: the hex sha256 of the salt and the Athens date.
func OASTHToken(salt string, t time.Time) string {
	h := sha256.Sum256([]byte(salt + t.In(gtfs.Athens).Format("20060102")))
	return hex.EncodeToString(h[:])
}

// OASTHAuth is the session and header scheme of telematics.oasth.gr/api. The salt is a secret:
// it is never logged.
type OASTHAuth struct {
	BaseURL, Salt string
	HTTP          *http.Client // the client that sends the requests, so a proxy applies to the session call too
	Now           func() time.Time

	mu      sync.Mutex
	cookie  string
	since   time.Time
	refused int // sessions refused (401) in a row
}

func NewOASTHAuth(baseURL, salt string, hc *http.Client) *OASTHAuth {
	return &OASTHAuth{BaseURL: baseURL, Salt: salt, HTTP: hc, Now: time.Now}
}

func (a *OASTHAuth) Apply(ctx context.Context, req *http.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	age := a.Now().Sub(a.since)
	if a.cookie != "" && age >= OASTHRefusedWait {
		a.refused = 0 // this session outlived the refusals
	}
	if a.cookie == "" || age >= OASTHSessionTTL {
		if a.refused >= 2 && age < OASTHRefusedWait {
			return errors.New("OASTH refused the last sessions; waiting before a new one")
		}
		if err := a.startLocked(ctx); err != nil {
			return err
		}
	}
	req.Header.Set("Cookie", "PHPSESSID="+a.cookie)
	req.Header.Set("X-CSRF-Token", OASTHToken(a.Salt, a.Now()))
	return a.page(req)
}

// page makes req look like a call from the OASTH web page: without Origin and Referer the API
// refuses every act, the session one included.
func (a *OASTHAuth) page(req *http.Request) error {
	u, err := url.Parse(a.BaseURL)
	if err != nil {
		return err
	}
	origin := u.Scheme + "://" + u.Host
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	return nil
}

// startLocked opens a session: any act answers with a PHPSESSID cookie, webGetLangs is the lightest.
func (a *OASTHAuth) startLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+"?act=webGetLangs", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	if err := a.page(req); err != nil {
		return err
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("OASTH session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OASTH session: HTTP %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "PHPSESSID" && c.Value != "" {
			a.cookie, a.since = c.Value, a.Now()
			return nil
		}
	}
	return errors.New("OASTH: no session cookie")
}

func (a *OASTHAuth) Reject() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cookie != "" {
		a.refused++
	}
	a.cookie = ""
}
