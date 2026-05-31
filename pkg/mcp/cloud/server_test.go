package cloud

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeProviderSatisfiesActiveTargetContract(t *testing.T) {
	t.Parallel()
	var p Provider = &fakeProvider{}
	require.NotNil(t, p)
	// compile-time: the interface now includes ActiveTargetEnv + ConfiguredTargets
}

func TestNewRequiresProvider(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	require.Error(t, err, "expected error when Provider is nil")
	_, err = New(Options{Provider: &fakeProvider{}})
	require.NoError(t, err)
}

func TestSelectableTargetsPrefersConfigured(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{targets: []Target{{ID: "acct-1", Name: "one"}}}
	s := newTestServer(t, p)
	got := s.selectableTargets(context.Background())
	assert.Equal(t, []Target{{ID: "acct-1", Name: "one"}}, got)
}

func TestSelectableTargetsFallsBackToScopeProjects(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{}, func(o *Options) {
		o.Scope = ScopeAllowlist{Projects: []string{"prod", "staging"}}
	})
	got := s.selectableTargets(context.Background())
	assert.Equal(t, []Target{{ID: "prod", Name: "prod"}, {ID: "staging", Name: "staging"}}, got)
}

func TestSelectableTargetsFallsBackToInventory(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{inventory: Inventory{Scopes: []Scope{{ID: "p1", Name: "Project One"}}}}
	s := newTestServer(t, p)
	got := s.selectableTargets(context.Background())
	assert.Equal(t, []Target{{ID: "p1", Name: "Project One"}}, got)
}

// liveInventoryProvider mirrors the real gcp/aws providers: its Inventory shells
// through the injected RunFunc rather than returning a canned set. The
// in-package fakeProvider ignores run, so only this double exercises the path
// where deriving the selectable set executes the CLI — the path that recursed
// before selectableTargets was given a run core without the active-target gate.
type liveInventoryProvider struct {
	fakeProvider
	scopes []Scope
}

func (p *liveInventoryProvider) Inventory(ctx context.Context, run RunFunc) (Inventory, error) {
	if _, err := run(ctx, []string{"echo", "inventory"}); err != nil {
		return Inventory{}, err
	}
	return Inventory{Scopes: p.scopes}, nil
}

// TestSelectableTargetsInventoryDoesNotRecurse pins that deriving the selectable
// set from live inventory does not re-enter the active-target gate. With no
// configured targets and no scope, selectableTargets shells inventory; that
// inventory run must not consult selectableTargets again, or New stack-overflows
// for unconstrained GCP and single-account AWS sources.
func TestSelectableTargetsInventoryDoesNotRecurse(t *testing.T) {
	t.Parallel()
	p := &liveInventoryProvider{
		fakeProvider: fakeProvider{
			binary:    "/bin/echo",
			allowlist: &CommandAllowlist{Commands: []Command{{Path: "echo"}}},
		},
		scopes: []Scope{{ID: "p1", Name: "one"}, {ID: "p2", Name: "two"}},
	}
	s := newTestServer(t, p)
	got := s.selectableTargets(context.Background())
	assert.Equal(t, []Target{{ID: "p1", Name: "one"}, {ID: "p2", Name: "two"}}, got)
}

func TestSetActiveTargetRejectsOutOfSet(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}})
	require.Error(t, s.setActive("acct-9"))
	require.NoError(t, s.setActive("acct-1"))
	assert.Equal(t, "acct-1", s.activeTarget)
}

func TestSubprocessEnvIncludesActiveTarget(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}})
	require.NoError(t, s.setActive("acct-1"))
	assert.Contains(t, s.subprocessEnv(), "FAKE_TARGET=acct-1")
}

func TestSingleTargetIsDefaultActive(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "only"}}})
	assert.Equal(t, "only", s.activeTarget)
}

func TestMultiTargetHasNoDefault(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "a"}, {ID: "b"}}})
	assert.Equal(t, "", s.activeTarget)
}

// TestSubprocessEnvDropsParentSecretsKeepsPassthrough exercises the env the
// server actually builds for run_cli — the path the real harness takes, which
// the isolated execCLI test cannot cover. A parent-env canary must be dropped
// while a declared passthrough var survives, so ambient launcher secrets never
// reach the provider CLI.
func TestSubprocessEnvDropsParentSecretsKeepsPassthrough(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_LEAK_CANARY", "should-not-appear")
	t.Setenv("CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT", "ro-sa@proj.iam.gserviceaccount.com")
	p := &fakeProvider{
		envPassthrough: []string{"CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"},
	}
	srv := newTestServer(t, p)

	env := srv.subprocessEnv()

	assert.NotContains(t, env, "TRIAGENT_CLOUD_LEAK_CANARY=should-not-appear",
		"parent-env secret must be dropped from the subprocess env")
	assert.Contains(t, env, "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=ro-sa@proj.iam.gserviceaccount.com",
		"declared passthrough var must be forwarded")
}
