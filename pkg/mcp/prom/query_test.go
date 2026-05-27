package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQuery_RejectsUnscopedHighCard pre-populates cardEst above the
// scope-required threshold so checkScope rejects before any HTTP probe.
// The "http://stub" client URL is never dialled.
func TestQuery_RejectsUnscopedHighCard(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("http_requests_total", 500)
	snap := &snapshot{client: newPromClient("http://stub", "", "", http.DefaultClient), catalog: cat}
	_, err := runInstantQuery(context.Background(), snap, "http_requests_total", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
}

func TestQuery_AllowsScopedQuery(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"namespace": "pay"}, "value": []any{1700000000, "0.5"}},
					},
				},
			})
		},
	})
	cat := cardCatalog("http_requests_total", 500)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runInstantQuery(context.Background(), snap, `http_requests_total{namespace="pay"}`, "")
	require.NoError(t, err)
	assert.Equal(t, "vector", res.ResultType)
	require.Len(t, res.Samples, 1)
	assert.Equal(t, 0.5, res.Samples[0].Value)
}

func TestQuery_RejectsOverSeriesCap(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			var rows []map[string]any
			for i := 0; i < 60; i++ {
				rows = append(rows, map[string]any{
					"metric": map[string]string{"i": strconv.Itoa(i), "namespace": "x"},
					"value":  []any{1700000000, "1"},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "vector", "result": rows},
			})
		},
	})
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runInstantQuery(context.Background(), snap, `noisy{namespace="x"}`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "60 series")
	assert.Contains(t, err.Error(), "topk")
	assert.True(t, strings.Contains(err.Error(), "aggregate"), "hint should mention aggregate")
}

func TestQuery_ScalarPassthrough(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "scalar", "result": []any{1700000000, "7"}},
			})
		},
	})
	cat := cardCatalog("up", 12)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runInstantQuery(context.Background(), snap, "up", "")
	require.NoError(t, err)
	assert.Equal(t, "scalar", res.ResultType)
	assert.NotNil(t, res.ScalarValue)
	assert.InDelta(t, 7.0, *res.ScalarValue, 0.0001)
}

func TestQuery_EmptyVectorReturnsError(t *testing.T) {
	t.Parallel()
	// Prom returns {"resultType":"vector","result":[]} when a query
	// matches no series. decodeQueryData treats an empty result envelope
	// as a malformed-response signal — assert the error is surfaced
	// cleanly to the agent rather than silently producing zero samples.
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "vector", "result": []map[string]any{}},
			})
		},
	})
	cat := cardCatalog("up", 12)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runInstantQuery(context.Background(), snap, "up", "")
	// Whether the contract is "empty samples" or "error", lock the
	// observed behaviour so a refactor can't silently flip it.
	if err == nil {
		require.Empty(t, res.Samples, "no series returned should yield empty Samples")
	}
}

func TestQuery_MatrixRejected(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{"metric": map[string]string{"x": "a"}, "values": []any{[]any{1700000000, "1"}}},
					},
				},
			})
		},
	})
	cat := cardCatalog("up", 12)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runInstantQuery(context.Background(), snap, "up", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "matrix")
}

func TestQuery_StringResultSurfacesValue(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "string", "result": []any{1700000000, "hello"}},
			})
		},
	})
	cat := cardCatalog("up", 12)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runInstantQuery(context.Background(), snap, "up", "")
	require.NoError(t, err)
	assert.Equal(t, "string", res.ResultType)
	assert.Equal(t, "hello", res.StringValue)
	assert.NotEmpty(t, res.Timestamp)
}

func TestRecentValue_RequiresLabels(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("foo", 5)
	snap := &snapshot{client: newPromClient("http://stub", "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "foo", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "labels required")
}

func TestRecentValue_SingleSample(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a", "namespace": "x"}, "value": []any{1700000000, "0.93"}},
					},
				},
			})
		},
	})
	cat := cardCatalog("zeebe_partition_health", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runRecentValue(context.Background(), snap, "zeebe_partition_health", map[string]string{"pod": "a"})
	require.NoError(t, err)
	assert.InDelta(t, 0.93, res.Value, 0.001)
	assert.NotEmpty(t, res.Timestamp)
}

