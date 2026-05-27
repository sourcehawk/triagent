package prom

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalog_BuildFromStub(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data":   []string{"zeebe_partition_health", "zeebe_partition_role", "go_goroutines", "up"},
			})
		},
		metadata: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"status": "success",
				"data": map[string]any{
					"zeebe_partition_health": []map[string]string{{"type": "gauge", "help": "Partition health", "unit": ""}},
					"up":                     []map[string]string{{"type": "gauge", "help": "1 if scrape ok", "unit": ""}},
				},
			})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat, err := buildCatalog(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, []string{"go_goroutines", "up", "zeebe_partition_health", "zeebe_partition_role"}, cat.names)
	assert.Equal(t, "gauge", cat.metadata["up"].Type)
	// prefix index groups by the longest "<prefix>_" segment
	assert.GreaterOrEqual(t, cat.prefixIdx["zeebe_"], 2)
}

func TestCatalog_Empty(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		labelNames: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"status": "success", "data": []string{}})
		},
		metadata: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"status": "success", "data": map[string]any{}})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat, err := buildCatalog(context.Background(), c)
	require.NoError(t, err)
	assert.Empty(t, cat.names)
}
