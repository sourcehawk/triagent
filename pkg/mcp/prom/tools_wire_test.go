package prom

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWire_AllToolsRegistered(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)

	srvT, cliT := sdkmcp.NewInMemoryTransports()
	srvSess, err := srv.impl.Connect(context.Background(), srvT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srvSess.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "wire-test", Version: "v0"}, nil)
	cliSess, err := client.Connect(context.Background(), cliT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cliSess.Close() })

	list, err := cliSess.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"prom_list_metrics", "prom_describe_metric", "prom_query", "prom_recent_value", "prom_query_range"} {
		assert.True(t, names[want], "tool %s not registered", want)
	}
}

func TestWire_EmptyCatalog_PromListMetrics_ReturnsError(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	srvT, cliT := sdkmcp.NewInMemoryTransports()
	srvSess, _ := srv.impl.Connect(context.Background(), srvT, nil)
	t.Cleanup(func() { _ = srvSess.Close() })
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "wire-test", Version: "v0"}, nil)
	cliSess, _ := client.Connect(context.Background(), cliT, nil)
	t.Cleanup(func() { _ = cliSess.Close() })

	res, err := cliSess.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "prom_list_metrics",
		Arguments: map[string]any{"query": "anything"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected catalog-empty error")
	require.NotEmpty(t, res.Content)
}

func TestWire_PromInfoResource_Readable(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{Endpoint: "http://127.0.0.1:9090"})
	require.NoError(t, err)
	srvT, cliT := sdkmcp.NewInMemoryTransports()
	srvSess, _ := srv.impl.Connect(context.Background(), srvT, nil)
	t.Cleanup(func() { _ = srvSess.Close() })
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "wire-test", Version: "v0"}, nil)
	cliSess, _ := client.Connect(context.Background(), cliT, nil)
	t.Cleanup(func() { _ = cliSess.Close() })

	res, err := cliSess.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: "prom://info"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Contents)
	require.Contains(t, res.Contents[0].Text, "0 metrics indexed")
}
