package cloud

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTools_Registered confirms the four cloud tools are exposed and that the
// set registered on the server matches the ToolSpecs() catalog exactly — the
// wire test fails if registration drifts from the catalog.
func TestTools_Registered(t *testing.T) {
	t.Parallel()

	srv, err := New(Options{Provider: &fakeProvider{}})
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

	registered := map[string]bool{}
	for _, tool := range list.Tools {
		registered[tool.Name] = true
	}

	cataloged := map[string]bool{}
	for _, spec := range ToolSpecs() {
		cataloged[spec.Name] = true
		assert.True(t, registered[spec.Name], "tool %q in ToolSpecs() but not registered", spec.Name)
	}
	for name := range registered {
		assert.True(t, cataloged[name], "tool %q registered but absent from ToolSpecs()", name)
	}

	for _, want := range []string{"list_inventory", "session_status", "run_cli", "list_allowed_commands"} {
		assert.True(t, registered[want], "%s not registered", want)
	}
}
