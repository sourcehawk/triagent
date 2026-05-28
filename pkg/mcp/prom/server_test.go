package prom

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


func TestNew_ReturnsServer(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestNew_BearerAndBasicAuthMutuallyExclusive(t *testing.T) {
	t.Parallel()
	_, err := New(Options{Endpoint: "http://127.0.0.1:9090", Bearer: "tok", BasicAuth: "user:pass"})
	require.Error(t, err)
}

func TestRun_ReturnsOnCanceledContext(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = srv.Run(ctx) // best-effort: SDK may return any error on cancelled-before-start; we just want no panic
}

// Probes against high-fanout upstreams (Thanos query in the camunda
// dev clusters) regularly took 8+ seconds in real sessions; a 10s
// hard ceiling on the HTTP client meant a single slow probe burned
// the entire query budget. Rely on the caller's context for the
// deadline instead.
// The error return on handleQuery (and friends) used to leave the
// typed result's slice fields nil. encoding/json renders a nil slice
// as `null`, which is the exact shape the agent twice fabricated as
// a "schema validation: samples is null" bug. We can't stop the
// agent from inventing diagnoses, but we can make sure the wire
// payload it sees never carries `null` for a slice-typed field.
func TestHandleQuery_ErrorPathEmitsNonNullSamples(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://stub"})
	require.NoError(t, err)
	// No catalog, no client wiring → first error path (catalog empty).
	_, res, err := srv.handleQuery(context.Background(), nil, promQueryIn{Promql: "up"})
	require.NoError(t, err)
	require.NotNil(t, res.Samples, "Samples on error must be non-nil so JSON renders [] not null")
	body, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"samples":[]`)
	assert.NotContains(t, string(body), `"samples":null`)
}

func TestNew_DefaultHTTPClientHasNoHardTimeout(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	require.Zero(t, srv.httpClient.Timeout, "default http.Client must not impose a hard timeout — propagate the context deadline instead")
}
