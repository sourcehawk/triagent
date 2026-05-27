package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPreflightAPI returns an apiHandlers suitable for testing handlePreflight.
// The preflightFn is stubbed out so no kubernetes I/O occurs.
func newPreflightAPI(t *testing.T) *apiHandlers {
	t.Helper()
	sessionsRoot := t.TempDir()
	return &apiHandlers{
		opts:        Options{SessionsRoot: sessionsRoot},
		manager:     NewManager(context.Background(), sessionsRoot),
		connections: connections.NewWithDir(t.TempDir()),
		preflightFn: func(opts preflight.Options) (*preflight.Result, error) {
			return &preflight.Result{
				MCPConfigPath: "/dev/null",
				DocsPrefix:    "",
			}, nil
		},
	}
}

func TestPreflight_AcceptsPickerFieldsAndDerivesURL(t *testing.T) {
	t.Parallel()
	a := newPreflightAPI(t)
	require.NoError(t, a.connections.SetSlackToken("xoxp-test", "https://example.slack.com"))

	body := strings.NewReader(`{
		"inputs": {
			"cluster_id":    {"value": "test-cluster-id"},
			"slack_channel": {"id": "C0123ABC", "name": "incident-2026-foo", "url": ""}
		}
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	a.handlePreflight(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)

	invs := a.manager.List()
	require.Len(t, invs, 1)
	got := invs[0]
	assert.Equal(t, "C0123ABC", got.SlackChannelID)
	assert.Equal(t, "incident-2026-foo", got.SlackChannelName)
	assert.Equal(t, "https://example.slack.com/archives/C0123ABC", got.SlackChannelURL,
		"backend must derive URL from workspace URL + channel id")
}

func TestPreflight_URLFallbackPath(t *testing.T) {
	t.Parallel()
	a := newPreflightAPI(t)
	// Slack NOT connected — no token, no workspace URL.

	body := strings.NewReader(`{
		"inputs": {
			"cluster_id":    {"value": "test-cluster-id"},
			"slack_channel": {"id": "", "name": "", "url": "https://other.slack.com/archives/C999"}
		}
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	a.handlePreflight(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	invs := a.manager.List()
	require.Len(t, invs, 1)
	got := invs[0]
	assert.Equal(t, "https://other.slack.com/archives/C999", got.SlackChannelURL)
	assert.Empty(t, got.SlackChannelID, "URL fallback path must not invent IDs")
	assert.Empty(t, got.SlackChannelName)
}

func TestPreflight_PickerWithMissingWorkspaceURLLeavesURLEmpty(t *testing.T) {
	t.Parallel()
	a := newPreflightAPI(t)
	// Token stored but workspace URL never captured (empty string).
	require.NoError(t, a.connections.SetSlackToken("xoxp-test", ""))

	body := strings.NewReader(`{
		"inputs": {
			"cluster_id":    {"value": "test-cluster-id"},
			"slack_channel": {"id": "C0123ABC", "name": "incident-2026-foo", "url": ""}
		}
	}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	a.handlePreflight(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	invs := a.manager.List()
	require.Len(t, invs, 1)
	got := invs[0]
	assert.Equal(t, "C0123ABC", got.SlackChannelID)
	assert.Equal(t, "incident-2026-foo", got.SlackChannelName)
	assert.Empty(t, got.SlackChannelURL, "no workspace URL → no derivation; URL stays empty")
}

func TestPreflight_EmptyInputsSucceedsWithNoProfile(t *testing.T) {
	t.Parallel()
	// When no profile is loaded (prof == nil) the handler skips schema
	// validation — an empty inputs map is accepted as-is.
	a := newPreflightAPI(t)

	body := strings.NewReader(`{"inputs": {}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	a.handlePreflight(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
}

func TestPreflight_AcceptsNotesOnlyWithEmptyNamespace(t *testing.T) {
	t.Parallel()
	a := newPreflightAPI(t)

	body := strings.NewReader(`{"inputs": {"notes": {"value": "something is wrong with the cluster"}}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	a.handlePreflight(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	invs := a.manager.List()
	require.Len(t, invs, 1)
	got := invs[0]
	assert.Empty(t, got.Namespace, "no cluster_id → no namespace; must stay empty")
	assert.Equal(t, "something is wrong with the cluster", got.Notes)
}

func TestPreflight_AcceptsClusterID(t *testing.T) {
	t.Parallel()
	a := newPreflightAPI(t)

	body := strings.NewReader(`{"inputs": {"cluster_id": {"value": "abc"}}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	a.handlePreflight(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	invs := a.manager.List()
	require.Len(t, invs, 1)
	// cluster_id is used for auto-briefing only; not stored on Investigation.
	// Namespace is no longer derived from cluster_id; starts empty.
	assert.Empty(t, invs[0].Namespace, "namespace must not be derived from cluster_id")
}

func TestHandleMessage_RehydratesBeforeSend(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(context.Background(), dir)
	inv := &Investigation{
		ID:              "id",
		SessionDir:      dir,
		Namespace:       "ns",
		ClaudeSessionID: "sess",
		needsRehydrate:  true,
		archived:        false,
	}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	mgr.byID[inv.ID] = inv

	var preflightCalls int
	a := &apiHandlers{
		manager: mgr,
		preflightFn: func(_ preflight.Options) (*preflight.Result, error) {
			preflightCalls++
			return &preflight.Result{MCPConfigPath: dir + "/mcp.json"}, nil
		},
		sessionFn: stubSession(),
	}

	body := strings.NewReader(`{"text":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/messages", body)
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()

	a.handleMessage(rr, req)

	// Rehydrate should have run.
	if preflightCalls != 1 {
		t.Fatalf("preflightCalls = %d, want 1", preflightCalls)
	}
	// Without claude actually being on PATH the SendFollowUp will likely
	// have failed — assert specifically on the rehydrate gate, not the
	// downstream send.
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if inv.needsRehydrate {
		t.Errorf("needsRehydrate must be false after a successful rehydrate")
	}
}
