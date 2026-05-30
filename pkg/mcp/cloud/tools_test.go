package cloud

import (
	"context"
	"testing"

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
