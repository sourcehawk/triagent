package k8s

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// The tests in this file guard against a SUBTLE regression: the MCP SDK's
// AddTool[In, Out] helper auto-populates CallToolResult.StructuredContent
// from whatever the handler returns as Out, REGARDLESS of whether the
// handler also set Content by hand. If Out is `struct{}{}` the wire result
// carries StructuredContent={}, and clients that prefer StructuredContent
// (Claude Code, for one) treat the tool output as empty and never look at
// Content — even though Content has the rendered YAML/log body.
//
// For tools whose payload is an opaque text body (get_resource, get_logs),
// we therefore use Out=any + return nil so StructuredContent is never set.
// These tests invoke the tool through the real SDK server/client machinery
// over an in-memory transport, so the wrapping that caused the bug is
// exercised end-to-end. A unit test on the handler method alone would
// miss it (the handler returned a fine *CallToolResult; the damage
// happened in the SDK wrapper).

// callRegisteredTool spins up a real server+client pair over an in-memory
// transport, registers the two opaque-text tools on the server, and
// returns a handle for invoking them as a client would.
func callRegisteredTool(t *testing.T, kit *ToolKit, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	ctx := context.Background()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "get_resource", Description: "test"}, kit.getResource)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "get_logs", Description: "test"}, kit.getLogs)

	serverT, clientT := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverT, nil)
	require.NoError(t, err, "server connect")
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err, "client connect")
	t.Cleanup(func() { _ = clientSession.Close() })

	res, err := clientSession.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "call %s", name)
	return res
}

func textBody(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content, "no content returned")
	tc, ok := res.Content[0].(*sdkmcp.TextContent)
	require.True(t, ok, "content[0] is %T, want *TextContent", res.Content[0])
	return tc.Text
}

func TestGetResource_WireResponse_HasContentAndNoStructuredContent(t *testing.T) {
	t.Parallel()

	kit := newTestKit(t, unstructuredPod("zeebe-0", "app", "Running"))
	res := callRegisteredTool(t, kit, "get_resource", map[string]any{
		"kind":      "Pod",
		"name":      "zeebe-0",
		"namespace": "app",
	})

	require.False(t, res.IsError, "unexpected tool error")
	// The regression guard. If this fails, StructuredContent is populated
	// with (most likely) an empty object, and clients that prefer
	// structured content will treat the tool response as empty.
	assert.Nil(t, res.StructuredContent, "StructuredContent must be nil for opaque-text tools")
	body := textBody(t, res)
	assert.Contains(t, body, "name: zeebe-0")
	assert.Contains(t, body, "phase: Running")
}

func TestGetLogs_WireResponse_HasContentAndNoStructuredContent(t *testing.T) {
	t.Parallel()

	// Minimal kit: we just need the typed client to resolve pods for the
	// selector. The fake's GetLogs returns a canned body, which is fine —
	// we're checking the transport shape, not the log body.
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "zeebe-0", Namespace: "app", Labels: map[string]string{"app": "zeebe"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "zeebe"}}},
	})
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})

	res := callRegisteredTool(t, kit, "get_logs", map[string]any{"pod": "zeebe-0", "namespace": "app"})

	require.False(t, res.IsError, "unexpected tool error")
	assert.Nil(t, res.StructuredContent, "StructuredContent must be nil for opaque-text tools")
	body := textBody(t, res)
	assert.Contains(t, body, "pod=zeebe-0", "log body header missing pod name")
}

func TestContextTools_Registered(t *testing.T) {
	t.Parallel()

	srv, err := New(context.Background(), Options{KubeconfigPath: "/tmp/anything"})
	require.NoError(t, err)

	serverT, clientT := sdkmcp.NewInMemoryTransports()
	srvSess, err := srv.impl.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srvSess.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	cliSess, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cliSess.Close() })

	list, err := cliSess.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	require.NoError(t, err)

	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["list_contexts"], "list_contexts not registered")
	assert.True(t, names["get_current_context"], "get_current_context not registered")
	assert.True(t, names["switch_context"], "switch_context not registered")
}

// Error results must also not leak empty StructuredContent — the same
// mechanism would mask the error message in Content.
func TestGetResource_WireResponse_ErrorsStillHaveNoStructuredContent(t *testing.T) {
	t.Parallel()

	kit := newTestKit(t) // no pods in the fake client → Pod "ghost" not found
	res := callRegisteredTool(t, kit, "get_resource", map[string]any{
		"kind":      "Pod",
		"name":      "ghost",
		"namespace": "app",
	})

	require.True(t, res.IsError, "expected error result, got success")
	assert.Nil(t, res.StructuredContent, "StructuredContent must be nil on error results")
	assert.Contains(t, textBody(t, res), "not found")
}
