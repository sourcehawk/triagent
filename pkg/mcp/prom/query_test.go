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

func TestQuery_EmptyVectorReturnsEmptySamples(t *testing.T) {
	t.Parallel()
	// Prom returns {"resultType":"vector","result":[]} when a query
	// matches no series. runInstantQuery returns a successful
	// QueryResult with Samples == [] (non-nil empty slice) so the
	// JSON shape carries an explicit empty array, not a missing key.
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
	require.NoError(t, err)
	require.Equal(t, "vector", res.ResultType)
	require.NotNil(t, res.Samples, "Samples must be non-nil to marshal as [] rather than null")
	require.Empty(t, res.Samples)
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

// When a label value the user supplied is absent from the cached
// labelsCache sample, that's the most likely reason the selector
// returned zero rows. Calling it out gives the agent something to
// correct rather than another round of guesses.
func TestRecentValue_NoMatch_IdentifiesAbsentValueFromSample(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			// Empty vector → "no data" path.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "vector", "result": []map[string]any{}},
			})
		},
	})
	cat := cardCatalog("zeebe_broker_health_nodes", 5)
	// labelsCache pre-populated with the known sample. "namespace" has
	// two observed values; the user's selector uses a third that is
	// absent from the sample → that's the suspect.
	cat.labelsCache["zeebe_broker_health_nodes"] = labelProfile{
		labels: []labelInfo{
			{Key: "namespace", Cardinality: 2, SampleValues: []string{"ns-a", "ns-b"}},
			{Key: "pod", Cardinality: 1, SampleValues: []string{"zeebe-0"}},
		},
		totalCardinality: 2,
	}
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "zeebe_broker_health_nodes", map[string]string{
		"namespace": "ns-missing",
		"pod":       "zeebe-0",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace")
	assert.Contains(t, err.Error(), "ns-missing")
	assert.Contains(t, err.Error(), "known values")
}

// When the sample is truncated, an absent value isn't proof of
// nonexistence — surface the suspicion as such instead of asserting
// the value doesn't exist.
func TestRecentValue_NoMatch_TruncatedSampleSoftensClaim(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "vector", "result": []map[string]any{}},
			})
		},
	})
	cat := cardCatalog("huge", 5)
	cat.labelsCache["huge"] = labelProfile{
		labels: []labelInfo{
			{Key: "namespace", Cardinality: 2, SampleValues: []string{"ns-a", "ns-b"}},
		},
		totalCardinality: cardProbeLimit,
		truncated:        true,
	}
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "huge", map[string]string{"namespace": "ns-missing"})
	require.Error(t, err)
	// Softer phrasing when sample is truncated.
	assert.Contains(t, err.Error(), "namespace")
	assert.Contains(t, err.Error(), "ns-missing")
	assert.Contains(t, err.Error(), "not in the 200-series sample")
}

// When all user-supplied values appear in the sample but the
// combination still matches nothing, surface that the combination
// (not any single label) is the issue.
func TestRecentValue_NoMatch_AllValuesPresentSurfacesCombination(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "vector", "result": []map[string]any{}},
			})
		},
	})
	cat := cardCatalog("foo", 5)
	cat.labelsCache["foo"] = labelProfile{
		labels: []labelInfo{
			{Key: "namespace", Cardinality: 2, SampleValues: []string{"ns-a", "ns-b"}},
			{Key: "pod", Cardinality: 2, SampleValues: []string{"zeebe-0", "zeebe-1"}},
		},
		totalCardinality: 2,
	}
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "foo", map[string]string{
		"namespace": "ns-a",
		"pod":       "zeebe-1",
	})
	require.Error(t, err)
	// User's individual values exist; the combination is what fails.
	assert.Contains(t, err.Error(), "combination")
}

// When no labelsCache entry exists yet, fall back to the bare "no data"
// message rather than make claims we can't substantiate.
func TestRecentValue_NoMatch_NoCacheFallback(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "vector", "result": []map[string]any{}},
			})
		},
	})
	cat := cardCatalog("foo", 5) // no labelsCache entry
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "foo", map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no data")
	assert.NotContains(t, err.Error(), "known values")
}

