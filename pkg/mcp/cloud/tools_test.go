package cloud

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T, p Provider, opts ...func(*Options)) *Server {
	t.Helper()
	o := Options{Provider: p}
	for _, f := range opts {
		f(&o)
	}
	srv, err := New(o)
	require.NoError(t, err)
	return srv
}

// errText reads the text content of a tool error result.
func errText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestRunCLIRequiresActiveTargetWhenMultiple(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "a"}, {ID: "b"}}, binary: "/bin/echo",
		allowlist: &CommandAllowlist{Commands: []Command{{Path: "echo"}}}})
	res, _, _ := s.runCLI(context.Background(), nil, RunCLIInput{Argv: []string{"echo", "x"}})
	require.True(t, res.IsError)
	require.Contains(t, errText(res), "set_active_target")

	require.NoError(t, s.setActive("a"))
	res2, out2, err2 := s.runCLI(context.Background(), nil, RunCLIInput{Argv: []string{"echo", "x"}})
	require.NoError(t, err2)
	require.Nil(t, res2, "with an active target the command runs (no error result)")
	require.Contains(t, out2.Stdout, "x")
}

func TestSetActiveTargetTool(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}, identity: IdentityStatus{Provider: "fake", AssumedIdentity: "ro@acct-1", Valid: true}})
	_, out, err := s.setActiveTarget(context.Background(), nil, SetActiveTargetInput{Target: "acct-1"})
	require.NoError(t, err)
	require.True(t, out.Valid)
	require.Equal(t, "acct-1", s.activeTarget)

	res, _, _ := s.setActiveTarget(context.Background(), nil, SetActiveTargetInput{Target: "nope"})
	require.True(t, res.IsError)
}

func TestListInventoryReturnsProviderScopes(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{inventory: Inventory{Scopes: []Scope{{ID: "prod", Name: "Production"}}}}
	srv := newTestServer(t, p)
	_, out, err := srv.listInventory(context.Background(), nil, ListInventoryInput{})
	require.NoError(t, err)
	require.Len(t, out.Scopes, 1)
	require.Equal(t, "prod", out.Scopes[0].ID)
}

func TestSessionStatusReturnsProbeIdentity(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{
		name:     "gcp",
		identity: IdentityStatus{Provider: "gcp", AssumedIdentity: "ro-sa@proj", Valid: true},
	}
	srv := newTestServer(t, p)
	_, out, err := srv.sessionStatus(context.Background(), nil, SessionStatusInput{})
	require.NoError(t, err)
	require.True(t, out.Valid)
	require.Equal(t, "ro-sa@proj", out.AssumedIdentity)
}

func TestListAllowedCommandsReturnsLoadedAllowlist(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{allowlist: &CommandAllowlist{Commands: []Command{
		{Path: "projects list", Description: "orient: list projects"},
	}}}
	srv := newTestServer(t, p)
	_, out, err := srv.listAllowedCommands(context.Background(), nil, ListAllowedCommandsInput{})
	require.NoError(t, err)
	require.Len(t, out.Commands, 1)
	require.Equal(t, "projects list", out.Commands[0].Path)
}

func TestListAllowedCommandsDropsDenyFlooredEntries(t *testing.T) {
	t.Parallel()
	// Even if a provider default lists a floored command, the catalog the agent
	// sees is exactly what run_cli enforces — the floored entry is absent.
	p := &fakeProvider{allowlist: &CommandAllowlist{Commands: []Command{
		{Path: "projects list"},
		{Path: "secrets versions access"},
	}}}
	srv := newTestServer(t, p)
	_, out, err := srv.listAllowedCommands(context.Background(), nil, ListAllowedCommandsInput{})
	require.NoError(t, err)
	for _, c := range out.Commands {
		require.NotEqual(t, "secrets versions access", c.Path, "deny-floored command must not be advertised")
	}
}

func TestRunCLIRejectsDenyFlooredArgvBeforeExec(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{
		binary:    "/bin/echo",
		allowlist: &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}},
	}
	srv := newTestServer(t, p)
	res, _, err := srv.runCLI(context.Background(), nil, RunCLIInput{
		Argv: []string{"compute", "instances", "list", "--impersonate-service-account", "evil"},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError, "deny-floored argv must be rejected as a tool error before exec")
}

func TestRunCLIShapesResultOnSuccess(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{
		binary:    "/bin/echo",
		allowlist: &CommandAllowlist{Commands: []Command{{Path: "projects list"}}},
	}
	srv := newTestServer(t, p)
	_, out, err := srv.runCLI(context.Background(), nil, RunCLIInput{Argv: []string{"projects", "list"}})
	require.NoError(t, err)
	require.Contains(t, out.Stdout, "projects list")
}

func TestRunCLIRejectsOutOfScopeTarget(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{
		binary:    "/bin/echo",
		allowlist: &CommandAllowlist{Commands: []Command{{Path: "projects list"}}},
	}
	srv := newTestServer(t, p, func(o *Options) {
		o.Scope = ScopeAllowlist{Projects: []string{"prod"}}
	})
	res, _, err := srv.runCLI(context.Background(), nil, RunCLIInput{
		Argv: []string{"projects", "list", "--project", "other"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "out-of-scope target must be rejected")
}
