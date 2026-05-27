package parallel

import (
	"context"
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestParseUpstreams_ValidJSON(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]UpstreamSpec{
		"triagent-git-alerts": {Command: "/bin/triagent-mcp", Args: []string{"serve", "--kind=git", "--repo=example-org/alerts"}, Env: map[string]string{"TRIAGENT_MCP_GIT_REPO": "example-org/alerts"}},
		"triagent-wiki":       {Command: "/bin/triagent-mcp", Args: []string{"serve", "--kind=wiki"}},
	})
	require.NoError(t, err)
	got, err := ParseUpstreams(string(raw))
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "/bin/triagent-mcp", got["triagent-git-alerts"].Command)
	require.Equal(t, []string{"serve", "--kind=git", "--repo=example-org/alerts"}, got["triagent-git-alerts"].Args)
	require.Equal(t, "example-org/alerts", got["triagent-git-alerts"].Env["TRIAGENT_MCP_GIT_REPO"])
}

func TestParseUpstreams_RejectsMalformed(t *testing.T) {
	t.Parallel()
	_, err := ParseUpstreams("not-json")
	require.Error(t, err)
}

func TestParseUpstreams_EmptyStringYieldsEmptyMap(t *testing.T) {
	t.Parallel()
	got, err := ParseUpstreams("")
	require.NoError(t, err)
	require.Len(t, got, 0)
}

// TestRegistry_LazySpawnAndReuse exercises the cache: connecting to the
// same alias twice reuses the underlying session. Uses an in-memory
// transport via the test-only Registry constructor so we don't spawn a
// subprocess.
func TestRegistry_LazySpawnAndReuse(t *testing.T) {
	t.Parallel()
	spawns := 0
	upstreams := map[string]UpstreamSpec{
		"triagent-fake": {Command: "/does/not/matter"},
	}
	reg := newRegistryForTest(upstreams, func(_ UpstreamSpec) (*sdkmcp.ClientSession, func() error, error) {
		spawns++
		server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "v0"}, nil)
		sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "ping", Description: "test"},
			func(_ context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, struct{}, error) {
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "pong"}}}, struct{}{}, nil
			})
		sT, cT := sdkmcp.NewInMemoryTransports()
		ctx := context.Background()
		_, err := server.Connect(ctx, sT, nil)
		require.NoError(t, err)
		client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0"}, nil)
		sess, err := client.Connect(ctx, cT, nil)
		require.NoError(t, err)
		return sess, sess.Close, nil
	})
	defer func() { _ = reg.Close() }()
	ctx := context.Background()
	sess1, err := reg.Client(ctx, "triagent-fake")
	require.NoError(t, err)
	require.NotNil(t, sess1)
	sess2, err := reg.Client(ctx, "triagent-fake")
	require.NoError(t, err)
	require.Same(t, sess1, sess2, "second call must reuse cached session")
	require.Equal(t, 1, spawns, "spawn should run only once")
}

func TestRegistry_UnknownAliasErrors(t *testing.T) {
	t.Parallel()
	reg := newRegistryForTest(map[string]UpstreamSpec{}, nil)
	defer func() { _ = reg.Close() }()
	_, err := reg.Client(context.Background(), "triagent-nope")
	require.Error(t, err)
}

func TestBuildUpstreamCommand_PopulatesArgsAndEnv(t *testing.T) {
	t.Parallel()
	spec := UpstreamSpec{Command: "/bin/echo", Args: []string{"a", "b"}, Env: map[string]string{"K": "V"}}
	cmd := buildUpstreamCommand(spec)
	require.Equal(t, "/bin/echo", cmd.Path)
	require.Equal(t, []string{"/bin/echo", "a", "b"}, cmd.Args)
	require.Contains(t, cmd.Env, "K=V")
}