// On multi-match, look at the labels that vary across matched series
// and surface them as narrowing candidates — the agent doesn't have to
// re-fetch describe to guess.
func TestRecentValue_MultiMatch_SurfacesVaryingLabels(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "zeebe-0", "partition": "1", "namespace": "x"}, "value": []any{1700000000, "0.1"}},
						{"metric": map[string]string{"pod": "zeebe-0", "partition": "2", "namespace": "x"}, "value": []any{1700000000, "0.2"}},
						{"metric": map[string]string{"pod": "zeebe-0", "partition": "3", "namespace": "x"}, "value": []any{1700000000, "0.3"}},
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
	assert.Contains(t, err.Error(), "3")
	assert.Contains(t, err.Error(), "partition", "label that varies across matches must be named")
	// pod and namespace are constant across the 3 matches — they wouldn't help narrow.
	assert.NotContains(t, err.Error(), "narrow on pod")
	assert.NotContains(t, err.Error(), "narrow on namespace")
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
	// 15m (900s) with maxPoints=100: ceiling-divide on (100-1)=99 →
	// step = ceil(900/99) = ceil(9.09) = 10s, giving at most 91 samples.
	assert.Equal(t, "10s", res.Step)
	assert.Equal(t, "10s", gotStep)
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

func TestQueryRange_StepRespectsSampleBudget(t *testing.T) {
	t.Parallel()
	// 15m duration with maxPoints=100: step must be >= 10s so the
	// inclusive-endpoint sample count (floor(900/step) + 1) stays <= 100.
	var seenStep string
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			seenStep = r.URL.Query().Get("step")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{"metric": map[string]string{"x": "1", "namespace": "x"}, "values": []any{[]any{1700000000, "0"}}},
					},
				},
			})
		},
	})
	cat := cardCatalog("m", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRangeQuery(context.Background(), snap, `m{namespace="x"}`, "15m", "", 10, 100, false)
	require.NoError(t, err)
	// step >= 10s satisfies the inclusive-endpoint budget.
	require.Equal(t, "10s", seenStep)
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

func TestQueryRange_MaxPointsOne(t *testing.T) {
	t.Parallel()
	var seenStep string
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			seenStep = r.URL.Query().Get("step")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result":     []map[string]any{{"metric": map[string]string{"x": "1", "namespace": "x"}, "values": []any{[]any{1700000900, "42"}}}},
				},
			})
		},
	})
	cat := cardCatalog("m", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runRangeQuery(context.Background(), snap, `m{namespace="x"}`, "15m", "", 10, 1, true)
	require.NoError(t, err)
	// step must exceed the 900s duration so Prom returns one sample.
	require.Equal(t, "901s", seenStep)
	require.Len(t, res.Series, 1)
	require.Len(t, res.Series[0].Points, 1)
}

func TestQueryRange_ClampsMaxPointsToCeiling(t *testing.T) {
	t.Parallel()
	var seenStep string
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			seenStep = r.URL.Query().Get("step")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result":     []map[string]any{{"metric": map[string]string{"x": "1", "namespace": "x"}, "values": []any{[]any{1700000000, "0"}}}},
				},
			})
		},
	})
	cat := cardCatalog("m", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	// Caller asks for max_points=100000, but the clamp should produce
	// a step matching maxPoints=rangeHardPointsCap (=200).
	_, err := runRangeQuery(context.Background(), snap, `m{namespace="x"}`, "1h", "", 10, 100000, false)
	require.NoError(t, err)
	// 3600s / (200-1) = ceiling division: (3600 + 199 - 2) / 199 = 3797/199 = 19s (floor).
	require.Equal(t, "19s", seenStep)
}

