package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/internal/sessions"
)

// stubPreflight returns an installable preflightFn that records calls
// and returns a fixed result/err.
func stubPreflight(t *testing.T, res *preflight.Result, err error) func(preflight.Options) (*preflight.Result, error) {
	t.Helper()
	return func(opts preflight.Options) (*preflight.Result, error) {
		return res, err
	}
}

// stubSession returns a sessionFn that hands back a no-op session
// without exec'ing the `claude` binary. Used by every test that drives
// rehydrate through to a successful build-session step.
func stubSession() func(sessions.Options, string) (investigationSession, error) {
	return func(_ sessions.Options, _ string) (investigationSession, error) {
		return fakeAutoSession{}, nil
	}
}

func TestRehydrate_Success_ClearsNeedsRehydrate(t *testing.T) {
	dir := t.TempDir()
	a := &apiHandlers{
		manager: NewManager(context.Background(), dir),
		preflightFn: stubPreflight(t, &preflight.Result{
			MCPConfigPath:  dir + "/mcp.json",
			KubeconfigPath: "/tmp/kc",
		}, nil),
		sessionFn: stubSession(),
	}
	inv := &Investigation{
		ID:              "id",
		SessionDir:      dir,
		Namespace:       "ns",
		ClaudeSessionID: "sess",
		KubeconfigPath:  "/tmp/kc",
		LaunchCwd:       dir,
		needsRehydrate:  true,
	}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())

	require.NoError(t, a.rehydrate(inv), "rehydrate")
	inv.mu.Lock()
	defer inv.mu.Unlock()
	assert.False(t, inv.needsRehydrate, "needsRehydrate should be false after success")
	assert.False(t, inv.rehydrating, "rehydrating should be false after success")
}

func TestRehydrate_KubeFails_KeepsNeedsRehydrate_PublishesFailed(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(context.Background(), dir)
	t.Cleanup(mgr.Shutdown)
	a := &apiHandlers{
		manager:     mgr,
		preflightFn: stubPreflight(t, nil, errors.New("namespace not found")),
	}
	inv := &Investigation{
		ID:              "id",
		SessionDir:      dir,
		Namespace:       "ns",
		ClaudeSessionID: "sess",
		needsRehydrate:  true,
		manager:         mgr,
	}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	mgr.byID[inv.ID] = inv

	_, ch, _, cancel := mgr.SubscribeStream("tab", 0)
	t.Cleanup(cancel)

	require.Error(t, a.rehydrate(inv), "expected error from rehydrate, got nil")

	// Drain envelopes; require a started then a failed.
	deadline := time.After(time.Second)
	var sawStarted, sawFailed bool
	for !sawFailed {
		select {
		case env := <-ch:
			if env.Kind == envKindRehydrateState && env.RehydrateState != nil {
				switch env.RehydrateState.Phase {
				case "started":
					sawStarted = true
				case "failed":
					sawFailed = true
					assert.NotEmpty(t, env.RehydrateState.Error, "failed envelope must carry Error")
				}
			}
		case <-deadline:
			t.Fatalf("timeout; sawStarted=%v sawFailed=%v", sawStarted, sawFailed)
		}
	}
	assert.True(t, sawStarted, "expected a rehydrate_state.started envelope before failed")
	inv.mu.Lock()
	defer inv.mu.Unlock()
	assert.True(t, inv.needsRehydrate, "needsRehydrate should stay true on failure")
}

func TestRehydrate_AlreadyRehydrating_ReturnsErrRehydrating(t *testing.T) {
	a := &apiHandlers{
		manager:     NewManager(context.Background(), t.TempDir()),
		preflightFn: stubPreflight(t, &preflight.Result{}, nil),
	}
	inv := &Investigation{
		ID:             "id",
		needsRehydrate: true,
		rehydrating:    true,
	}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())

	require.ErrorIs(t, a.rehydrate(inv), ErrRehydrating, "err")
}

// Regression for the resume-loses-MCPs bug. The previous rehydrate
// trusted inv's persisted external-state fields, which meant:
//   - LinkedRepos was nil after restart (never persisted), so triagent-git-*
//     servers vanished from the regenerated MCP config.
//   - Slack/Incidentio tokens were never re-fetched from the connections
//     manager, so triagent-slack / triagent-incidentio servers vanished too.
//   - WikiProposalsPath was passed raw from launcher opts.
//
// All three should now come from current launcher / connections state.
func TestRehydrate_EvaluatesExternalStateFreshly(t *testing.T) {
	connDir := t.TempDir()
	conns := connections.NewWithDir(connDir)
	t.Setenv("TRIAGENT_SLACK_TOKEN", "xoxb-test-slack")
	t.Setenv("TRIAGENT_INCIDENTIO_TOKEN", "io-test-key")

	wikiProposals := filepath.Join(t.TempDir(), "wiki-proposals")

	dir := t.TempDir()
	// Seed user_repos.yaml so the rehydrated session picks up the
	// example-service entry without relying on the (now-removed) flag/env
	// surface. The description must meet repos.MinDescriptionLength so the
	// add-time validator accepts it.
	userReposPath := filepath.Join(dir, "user_repos.yaml")
	require.NoError(t, repos.AddUserRepo(userReposPath, repos.LinkedRepo{
		Owner: "example-org", Name: "example-service",
		Description: "Example service used to exercise the rehydrate path under test.",
	}))

	var captured preflight.Options
	captureFn := func(opts preflight.Options) (*preflight.Result, error) {
		captured = opts
		return &preflight.Result{
			MCPConfigPath:  filepath.Join(opts.SessionDir, "mcp.json"),
			KubeconfigPath: "/tmp/kc",
		}, nil
	}

	a := &apiHandlers{
		manager:     NewManager(context.Background(), dir),
		connections: conns,
		opts: Options{
			UserReposPath:     userReposPath,
			WikiPath:          "/tmp/wiki",
			WikiProposalsPath: wikiProposals,
		},
		preflightFn: captureFn,
		sessionFn:   stubSession(),
	}
	inv := &Investigation{
		ID:              "id",
		SessionDir:      dir,
		Namespace:       "ns",
		ClaudeSessionID: "sess",
		KubeconfigPath:  "/tmp/kc",
		LaunchCwd:       dir,
		needsRehydrate:  true,
	}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())

	require.NoError(t, a.rehydrate(inv), "rehydrate")

	// Tokens were fetched from the connections manager, not pulled off
	// the persisted Investigation (which had no token fields).
	assert.Equal(t, "xoxb-test-slack", captured.SlackToken, "SlackToken")
	assert.Equal(t, "io-test-key", captured.IncidentioToken, "IncidentioToken")
	// Linked repos resolved from current launcher config rather than
	// from a per-session snapshot.
	require.Len(t, captured.LinkedRepos, 1, "LinkedRepos len")
	assert.Equal(t, "example-org", captured.LinkedRepos[0].Owner)
	assert.Equal(t, "example-service", captured.LinkedRepos[0].Name)
	// Wiki proposals path flowed through from Options.
	assert.Equal(t, wikiProposals, captured.WikiProposalsPath, "WikiProposalsPath")

	// inv now reflects current external state.
	inv.mu.Lock()
	defer inv.mu.Unlock()
	assert.True(t, inv.SlackMCPEnabled, "SlackMCPEnabled")
	assert.True(t, inv.IncidentioMCPEnabled, "IncidentioMCPEnabled")
	require.Len(t, inv.LinkedRepos, 1, "inv.LinkedRepos len")
	assert.Equal(t, "example-org/example-service", inv.LinkedRepos[0].Key())
}

