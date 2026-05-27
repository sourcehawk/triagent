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

// QueryResponse is the shape Prom returns from /api/v1/query and
// /api/v1/query_range. ResultType is one of "vector", "matrix",
// "scalar", "string". For vectors/matrices, Result holds the per-series
// rows; for scalars/strings, Scalar holds the `[timestamp, "value"]` pair.
type QueryResponse struct {
	ResultType string         `json:"resultType"`
	Result     []SeriesResult `json:"-"` // vector/matrix samples
	Scalar     []any          `json:"-"` // scalar: [ts, "v"]
}

type SeriesResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value,omitempty"`  // instant: [ts, "v"]
	Values [][]any           `json:"values,omitempty"` // range: [[ts, "v"], ...]
}

// doQuery is the shared body of query and queryRange: issue a GET with
// the supplied url.Values, parse the {status, data} envelope, and
// dispatch through decodeQueryData. `path` is `/api/v1/query` or
// `/api/v1/query_range`.
func (c *promClient) doQuery(ctx context.Context, path string, q url.Values) (QueryResponse, error) {
	var raw struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := c.doJSON(ctx, path, q, &raw); err != nil {
		return QueryResponse{}, err
	}
	if raw.Status != "success" {
		return QueryResponse{}, fmt.Errorf("prom %s: status=%q", path, raw.Status)
	}
	return decodeQueryData(raw.Data)
}

func (c *promClient) query(ctx context.Context, expr, atTime string) (QueryResponse, error) {
	q := url.Values{}
	q.Set("query", expr)
	if atTime != "" {
		q.Set("time", atTime)
	}
	return c.doQuery(ctx, "/api/v1/query", q)
}

func (c *promClient) queryRange(ctx context.Context, expr, start, end, step string) (QueryResponse, error) {
	q := url.Values{}
	q.Set("query", expr)
	q.Set("start", start)
	q.Set("end", end)
	q.Set("step", step)
	return c.doQuery(ctx, "/api/v1/query_range", q)
}

// decodeQueryData splits the polymorphic /api/v1/query data envelope
// into the QueryResponse shape. Scalar and string land in Scalar; vector
// and matrix land in Result. Any other resultType is treated as an
// error rather than silently accepted — Prom's response shape contract
// is fixed, and a mismatch indicates either a version drift or a stub
// bug we want surfaced.
func decodeQueryData(data json.RawMessage) (QueryResponse, error) {
	var head struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return QueryResponse{}, err
	}
	if len(head.Result) == 0 {
		return QueryResponse{}, fmt.Errorf("prom response missing result field for resultType=%q", head.ResultType)
	}
	out := QueryResponse{ResultType: head.ResultType}
	switch head.ResultType {
	case "scalar", "string":
		var pair []any
		if err := json.Unmarshal(head.Result, &pair); err != nil {
			return QueryResponse{}, err
		}
		out.Scalar = pair
	case "vector", "matrix":
		var rows []SeriesResult
		if err := json.Unmarshal(head.Result, &rows); err != nil {
			return QueryResponse{}, err
		}
		out.Result = rows
	default:
		return QueryResponse{}, fmt.Errorf("prom response has unknown resultType=%q", head.ResultType)
	}
	return out, nil
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
