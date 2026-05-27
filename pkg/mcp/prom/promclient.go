package prom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// MetricMetadata is one entry from /api/v1/metadata. Prom returns a list
// per name (one per scrape target); we keep the first for simplicity.
type MetricMetadata struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

type promClient struct {
	endpoint   string
	bearer     string
	basic      string // "user:pass"
	httpClient *http.Client
}

func newPromClient(endpoint, bearer, basic string, c *http.Client) *promClient {
	return &promClient{
		endpoint:   strings.TrimRight(endpoint, "/"),
		bearer:     bearer,
		basic:      basic,
		httpClient: c,
	}
}

func (c *promClient) doJSON(ctx context.Context, path string, q url.Values, out any) error {
	u := c.endpoint + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("prom %s returned %d: %s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *promClient) applyAuth(req *http.Request) {
	switch {
	case c.bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	case c.basic != "":
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.basic)))
	}
}

// series issues GET /api/v1/series?match[]=<expr>&limit=<n>. The Prom
// response shape is {"status":"success","data":[{label:value, ...}, ...]}.
// Returns the raw label maps so callers can both count and read sample
// values without a second round-trip.
func (c *promClient) series(ctx context.Context, matchExpr string, limit int) ([]map[string]string, error) {
	q := url.Values{}
	q.Set("match[]", matchExpr)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var resp struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	if err := c.doJSON(ctx, "/api/v1/series", q, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("prom series: status=%q", resp.Status)
	}
	return resp.Data, nil
}

// labelNames returns the set of metric names indexed by Prom.
func (c *promClient) labelNames(ctx context.Context) ([]string, error) {
	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := c.doJSON(ctx, "/api/v1/label/__name__/values", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("prom label values: status=%q", resp.Status)
	}
	return resp.Data, nil
}

// metadata returns the metric metadata table.
func (c *promClient) metadata(ctx context.Context) (map[string]MetricMetadata, error) {
	var resp struct {
		Status string                      `json:"status"`
		Data   map[string][]MetricMetadata `json:"data"`
	}
	if err := c.doJSON(ctx, "/api/v1/metadata", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("prom metadata: status=%q", resp.Status)
	}
	out := make(map[string]MetricMetadata, len(resp.Data))
	for name, entries := range resp.Data {
		if len(entries) > 0 {
			out[name] = entries[0]
		}
	}
	return out, nil
}
