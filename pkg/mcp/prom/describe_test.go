package prom

import (
	"context"
	"encoding/json"
	"net/http"
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
