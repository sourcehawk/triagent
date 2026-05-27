package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cardCatalog(name string, card int) *catalog {
	cat := emptyCatalog()
	cat.names = []string{name}
	cat.cardEst = map[string]int{name: card}
	cat.metadata = map[string]MetricMetadata{name: {Type: "gauge"}}
	return cat
}

func TestScope_AllowsScopedHighCard(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `container_cpu_usage{namespace="payments"}`)
	require.NoError(t, err)
}

func TestScope_RejectsUnscopedHighCard(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, "container_cpu_usage")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
	assert.Contains(t, err.Error(), "container_cpu_usage")
}

func TestScope_RejectsOnlyNameMatcher(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("container_cpu_usage", 500)
	err := checkScope(context.Background(), nil, cat, `container_cpu_usage{__name__="container_cpu_usage"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
}

func TestScope_AllowsLowCardUnscoped(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("up", 12)
	err := checkScope(context.Background(), nil, cat, "up")
	require.NoError(t, err)
}

func TestScope_AllowsLabelMatcherWithRegex(t *testing.T) {
	t.Parallel()
	cat := cardCatalog("http_requests_total", 500)
	err := checkScope(context.Background(), nil, cat, `http_requests_total{job=~".*api.*"}`)
	require.NoError(t, err)
}

func TestScope_ProbesUnknownCardinality(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			rows := []map[string]string{}
			for i := 0; i < 12; i++ {
				rows = append(rows, map[string]string{"__name__": "small_metric", "instance": itoa(i)})
			}
			_ = jsonWrite(w, map[string]any{"status": "success", "data": rows})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"small_metric"}
	// cardEst is unset → checkScope must probe it.
	err := checkScope(context.Background(), c, cat, "small_metric")
	require.NoError(t, err, "low-card metric should pass unscoped")
}

// jsonWrite is a tiny helper to keep this file independent of the
// testify-only writeJSON in promclient_test.go (same package, exported).
func jsonWrite(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}
