package teleport

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTools_Registered confirms list_clusters and login are exposed.
func TestTools_Registered(t *testing.T) {
	t.Parallel()

	srv, err := New(Options{KubeconfigPath: "/tmp/nonexistent-kubeconfig"})
	require.NoError(t, err)

	serverT, clientT := sdkmcp.NewInMemoryTransports()
	serverSession, err := srv.impl.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	list, err := clientSession.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	require.NoError(t, err)

	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["list_clusters"], "list_clusters not registered")
	assert.True(t, names["login"], "login not registered")
}
