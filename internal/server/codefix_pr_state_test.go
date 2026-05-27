package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestManagerForCodefixPRState constructs the minimal Manager
// needed so apiHandlers.refreshAllPRStates can call
// a.manager.publishGlobalEvent.
func newTestManagerForCodefixPRState(t *testing.T) *Manager {
	t.Helper()
	return NewManager(context.Background(), t.TempDir())
}

// stubCodefixGhWithCapture returns a stub that pushes each call's args
// into the passed-in slice. Used by the fan-out test to assert the
// per-repo arg lists.
func stubCodefixGhWithCapture(out *[][]string) *stubCodefixGh {
	return &stubCodefixGh{
		responseFn: func(args []string) []byte {
			*out = append(*out, append([]string(nil), args...))
			return []byte("[]")
		},
	}
}

func TestRefreshAllPRStates_FansOutAcrossRepos(t *testing.T) {
	t.Parallel()
	var capturedArgs [][]string
	stub := stubCodefixGhWithCapture(&capturedArgs)

	mgr := newTestManagerForCodefixPRState(t)
	a := &apiHandlers{
		opts:      Options{SessionsRepo: "sourcehawk/triagent-sessions"},
		manager:   mgr,
		codefixGh: stub,
		codefixRepos: map[string]struct{}{
			"example-org/example-service":   {},
			"example-org/example-app": {},
		},
	}
	require.NoError(t, a.refreshAllPRStates(context.Background()))
	require.Len(t, capturedArgs, 3, "expected one gh call per repo (1 sessions + 2 codefix)")

	var labelArgs int
	for _, callArgs := range capturedArgs {
		for _, arg := range callArgs {
			if arg == "label:triagent-proposal" {
				labelArgs++
			}
		}
	}
	require.Equal(t, 2, labelArgs, "label filter should be set for codefix repos only")
}

// TestRefreshAllPRStates_PublishesOnStateChange seeds prStateCache
// with an "open" entry, polls again with the state changed to
// "merged", and asserts the global SSE bus saw a codefix_pr_state
// envelope.
func TestRefreshAllPRStates_PublishesOnStateChange(t *testing.T) {
	t.Parallel()
	mergedResp, _ := json.Marshal([]map[string]any{
		{
			"url":      "https://github.com/example-org/example-service/pull/77",
			"state":    "MERGED",
			"mergedAt": "2026-05-10T20:00:00Z",
			"closedAt": nil,
		},
	})
	stub := &stubCodefixGh{response: mergedResp}
	mgr := newTestManagerForCodefixPRState(t)
	a := &apiHandlers{
		opts:      Options{}, // no sessions repo so only the codefix repo polls
		manager:   mgr,
		codefixGh: stub,
		codefixRepos: map[string]struct{}{
			"example-org/example-service": {},
		},
		prStateCache: map[string]PRInfo{
			canonicalURL("https://github.com/example-org/example-service/pull/77"): {
				URL:   "https://github.com/example-org/example-service/pull/77",
				State: PRStateOpen,
			},
		},
	}
	_, ch, _, cancel := mgr.SubscribeStream("test-tok-"+t.Name(), 0)
	defer cancel()

	require.NoError(t, a.refreshAllPRStates(context.Background()))

	// Drain envelopes; find a codefix_pr_state for our PR URL.
	var found *CodefixPRStatePayload
	for {
		select {
		case env := <-ch:
			if env.Kind == globalKindCodefixPRState && env.CodefixPRState != nil &&
				env.CodefixPRState.URL == canonicalURL("https://github.com/example-org/example-service/pull/77") {
				found = env.CodefixPRState
			}
		default:
			goto done
		}
	}
done:
	require.NotNil(t, found, "expected codefix_pr_state envelope")
	require.Equal(t, PRStateMerged, found.State)
	require.NotNil(t, found.MergedAt)
}

// TestRefreshAllPRStates_SuppressesWhenUnchanged seeds cache with the
// SAME state the poller returns; no envelope should fire.
func TestRefreshAllPRStates_SuppressesWhenUnchanged(t *testing.T) {
	t.Parallel()
	openResp, _ := json.Marshal([]map[string]any{
		{"url": "https://github.com/example-org/example-service/pull/77", "state": "OPEN", "mergedAt": nil, "closedAt": nil},
	})
	stub := &stubCodefixGh{response: openResp}
	mgr := newTestManagerForCodefixPRState(t)
	a := &apiHandlers{
		opts:      Options{},
		manager:   mgr,
		codefixGh: stub,
		codefixRepos: map[string]struct{}{"example-org/example-service": {}},
		prStateCache: map[string]PRInfo{
			canonicalURL("https://github.com/example-org/example-service/pull/77"): {
				URL:   "https://github.com/example-org/example-service/pull/77",
				State: PRStateOpen,
			},
		},
	}
	_, ch, _, cancel := mgr.SubscribeStream("test-tok-"+t.Name(), 0)
	defer cancel()
	require.NoError(t, a.refreshAllPRStates(context.Background()))
	select {
	case env := <-ch:
		if env.Kind == globalKindCodefixPRState {
			t.Fatalf("expected no codefix_pr_state envelope but got %+v", env)
		}
	default:
		// good — nothing on the bus
	}
}

func TestTrackCodefixRepo_AddsToSet(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	a.trackCodefixRepo("example-org/example-service")
	a.trackCodefixRepo("example-org/example-app")
	a.trackCodefixRepo("example-org/example-service") // idempotent
	require.Len(t, a.codefixRepos, 2)
}

func TestIsCodefixURL(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{
		codefixRepos: map[string]struct{}{
			"example-org/example-service": {},
		},
	}
	require.True(t, a.isCodefixURL("https://github.com/example-org/example-service/pull/77"))
	require.False(t, a.isCodefixURL("https://github.com/sourcehawk/triagent-sessions/pull/42"))
	require.False(t, a.isCodefixURL("https://github.com/other/repo/pull/1"))
}

func TestEqualTimePtr(t *testing.T) {
	t.Parallel()
	require.True(t, equalTimePtr(nil, nil))
	t1, _ := time.Parse(time.RFC3339, "2026-05-10T20:00:00Z")
	t2, _ := time.Parse(time.RFC3339, "2026-05-10T20:00:00Z")
	t3, _ := time.Parse(time.RFC3339, "2026-05-11T00:00:00Z")
	require.True(t, equalTimePtr(&t1, &t2))
	require.False(t, equalTimePtr(&t1, &t3))
	require.False(t, equalTimePtr(nil, &t1))
	require.False(t, equalTimePtr(&t1, nil))
}
