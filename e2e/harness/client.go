//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"
)

// Client is the HTTP helper bound to a launcher. It carries a cookie jar so
// the launch-token cookie set on the first authenticated request persists
// across calls, mirroring how the browser SPA authenticates. The token is
// supplied via setToken once Launch parses it from the launcher's startup
// log.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

func newClient(baseURL string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
}

// setToken records the per-launch token. Subsequent authenticated requests
// attach it as the ?token= query param on the first call, which the
// launcher converts to a cookie for the rest of the session.
func (c *Client) setToken(token string) { c.token = token }

// Get issues an authenticated GET and returns the status + body. The caller
// asserts; Get never fails the test itself so negative cases stay testable.
func (c *Client) Get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	return c.do(t, http.MethodGet, path, nil)
}

// PostJSON issues an authenticated POST with a JSON body.
func (c *Client) PostJSON(t *testing.T, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("client: marshal body: %v", err)
	}
	return c.do(t, http.MethodPost, path, raw)
}

func (c *Client) do(t *testing.T, method, path string, body []byte) (int, []byte) {
	t.Helper()
	url := c.baseURL + path
	if c.token != "" {
		sep := "?"
		if bytes.ContainsRune([]byte(path), '?') {
			sep = "&"
		}
		url += sep + "token=" + c.token
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("client: build request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("client: %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("client: read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, out
}

// Healthz fetches /healthz and returns the parsed body. It does not require
// the launch token (the probe is unauthenticated).
func (c *Client) Healthz(t *testing.T) (profile, version string) {
	t.Helper()
	status, body := c.do(t, http.MethodGet, "/healthz", nil)
	if status != http.StatusOK {
		t.Fatalf("client: /healthz status %d: %s", status, body)
	}
	var parsed struct {
		Profile string `json:"profile"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("client: decode /healthz: %v (body %s)", err, body)
	}
	return parsed.Profile, parsed.Version
}

// waitForHealthz polls GET /healthz until it returns 200 or the timeout
// elapses. Used by Launch as the readiness gate.
func waitForHealthz(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	hc := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := hc.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("/healthz not ready within %s: %w", timeout, lastErr)
}
