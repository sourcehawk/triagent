package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribe_PopulatesLabelsAndRelated(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			rows := []map[string]string{
				{"__name__": "zeebe_partition_health", "namespace": "zeebe-prod", "pod": "zeebe-0", "partition": "1"},
				{"__name__": "zeebe_partition_health", "namespace": "zeebe-prod", "pod": "zeebe-1", "partition": "2"},
				{"__name__": "zeebe_partition_health", "namespace": "zeebe-stg", "pod": "zeebe-0", "partition": "1"},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"zeebe_partition_health", "zeebe_partition_role", "zeebe_partition_term"}
	cat.metadata = map[string]MetricMetadata{
		"zeebe_partition_health": {Type: "gauge", Help: "Partition health"},
	}
	res, err := describeMetric(context.Background(), c, cat, "zeebe_partition_health")
	require.NoError(t, err)
	assert.Equal(t, "zeebe_partition_health", res.Name)
	assert.Equal(t, "gauge", res.Type)
	assert.Equal(t, "Partition health", res.Help)
	require.NotEmpty(t, res.Labels)
	keys := map[string]bool{}
	for _, l := range res.Labels {
		keys[l.Key] = true
	}
	assert.True(t, keys["namespace"])
	assert.True(t, keys["pod"])
	assert.Contains(t, res.Related, "zeebe_partition_role")
}

func TestDescribe_UnknownMetric(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.names = []string{"foo"}
	_, err := describeMetric(context.Background(), nil, cat, "bar")
	require.Error(t, err)
}

// When the probe hits cardProbeLimit, the per-label cardinality and
// sample_values are derived from a truncated sample and must not be
// trusted as the full picture. DescribeResult signals this via a
// top-level Truncated flag so the agent can avoid treating
// `cardinality: 1` on a label as proof of uniqueness.
func TestDescribe_TruncatedWhenAtProbeLimit(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			rows := make([]map[string]string, 0, cardProbeLimit)
			for i := 0; i < cardProbeLimit; i++ {
				rows = append(rows, map[string]string{"__name__": "huge", "instance": itoa(i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"huge"}
	cat.metadata = map[string]MetricMetadata{"huge": {Type: "gauge"}}
	res, err := describeMetric(context.Background(), c, cat, "huge")
	require.NoError(t, err)
	assert.True(t, res.Truncated, "probe at limit must set Truncated=true")
	assert.Equal(t, cardProbeLimit, res.CardinalityTotal)
}

func TestDescribe_NotTruncatedBelowProbeLimit(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			rows := []map[string]string{
				{"__name__": "small", "instance": "a"},
				{"__name__": "small", "instance": "b"},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"small"}
	cat.metadata = map[string]MetricMetadata{"small": {Type: "gauge"}}
	res, err := describeMetric(context.Background(), c, cat, "small")
	require.NoError(t, err)
	assert.False(t, res.Truncated, "probe below limit must leave Truncated=false")
}

func TestDescribe_TruncatedSerializesAsTopLevelField(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			rows := make([]map[string]string, 0, cardProbeLimit)
			for i := 0; i < cardProbeLimit; i++ {
				rows = append(rows, map[string]string{"__name__": "huge", "instance": itoa(i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"huge"}
	cat.metadata = map[string]MetricMetadata{"huge": {Type: "gauge"}}
	res, err := describeMetric(context.Background(), c, cat, "huge")
	require.NoError(t, err)
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"truncated":true`)
}

func TestDescribe_CacheHitSkipsProbe(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			rows := []map[string]string{
				{"__name__": "foo", "namespace": "ns"},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"foo"}
	cat.metadata = map[string]MetricMetadata{"foo": {Type: "gauge"}}
	_, err := describeMetric(context.Background(), c, cat, "foo")
	require.NoError(t, err)
	_, err = describeMetric(context.Background(), c, cat, "foo")
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load(), "second describe call must hit the labelsCache, not re-probe")
}
