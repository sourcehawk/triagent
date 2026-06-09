package slack

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// slackWireSession connects an in-memory client to the server's MCP impl so
// a tool call goes through the go-sdk's output-schema validation — the layer
// the handler unit tests bypass by calling the method directly.
func slackWireSession(t *testing.T) *sdkmcp.ClientSession {
	t.Helper()
	s, err := New(Options{Token: "xoxp-test", CacheDir: t.TempDir()})
	require.NoError(t, err)

	srvT, cliT := sdkmcp.NewInMemoryTransports()
	srvSess, err := s.impl.Connect(context.Background(), srvT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srvSess.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "wire-test", Version: "v0"}, nil)
	cliSess, err := client.Connect(context.Background(), cliT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cliSess.Close() })
	return cliSess
}

// TestWire_Slack_CitationTools_MissingArgs_NilCitationsDoesNotBreakSchema
// covers the early-return paths of the two slack sub-agent tools, whose Out
// structs carry a non-nullable citations array. A missing required argument
// returns before any sub-agent run, with a nil Citations slice pre-fix —
// which the SDK rejects as "citations":null, replacing the handler's
// "… is required" message with an opaque output-schema error.
func TestWire_Slack_CitationTools_MissingArgs_NilCitationsDoesNotBreakSchema(t *testing.T) {
	t.Parallel()
	cliSess := slackWireSession(t)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"summarize_thread", map[string]any{"channel_id": "", "thread_ts": "", "desired_findings": ""}},
		{"analyze_channel", map[string]any{"channel_id": "", "desired_findings": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			res, err := cliSess.CallTool(context.Background(), &sdkmcp.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			require.NoError(t, err, "nil Citations on the early-return path must not trip output-schema validation")
			require.True(t, res.IsError, "missing args must surface as a tool error")
			require.NotEmpty(t, res.Content)
		})
	}
}
