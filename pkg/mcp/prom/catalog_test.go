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
	// prefix index groups by the first "<prefix>_" segment
	assert.Equal(t, 2, cat.prefixIdx["zeebe_"])
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

func TestBuildPrefixIndex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input []string
		want  map[string]int
	}{
		{
			name:  "happy path groups by first prefix segment",
			input: []string{"zeebe_partition_health", "zeebe_broker_role", "node_load1"},
			want:  map[string]int{"zeebe_": 2, "node_": 1},
		},
		{
			name:  "names without underscore are skipped",
			input: []string{"up", "foobar"},
			want:  map[string]int{},
		},
		{
			name:  "leading underscore is skipped (i == 0)",
			input: []string{"_internal_metric", "zeebe_x"},
			want:  map[string]int{"zeebe_": 1},
		},
		{
			name:  "single-character prefix is included",
			input: []string{"x_one", "x_two"},
			want:  map[string]int{"x_": 2},
		},
		{
			name:  "empty input yields empty map",
			input: nil,
			want:  map[string]int{},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildPrefixIndex(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
