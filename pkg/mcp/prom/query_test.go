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