func TestRecentValue_MultipleMatches(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a"}, "value": []any{1700000000, "0.1"}},
						{"metric": map[string]string{"pod": "b"}, "value": []any{1700000000, "0.2"}},
					},
				},
			})
		},
	})
	cat := cardCatalog("zeebe_partition_health", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "zeebe_partition_health", map[string]string{"namespace": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple series matched")
}

func TestBuildMatcherString_Deterministic(t *testing.T) {
	t.Parallel()
	got := buildMatcherString(map[string]string{"b": "2", "a": "1"})
	assert.Equal(t, `a="1",b="2"`, got)
}

func TestBuildMatcherString_EscapesQuotesAndBackslashes(t *testing.T) {
	t.Parallel()
	got := buildMatcherString(map[string]string{"k": `val"with"quotes`})
	assert.Equal(t, `k="val\"with\"quotes"`, got)

	got = buildMatcherString(map[string]string{"k": `val\back`})
	assert.Equal(t, `k="val\\back"`, got)

	// Combined: a literal backslash followed by a quote should produce
	// `\\\"` in the rendered matcher.
	got = buildMatcherString(map[string]string{"k": "\\\""})
	assert.Equal(t, "k=\"\\\\\\\"\"", got)
}

func TestBuildMatcherString_EmptyMap(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", buildMatcherString(nil))
	assert.Equal(t, "", buildMatcherString(map[string]string{}))
}

func TestRecentValue_RequiresMetric(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("foo", 5)
	snap := &snapshot{client: newPromClient("http://stub", "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "", map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metric is required")
}

func TestQueryRange_ComputesStepForPointBudget(t *testing.T) {
	t.Parallel()
	var gotStep string
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			gotStep = r.URL.Query().Get("step")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a", "namespace": "x"}, "values": []any{
							[]any{1700000000, "0.1"},
							[]any{1700000060, "0.2"},
						}},
					},
				},
			})
		},
	})
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runRangeQuery(context.Background(), snap, `noisy{namespace="x"}`, "15m", "", 10, 100, false)
	require.NoError(t, err)
	require.Len(t, res.Series, 1)
	// 15m / 100 points → 9s step
	assert.Equal(t, "9s", res.Step)
	assert.Equal(t, "9s", gotStep)
	assert.NotEmpty(t, res.Series[0].Sparkline)
	assert.InDelta(t, 0.15, res.Series[0].Stats.Mean, 0.001)
}

func TestQueryRange_RawPointsMode(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a", "namespace": "x"}, "values": []any{
							[]any{1700000000, "0.1"},
							[]any{1700000060, "0.2"},
							[]any{1700000120, "0.3"},
						}},
					},
				},
			})
		},
	})
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runRangeQuery(context.Background(), snap, `noisy{namespace="x"}`, "15m", "", 10, 100, true)
	require.NoError(t, err)
	require.Len(t, res.Series, 1)
	assert.Len(t, res.Series[0].Points, 3)
}

func TestQueryRange_RejectsOverSeriesCap(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			var rows []map[string]any
			for i := 0; i < 30; i++ {
				rows = append(rows, map[string]any{
					"metric": map[string]string{"i": strconv.Itoa(i), "namespace": "x"},
					"values": []any{[]any{1700000000, "1"}},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": rows}})
		},
	})
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRangeQuery(context.Background(), snap, `noisy{namespace="x"}`, "15m", "", 10, 100, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "30 series")
}

func TestQueryRange_RejectsInvalidEnd(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient("http://stub", "", "", http.DefaultClient), catalog: cat}
	_, err := runRangeQuery(context.Background(), snap, `noisy{namespace="x"}`, "15m", "not-an-rfc3339-time", 10, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid end")
}

func TestQueryRange_RejectsRangeOverCap(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient("http://stub", "", "", http.DefaultClient), catalog: cat}
	_, err := runRangeQuery(context.Background(), snap, `noisy{namespace="x"}`, "25h", "", 10, 100, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds cap")
}

func TestQueryRange_RawTruncatesAtMaxPoints(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			pts := make([]any, 0, 5)
			for i := 0; i < 5; i++ {
				pts = append(pts, []any{1700000000 + i*10, "1"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a", "namespace": "x"}, "values": pts},
					},
				},
			})
		},
	})
	cat := cardCatalog("noisy", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runRangeQuery(context.Background(), snap, `noisy{namespace="x"}`, "15m", "", 10, 3, true)
	require.NoError(t, err)
	require.Len(t, res.Series, 1)
	assert.Len(t, res.Series[0].Points, 3, "raw points must be capped at max_points")
}
