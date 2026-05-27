package prom

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_RequiresEndpoint(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	require.Error(t, err, "empty Endpoint must fail")
}

func TestNew_ReturnsServer(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestRun_ReturnsOnCanceledContext(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = srv.Run(ctx) // best-effort: SDK may return any error on cancelled-before-start; we just want no panic
}
