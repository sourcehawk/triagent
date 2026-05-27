package prom

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleCatalog() *catalog {
	cat := emptyCatalog()
	cat.endpoint = "http://stub"
	cat.names = []string{
		"zeebe_partition_health",
		"zeebe_partition_role",
		"zeebe_partition_term",
		"zeebe_partition_leader",
		"zeebe_broker_health",
		"zeebe_broker_role",
		"zeebe_exporter_lag",
		"zeebe_exporter_errors",
		"node_load1",
		"node_load5",
		"node_cpu_seconds_total",
	}
	cat.metadata = map[string]MetricMetadata{
		"zeebe_partition_health": {Type: "gauge", Help: "Partition health (0 healthy, 1 unhealthy, 2 dead)"},
		"zeebe_broker_health":    {Type: "gauge", Help: "Broker overall health"},
		"node_load1":             {Type: "gauge", Help: "1m load average"},
	}
	cat.prefixIdx = buildPrefixIndex(cat.names)
	return cat
}

func TestSearch_ExactNameTokenMatch(t *testing.T) {
	t.Parallel()
	r := searchMetrics(sampleCatalog(), "partition health", 30)
	require.NotNil(t, r.Matches)
	require.GreaterOrEqual(t, len(r.Matches), 1)
	assert.Equal(t, "zeebe_partition_health", r.Matches[0].Name)
}

func TestSearch_TokenANDOnName(t *testing.T) {
	t.Parallel()
	r := searchMetrics(sampleCatalog(), "zeebe partition", 30)
	// 4 partition_* metrics share both tokens; broker and exporter share only one.
	require.NotNil(t, r.Matches)
	for _, m := range r.Matches {
		assert.True(t, strings.Contains(m.Name, "zeebe"), "%s missing zeebe", m.Name)
	}
	gotPartition := 0
	for _, m := range r.Matches {
		if strings.Contains(m.Name, "partition") {
			gotPartition++
		}
	}
	assert.Equal(t, 4, gotPartition)
}

func TestSearch_RequiresQuery(t *testing.T) {
	t.Parallel()
	r := searchMetrics(sampleCatalog(), "", 30)
	assert.Equal(t, "query is required (non-empty)", r.Error)
}

func TestSearch_RejectsWildcard(t *testing.T) {
	t.Parallel()
	r := searchMetrics(sampleCatalog(), "zeebe*", 30)
	assert.Contains(t, r.Error, "no wildcard")
}

func TestSearch_FacetFallbackOnOverflow(t *testing.T) {
	t.Parallel()
	r := searchMetrics(sampleCatalog(), "zeebe", 3)
	assert.Nil(t, r.Matches)
	require.NotNil(t, r.Overflow)
	assert.Equal(t, 8, r.Overflow.Total)
	require.GreaterOrEqual(t, len(r.Overflow.Facets), 2)
	// Top facets should sort by count descending
	for i := 1; i < len(r.Overflow.Facets); i++ {
		assert.GreaterOrEqual(t, r.Overflow.Facets[i-1].Count, r.Overflow.Facets[i].Count)
	}
}

func TestSearch_HardCapAt50(t *testing.T) {
	t.Parallel()
	cat := emptyCatalog()
	cat.endpoint = "x"
	for i := 0; i < 80; i++ {
		cat.names = append(cat.names, "match_metric_"+itoa(i))
	}
	cat.prefixIdx = buildPrefixIndex(cat.names)
	r := searchMetrics(cat, "match", 100) // ask for more than hard cap
	// Hard cap is 50; 80 matches → overflow facet fallback.
	assert.Nil(t, r.Matches)
	require.NotNil(t, r.Overflow)
	assert.Equal(t, 80, r.Overflow.Total)
}

// itoa avoids importing strconv just for tests; keeps the test file
// self-contained.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
