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

func TestCardinality_LowCardCounted(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			data := []map[string]string{}
			for i := 0; i < 3; i++ {
				data = append(data, map[string]string{"__name__": "up", "instance": itoa(i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"up"}
	cat.metadata = map[string]MetricMetadata{"up": {Type: "gauge"}}
	got, err := cardinalityOf(context.Background(), c, cat, "up")
	require.NoError(t, err)
	assert.Equal(t, 3, got)
}

func TestCardinality_HighCardSentinel(t *testing.T) {
	t.Parallel()
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			data := []map[string]string{}
			for i := 0; i < cardProbeLimit; i++ {
				data = append(data, map[string]string{"__name__": "x", "instance": itoa(i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"x"}
	got, err := cardinalityOf(context.Background(), c, cat, "x")
	require.NoError(t, err)
	assert.Equal(t, -1, got, "limit-reached → high-card sentinel")
}

func TestCardinality_Cached(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	stub := newStubProm(t, stubHandlers{
		series: func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []map[string]string{{"__name__": "y"}}})
		},
	})
	c := newPromClient(stub.URL, "", "", http.DefaultClient)
	cat := emptyCatalog()
	cat.names = []string{"y"}
	_, err := cardinalityOf(context.Background(), c, cat, "y")
	require.NoError(t, err)
	_, err = cardinalityOf(context.Background(), c, cat, "y")
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load(), "second call must hit cache")
}