// Regression: NaN and ±Inf are valid Prometheus sample values, but
// encoding/json returns an UnsupportedValueError for them. Without an
// explicit filter the whole MCP tool response would fail to serialize.
// runInstantQuery surfaces a clear error for scalar NaN and skips
// non-finite rows from a vector result so the JSON shape stays valid.
func TestQuery_ScalarNonFiniteReturnsError(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data":   map[string]any{"resultType": "scalar", "result": []any{1700000000, "NaN"}},
			})
		},
	})
	cat := cardCatalog("up", 12)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runInstantQuery(context.Background(), snap, "up", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-finite")
}

func TestQuery_VectorDropsNonFiniteRows(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a"}, "value": []any{1700000000, "0.5"}},
						{"metric": map[string]string{"pod": "b"}, "value": []any{1700000000, "NaN"}},
						{"metric": map[string]string{"pod": "c"}, "value": []any{1700000000, "+Inf"}},
						{"metric": map[string]string{"pod": "d"}, "value": []any{1700000000, "1.5"}},
					},
				},
			})
		},
	})
	cat := cardCatalog("ratio", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	res, err := runInstantQuery(context.Background(), snap, "ratio", "")
	require.NoError(t, err)
	require.Len(t, res.Samples, 2, "non-finite rows must be dropped before JSON encoding")
	// Output must round-trip through encoding/json without UnsupportedValueError.
	_, marshalErr := json.Marshal(res)
	require.NoError(t, marshalErr)
}

// Regression: label keys are concatenated raw into PromQL — values
// are quote-escaped but keys live between commas and `=`. An attacker-
// controlled key that breaks the matcher syntax would otherwise
// inject arbitrary PromQL and bypass the catalog scope guard.
func TestRecentValue_RejectsInvalidLabelKey(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("foo", 5)
	snap := &snapshot{client: newPromClient("http://stub", "", "", http.DefaultClient), catalog: cat}
	cases := []map[string]string{
		{`pod"} or vector(1) {dummy`: "x"},
		{"123invalid": "x"},      // can't start with a digit
		{"with-dash": "x"},       // dash isn't in [a-zA-Z0-9_]
		{"with space": "x"},      // space isn't allowed
		{"":            "x"},     // empty key
	}
	for _, labels := range cases {
		_, err := runRecentValue(context.Background(), snap, "foo", labels)
		require.Error(t, err, "labels=%v should reject", labels)
		assert.Contains(t, err.Error(), "invalid label name")
	}
}

func TestRecentValue_NonFiniteReturnsError(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		query: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "vector",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a"}, "value": []any{1700000000, "+Inf"}},
					},
				},
			})
		},
	})
	cat := cardCatalog("ratio", 5)
	snap := &snapshot{client: newPromClient(stub.URL, "", "", http.DefaultClient), catalog: cat}
	_, err := runRecentValue(context.Background(), snap, "ratio", map[string]string{"pod": "a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-finite")
}

func TestQueryRange_RawDropsNonFinitePoints(t *testing.T) {
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
							[]any{1700000060, "NaN"},
							[]any{1700000120, "-Inf"},
							[]any{1700000180, "0.2"},
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
	require.Len(t, res.Series[0].Points, 2, "non-finite points must be skipped")
	_, marshalErr := json.Marshal(res)
	require.NoError(t, marshalErr)
}

func TestQueryRange_SummarySkipsNonFiniteSamples(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		queryRange: func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "matrix",
					"result": []map[string]any{
						{"metric": map[string]string{"pod": "a", "namespace": "x"}, "values": []any{
							[]any{1700000000, "1"},
							[]any{1700000060, "NaN"},
							[]any{1700000120, "+Inf"},
							[]any{1700000180, "3"},
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
	require.NotNil(t, res.Series[0].Stats)
	// Stats must reflect only the finite samples {1, 3} → mean 2.
	assert.InDelta(t, 2.0, res.Series[0].Stats.Mean, 0.001)
	_, marshalErr := json.Marshal(res)
	require.NoError(t, marshalErr)
}
