package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProm spins up a httptest server that responds to the Prom HTTP
// API endpoints catalog tests rely on. Handlers are passed in so each
// test can shape its own responses.
type stubHandlers struct {
	labelNames func(w http.ResponseWriter, r *http.Request) // /api/v1/label/__name__/values
	metadata   func(w http.ResponseWriter, r *http.Request) // /api/v1/metadata
	series     func(w http.ResponseWriter, r *http.Request) // /api/v1/series
	query      func(w http.ResponseWriter, r *http.Request) // /api/v1/query
	queryRange func(w http.ResponseWriter, r *http.Request) // /api/v1/query_range
}

func newStubProm(t *testing.T, h stubHandlers) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	if h.labelNames != nil {
		mux.HandleFunc("/api/v1/label/__name__/values", h.labelNames)
	}
	if h.metadata != nil {
		mux.HandleFunc("/api/v1/metadata", h.metadata)
	}
	if h.series != nil {
		mux.HandleFunc("/api/v1/series", h.series)
	}
	if h.query != nil {
		mux.HandleFunc("/api/v1/query", h.query)
	}
	if h.queryRange != nil {
		mux.HandleFunc("/api/v1/query_range", h.queryRange)
	}
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

func TestPromClient_LabelNames(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data":   []string{"up", "go_goroutines", "node_load1"},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	names, err := c.labelNames(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"up", "go_goroutines", "node_load1"}, names)
}

func TestPromClient_Metadata(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		metadata: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"up": []map[string]string{{"type": "gauge", "help": "1 if scrape ok", "unit": ""}},
				},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	md, err := c.metadata(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "gauge", md["up"].Type)
	assert.Equal(t, "1 if scrape ok", md["up"].Help)
}

func TestPromClient_BearerHeader(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			seen <- r.Header.Get("Authorization")
			writeJSON(t, w, map[string]any{"status": "success", "data": []string{}})
		},
	})
	c := newPromClient(stub.URL, "tok", "", http.DefaultClient)
	_, err := c.labelNames(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok", <-seen)
}

func TestPromClient_BasicAuthHeader(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			seen <- r.Header.Get("Authorization")
			writeJSON(t, w, map[string]any{"status": "success", "data": []string{}})
		},
	})
	c := newPromClient(stub.URL, "", "user:pass", http.DefaultClient)
	_, err := c.labelNames(context.Background())
	require.NoError(t, err)
	// base64("user:pass") == "dXNlcjpwYXNz"
	assert.Equal(t, "Basic dXNlcjpwYXNz", <-seen)
}

func TestPromClient_NonSuccess(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("boom"))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.labelNames(context.Background())
	require.Error(t, err)
}

func TestPromClient_QueryInstant(t *testing.T) {
	t.Parallel()
	gotTime := make(chan string, 1)
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			gotTime <- r.URL.Query().Get("time")
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"job": "x"}, "value": []any{1700000000, "0.5"}},
					},
				},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	res, err := c.query(context.Background(), "up", "")
	require.NoError(t, err)
	assert.Equal(t, "vector", res.ResultType)
	require.Len(t, res.Result, 1)
	assert.Equal(t, "0.5", res.Result[0].Value[1])
	assert.Empty(t, <-gotTime, "time param must be omitted when atTime is empty")
}

func TestPromClient_QueryScalar(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "scalar",
					"result":     []any{1700000000, "7"},
				},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	res, err := c.query(context.Background(), "vector(7)", "")
	require.NoError(t, err)
	assert.Equal(t, "scalar", res.ResultType)
	require.Len(t, res.Scalar, 2)
	assert.Equal(t, "7", res.Scalar[1])
}

func TestPromClient_QueryUnknownResultType(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "future_shape",
					"result":     []any{},
				},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.query(context.Background(), "expr", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown resultType")
}

func TestPromClient_QueryRange(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{
							"metric": map[string]string{"pod": "a"},
							"values": []any{
								[]any{1700000000, "0.1"},
								[]any{1700000060, "0.2"},
							},
						},
					},
				},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	res, err := c.queryRange(context.Background(), "up", "1700000000", "1700001000", "60")
	require.NoError(t, err)
	assert.Equal(t, "matrix", res.ResultType)
	require.Len(t, res.Result, 1)
	require.Len(t, res.Result[0].Values, 2)
}
