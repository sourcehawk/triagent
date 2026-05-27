package parallel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// startServerForTest wires a Server with a supplied registry, connects an
// in-memory client, and returns the session for calling tools.
func startServerForTest(t *testing.T, reg *Registry) *sdkmcp.ClientSession {
	t.Helper()
	srv, err := New(Options{registry: reg})
	require.NoError(t, err)
	ctx := context.Background()
	sT, cT := sdkmcp.NewInMemoryTransports()
	_, err = srv.impl.Connect(ctx, sT, nil)
	require.NoError(t, err)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, cT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestServer_CallTool_HappyPath(t *testing.T) {
	t.Parallel()
	reg := fakeUpstream(t, "triagent-git-alerts", 0, "answer", "")
	defer func() { _ = reg.Close() }()
	client := startServerForTest(t, reg)

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "call",
		Arguments: map[string]any{
			"summary": "spawning two sub-agents",
			"calls": []map[string]any{
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{"q": "1"}},
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{"q": "2"}},
			},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %s", firstTextContent(res))
	var out CallOut
	require.NotNil(t, res.StructuredContent)
	body, _ := json.Marshal(res.StructuredContent)
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, "spawning two sub-agents", out.Summary)
	require.Len(t, out.Results, 2)
	require.True(t, out.Results[0].OK)
	require.True(t, out.Results[1].OK)
}

func TestServer_CallTool_RejectsBadSchema(t *testing.T) {
	t.Parallel()
	reg := fakeUpstream(t, "triagent-git-alerts", 0, "x", "")
	defer func() { _ = reg.Close() }()
	client := startServerForTest(t, reg)

	cases := []struct {
		name string
		args map[string]any
	}{
		{
			name: "empty summary",
			args: map[string]any{"summary": "", "calls": []map[string]any{
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
			}},
		},
		{
			name: "summary too long",
			args: map[string]any{
				"summary": strings.Repeat("x", 200),
				"calls": []map[string]any{
					{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
					{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
				}},
		},
		{
			name: "calls length 1",
			args: map[string]any{"summary": "x", "calls": []map[string]any{
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
			}},
		},
		{
			name: "calls length 9",
			args: func() map[string]any {
				calls := make([]map[string]any, 9)
				for i := range calls {
					calls[i] = map[string]any{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}}
				}
				return map[string]any{"summary": "x", "calls": calls}
			}(),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
				Name: "call", Arguments: tc.args,
			})
			require.NoError(t, err)
			require.True(t, res.IsError, "expected schema rejection")
		})
	}
}

func TestServer_CallTool_ClampsMaxConcurrency(t *testing.T) {
	t.Parallel()
	reg := fakeUpstream(t, "triagent-git-alerts", 0, "x", "")
	defer func() { _ = reg.Close() }()
	client := startServerForTest(t, reg)

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "call",
		Arguments: map[string]any{
			"summary":         "ok",
			"max_concurrency": 99,
			"calls": []map[string]any{
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
				{"server": "triagent-git-alerts", "tool": "analyze_change", "input": map[string]any{}},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "max_concurrency > 8 must be rejected")
}
