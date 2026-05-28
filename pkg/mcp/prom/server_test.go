package prom

import (
	"context"
	"testing"

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
func TestNew_DefaultHTTPClientHasNoHardTimeout(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	require.Zero(t, srv.httpClient.Timeout, "default http.Client must not impose a hard timeout — propagate the context deadline instead")
}
