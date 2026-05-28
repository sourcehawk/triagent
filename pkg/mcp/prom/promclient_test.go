package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Upstream HTTP failures must carry enough context to identify the
// operation that triggered them. A bare "context deadline exceeded"
// or "127.0.0.1:N/api/v1/series" leaves the agent guessing what
// failed; the camunda session showed that turns into a fabricated
// schema-bug diagnosis.
func TestPromClient_SeriesErrorWrapsOperationAndMatcher(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("upstream unavailable"))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.series(context.Background(), "zeebe_broker_health_nodes", 200)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "series probe", "operation name must appear in the error")
	assert.Contains(t, err.Error(), "zeebe_broker_health_nodes", "matcher must appear in the error")
}

func TestPromClient_QueryErrorWrapsOperationAndExpr(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.query(context.Background(), `topk(5, zeebe_broker_health_nodes)`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instant query", "operation name must appear in the error")
	assert.Contains(t, err.Error(), "zeebe_broker_health_nodes", "expression must appear in the error")
}

func TestPromClient_QueryRangeErrorWrapsOperationAndExpr(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.queryRange(context.Background(), `rate(zeebe_messages_total[5m])`, "0", "60", "10s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "range query", "operation name must appear in the error")
	assert.Contains(t, err.Error(), "zeebe_messages_total", "expression must appear in the error")
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

func TestPromClient_SeriesBoundedBody(t *testing.T) {
	t.Parallel()
	// Stub returns a deliberately oversized series response (>1 MiB).
	// The defensive cap should trip before the decode finishes.
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			// Each entry is ~130 bytes; 30000 entries ≈ 3.9 MiB — well over the 1 MiB cap.
			entry := `{"__name__":"foo","x":"` + strings.Repeat("a", 100) + `"}`
			entries := strings.Repeat(entry+",", 30000)
			entries = strings.TrimSuffix(entries, ",")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":[` + entries + `]}`))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.series(context.Background(), "foo", 200)
	require.Error(t, err, "oversized response must trip the body cap")
}

// Regression: the per-series caps in runInstantQuery / runRangeQuery
// only fire after decode. Without a body cap, a broad query could
// allocate hundreds of MiB before the cap rejects it. doQuery now
// routes through doJSONBounded with queryMaxBodyBytes.
func TestPromClient_QueryBoundedBody(t *testing.T) {
	t.Parallel()
	// Stub returns a deliberately oversized query response (>queryMaxBodyBytes).
	// Each row is ~1.2 KiB; 32000 rows ≈ 38 MiB — well past any cap sized
	// for the advertised range-query worst case.
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			row := `{"metric":{"__name__":"x","pad":"` + strings.Repeat("a", 1024) + `"},"value":[1700000000,"1"]}`
			rows := strings.Repeat(row+",", 32000)
			rows = strings.TrimSuffix(rows, ",")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[` + rows + `]}}`))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	_, err := c.query(context.Background(), "x", "")
	require.Error(t, err, "oversized query response must trip the body cap before decode finishes")
}

// Regression for the body-cap-vs-advertised-input-cap mismatch: a range
// query response that lands at the worst case the advertised input caps
// permit (rangeHardSeriesCap × rangeHardPointsCap with realistic k8s
// label volume) must NOT be rejected by the defensive body cap. The
// previous 8 MiB ceiling tripped on bodies the agent was promised it
// could request.
func TestPromClient_QueryRangeWorstCaseValidResponseFitsBodyCap(t *testing.T) {
	t.Parallel()
	// Build a 500-series × 200-point matrix with ~12 KiB of labels per
	// series (a realistic k8s pod row with annotations exposed as labels).
	// Total body size ≈ 9.3 MiB — past the old 8 MiB cap, comfortably
	// inside the new one and inside what rangeHardSeriesCap=500 ×
	// rangeHardPointsCap=200 allows.
	const series = 500
	const points = 200
	const labelPad = 12 * 1024
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[`))
			pointBuf := strings.Builder{}
			for j := 0; j < points; j++ {
				if j > 0 {
					pointBuf.WriteByte(',')
				}
				_, _ = fmt.Fprintf(&pointBuf, `[%d,"1.234567"]`, 1700000000+j*60)
			}
			pointsBlock := pointBuf.String()
			pad := strings.Repeat("a", labelPad)
			for i := 0; i < series; i++ {
				if i > 0 {
					_, _ = w.Write([]byte(","))
				}
				_, _ = fmt.Fprintf(w, `{"metric":{"__name__":"m","pod":"p%d","pad":"%s"},"values":[%s]}`, i, pad, pointsBlock)
			}
			_, _ = w.Write([]byte(`]}}`))
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	res, err := c.queryRange(context.Background(), "m", "1700000000", "1700012000", "60")
	require.NoError(t, err, "worst-case valid range response must fit the body cap")
	require.Equal(t, "matrix", res.ResultType)
	require.Len(t, res.Result, series)
}
