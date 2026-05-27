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
	var seen string
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("Authorization")
			writeJSON(t, w, map[string]any{"status": "success", "data": []string{}})
		},
	})
	c := newPromClient(stub.URL, "tok", "", http.DefaultClient)
	_, err := c.labelNames(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok", seen)
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
