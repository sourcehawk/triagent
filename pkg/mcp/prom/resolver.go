package prom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// currentSnapshot returns the snapshot the caller should use for this
// tool call. In static mode (no resolver configured) it just loads the
// atomic pointer. In resolver mode it POSTs to the resolver URL, swaps
// the snapshot atomically if the returned URL differs from the
// current snapshot's endpoint, and refreshes the catalog synchronously
// before returning.
//
// Returns an error only when the resolver itself is unreachable or
// returns a non-2xx. A successful resolver call with an unchanged URL
// is the fast path.
func (s *Server) currentSnapshot(ctx context.Context) (*snapshot, error) {
	if s.resolverURL == "" {
		return s.snapshot.Load(), nil
	}
	url, err := s.askResolver(ctx)
	if err != nil {
		return nil, err
	}
	cur := s.snapshot.Load()
	if cur != nil && cur.endpoint == url {
		return cur, nil
	}
	newClient := newPromClient(url, s.bearer, s.basic, s.httpClient)
	newSnap := &snapshot{endpoint: url, client: newClient, catalog: emptyCatalog()}
	s.snapshot.Store(newSnap)
	if err := s.refreshCatalog(ctx); err != nil {
		_, _ = fmt.Fprintf(stderrWriter, "prom: catalog refresh after rebind failed: %v\n", err)
	}
	return s.snapshot.Load(), nil
}

// askResolver issues the POST to the resolver URL and returns the URL
// it reports.
func (s *Server) askResolver(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.resolverURL, bytes.NewReader(nil))
	if err != nil {
		return "", fmt.Errorf("build resolver request: %w", err)
	}
	if s.launcherToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.launcherToken)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolver request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("resolver returned %d: %s", resp.StatusCode, string(body))
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode resolver response: %w", err)
	}
	if body.URL == "" {
		return "", fmt.Errorf("resolver returned empty url")
	}
	return body.URL, nil
}
