package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/auto"
	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/stretchr/testify/require"
)

// fakeAutoSession is a no-op investigationSession used by tests that
// exercise auto-mode HTTP handlers without spawning the claude binary.
// Resume returns an immediately-closed event channel so Investigation.drain
// completes (and resets streaming) without producing any envelopes.
type fakeAutoSession struct{}

func (fakeAutoSession) Start(context.Context) (<-chan claude.Event, error) {
	ch := make(chan claude.Event)
	close(ch)
	return ch, nil
}

func (fakeAutoSession) Resume(context.Context, string) (<-chan claude.Event, error) {
	ch := make(chan claude.Event)
	close(ch)
	return ch, nil
}

// newTestAPIWithAuto builds an apiHandlers wired to a Manager containing
// one Investigation in the given auto-mode phase. The Investigation has
// started=true and a no-op session so SendFollowUp pre-checks pass.
func newTestAPIWithAuto(t *testing.T, invID string, st auto.State) (*apiHandlers, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	inv := mgr.RegisterForTest(invID)
	inv.mu.Lock()
	inv.Auto = st
	inv.started = true
	inv.streaming = false
	inv.session = fakeAutoSession{}
	inv.mu.Unlock()
	a := &apiHandlers{manager: mgr, telemetryToken: "tok"}
	srv := httptest.NewServer(nil)
	return a, srv
}

func TestHandleAutoSendMessage_PublishesAndForwards(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)

	body := strings.NewReader(`{"text":"wiki"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/send-message", body)
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoSendMessage(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())

	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)
	found := false
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindUser && e.Origin == "operator" && e.Text == "wiki" {
			found = true
		}
	}
	require.True(t, found, "expected operator user envelope in backlog")
}

func TestHandleAutoSendMessage_RejectsWhenPaused(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhasePaused})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/send-message", strings.NewReader(`{"text":"x"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoSendMessage(w, req)
	require.Equal(t, http.StatusLocked, w.Code)
}

func TestHandleAutoSendMessage_RejectsMissingBearer(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/send-message", strings.NewReader(`{"text":"x"}`))
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoSendMessage(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleAutoSendMessage_PublishesOnlyOneEnvelope guards against a
// regression where the handler pre-published an operator user envelope
// *and* SendFollowUp re-published its own user envelope, producing two
// adjacent user envelopes per send. SendFollowUp now owns the publish
// (with the supplied origin), so exactly one user envelope must land on
// the wire per /auto/send-message call.
func TestHandleAutoSendMessage_PublishesOnlyOneEnvelope(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/send-message", strings.NewReader(`{"text":"wiki"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoSendMessage(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())

	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)
	count := 0
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindUser && e.Text == "wiki" {
			count++
		}
	}
	require.Equal(t, 1, count, "exactly one user envelope should land on the wire")
}

// TestHandleAutoFinish_Transitions exercises the happy path: an active
// investigation transitions to PhaseFinished, publishes an
// auto_mode_state envelope, and writes the new phase to disk so a
// rehydrate after restart sees finished.
func TestHandleAutoFinish_Transitions(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/finish", strings.NewReader(`{"reason":"done"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoFinish(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)
	require.Equal(t, auto.PhaseFinished, inv.Auto.Phase)
	found := false
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindAutoModeState && e.AutoMode != nil && e.AutoMode.Phase == "finished" {
			found = true
		}
	}
	require.True(t, found, "expected auto_mode_state envelope with phase=finished")
}

// TestHandleAutoFinish_SyncsOperatorState guards against a regression
// where /auto/finish flipped inv.Auto.Phase to PhaseFinished but did not
// notify the embedded auto.Operator, leaving its internal state at
// PhaseStarted. The next recordSuccess → snapshot → applyAutoState then
// observed prevPhase=PhaseFinished vs s.Phase=PhaseStarted, re-publishing
// an auto_mode_state{started} envelope and reverting on-disk metadata.
func TestHandleAutoFinish_SyncsOperatorState(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)

	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)

	backend := &fakeAutoBackend{}
	op := auto.New(auto.Config{
		Backend:   backend,
		PersistFn: func(s auto.State) { a.manager.applyAutoState(inv, s) },
	})
	inv.mu.Lock()
	inv.autoOp = op
	inv.autoBackend = backend
	inv.mu.Unlock()
	require.NoError(t, op.Start(context.Background(), "x"))

	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/finish", strings.NewReader(`{"reason":"done"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoFinish(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	require.Equal(t, auto.PhaseFinished, inv.Auto.Phase, "inv.Auto.Phase")
	require.Equal(t, auto.PhaseFinished, op.Phase(), "operator's internal phase must sync to finished")
}

// TestHandleAutoFinish_SecondCallIsIdempotent asserts the handler is
// safe to call repeatedly: a second finish on an already-finished
// investigation returns 200 with {"already":true} and does not
// re-publish.
func TestHandleAutoFinish_SecondCallIsIdempotent(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseFinished})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/finish", strings.NewReader(`{"reason":"done again"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoFinish(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"already":true`)
}

// TestHandleAutoRequestTakeover_PausesAndAnnotatesChat exercises the
// operator-initiated yield: an active auto-mode investigation flips to
// PhasePaused, emits an auto_mode_state envelope tagged
// TakenOverBy="operator", and publishes a synthetic user envelope with
// Origin="operator" carrying the yield reason.
func TestHandleAutoRequestTakeover_PausesAndAnnotatesChat(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/request-takeover", strings.NewReader(`{"reason":"need human"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoRequestTakeover(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)
	require.Equal(t, auto.PhasePaused, inv.Auto.Phase)
	stateSeen, noteSeen := false, false
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindAutoModeState && e.AutoMode != nil && e.AutoMode.Phase == "paused" && e.AutoMode.TakenOverBy == "operator" {
			stateSeen = true
		}
		if e.Kind == envKindUser && e.Origin == "operator" && strings.Contains(e.Text, "yielded") {
			noteSeen = true
		}
	}
	require.True(t, stateSeen, "expected auto_mode_state envelope with phase=paused and takenOverBy=operator")
	require.True(t, noteSeen, "expected synthetic operator user envelope describing the yield")
}

// TestHandleAutoRequestTakeover_RejectsWhenNotActive asserts the
// handler refuses to yield when auto mode is already paused/finished/
// aborted — i.e. there is no active operator turn to yield.
func TestHandleAutoRequestTakeover_RejectsWhenNotActive(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhasePaused})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/request-takeover", strings.NewReader(`{"reason":"x"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoRequestTakeover(w, req)
	require.Equal(t, http.StatusLocked, w.Code)
}

// TestHandleAutoRequestTakeover_RejectsMissingBearer guards the
// telemetry token gate on the MCP-callable surface.
func TestHandleAutoRequestTakeover_RejectsMissingBearer(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/request-takeover", strings.NewReader(`{"reason":"x"}`))
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoRequestTakeover(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleAutoApproveProposal_RoutesToWiki asserts kind="wiki" reaches the
// shared wiki approve helper — proven by the "invalid proposal id" 400 the
// helper emits when the id doesn't match wikiProposalIDPattern. The real push-
// to-vault path is exercised by the browser-facing handler's own tests.
func TestHandleAutoApproveProposal_RoutesToWiki(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/approve-proposal", strings.NewReader(`{"kind":"wiki","proposal_id":"not-a-real-prop-id"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoApproveProposal(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "invalid proposal id")
}

// TestHandleAutoApproveProposal_RoutesToPlaybook asserts kind="playbook"
// reaches the shared playbook approve helper. With UserPlaybooksDir unset the
// helper short-circuits to 503 "user playbooks dir is not configured" before
// touching disk, which is enough to confirm the switch dispatched.
func TestHandleAutoApproveProposal_RoutesToPlaybook(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/approve-proposal", strings.NewReader(`{"kind":"playbook","proposal_id":"prop-42ec5c16c183"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoApproveProposal(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "user playbooks dir is not configured")
}

// TestHandleAutoApproveProposal_RejectsBadKind asserts kind values other than
// "wiki" or "playbook" are rejected at the switch.
func TestHandleAutoApproveProposal_RejectsBadKind(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/approve-proposal", strings.NewReader(`{"kind":"random","proposal_id":"prop-42ec5c16c183"}`))
	req.SetPathValue("id", "inv-1")
	req.Header.Set("Authorization", "Bearer "+a.telemetryToken)
	w := httptest.NewRecorder()
	a.handleAutoApproveProposal(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "kind must be")
}

// TestHandleAutoApproveProposal_RejectsMissingBearer guards the telemetry-token
// gate on the MCP-callable surface.
func TestHandleAutoApproveProposal_RejectsMissingBearer(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/inv-1/auto/approve-proposal", strings.NewReader(`{"kind":"wiki","proposal_id":"prop-42ec5c16c183"}`))
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoApproveProposal(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleAutoTakeover_FromStarted exercises the human-initiated
// take-over: an active auto-mode investigation flips to PhasePaused and
// emits an auto_mode_state envelope tagged TakenOverBy="human". This is
// the browser-callable surface (cookie auth via the existing
// middleware), so no bearer is required on the handler itself.
func TestHandleAutoTakeover_FromStarted(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/inv-1/auto/takeover", nil)
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoTakeover(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)
	require.Equal(t, auto.PhasePaused, inv.Auto.Phase)
	stateSeen := false
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindAutoModeState && e.AutoMode != nil && e.AutoMode.Phase == "paused" && e.AutoMode.TakenOverBy == "human" {
			stateSeen = true
		}
	}
	require.True(t, stateSeen, "expected auto_mode_state envelope with phase=paused and takenOverBy=human")
}

// TestHandleAutoTakeover_RejectsWhenNotActive asserts the handler
// refuses to take over when auto mode is not currently active — there is
// no operator turn to interrupt.
func TestHandleAutoTakeover_RejectsWhenNotActive(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhasePaused})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/inv-1/auto/takeover", nil)
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoTakeover(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
}

// TestHandleAutoResume_FromPaused exercises the human-initiated resume:
// a paused investigation flips to PhaseResumed and emits an
// auto_mode_state envelope tagged phase="resumed".
func TestHandleAutoResume_FromPaused(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhasePaused, PausedAtSeq: 5})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/inv-1/auto/resume", nil)
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoResume(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	inv := a.manager.Get("inv-1")
	require.NotNil(t, inv)
	require.Equal(t, auto.PhaseResumed, inv.Auto.Phase)
	stateSeen := false
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindAutoModeState && e.AutoMode != nil && e.AutoMode.Phase == "resumed" {
			stateSeen = true
		}
	}
	require.True(t, stateSeen, "expected auto_mode_state envelope with phase=resumed")
}

// TestHandleAutoResume_RejectsIfNotPaused asserts the resume handler
// only accepts a transition from PhasePaused — calling it on a started/
// resumed/finished/aborted phase is a 409 conflict.
func TestHandleAutoResume_RejectsIfNotPaused(t *testing.T) {
	a, srv := newTestAPIWithAuto(t, "inv-1", auto.State{Enabled: true, Phase: auto.PhaseStarted})
	t.Cleanup(srv.Close)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/inv-1/auto/resume", nil)
	req.SetPathValue("id", "inv-1")
	w := httptest.NewRecorder()
	a.handleAutoResume(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
}

// TestPreflight_AutoTrue_StartsAutoOperator wires the preflight handler
// end-to-end with auto:true: the handler must accept the new field, hand
// the freshly-registered Investigation to Manager.EnableAuto (in a
// goroutine so the HTTP response doesn't wait on claude warm-up), and
// land an op-mcp.json on disk. The test injects a fake backend factory
// via apiHandlers.autoBackendFactory so no real claude subprocess is
// spawned.
func TestPreflight_AutoTrue_StartsAutoOperator(t *testing.T) {
	t.Parallel()
	sessionsRoot := t.TempDir()
	a := &apiHandlers{
		opts: Options{
			SessionsRoot:  sessionsRoot,
			MCPBinaryPath: "/fake/triagent-mcp",
		},
		manager:     NewManager(context.Background(), sessionsRoot),
		connections: connections.NewWithDir(t.TempDir()),
		preflightFn: func(_ preflight.Options) (*preflight.Result, error) {
			return &preflight.Result{MCPConfigPath: "/dev/null"}, nil
		},
		autoBackendFactory: func(_ AutoOptions) (autoBackendish, error) {
			return &fakeAutoBackend{sessionID: "fake-op-session"}, nil
		},
	}
	// Shutdown drains every Investigation's persistence store writer
	// goroutine before t.TempDir cleanup unlinks the session subdir;
	// without it the writer can race RemoveAll and leave a freshly
	// re-created events.jsonl behind ("directory not empty").
	t.Cleanup(a.manager.Shutdown)

	body := strings.NewReader(`{"inputs":{"cluster_id":{"value":"abc"}},"auto":true,"prom":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	w := httptest.NewRecorder()
	a.handlePreflight(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Wait for the EnableAuto goroutine to finish wiring the operator
	// onto the investigation. OperatorSessionID is set by
	// applyAutoState during op.Start, which runs AFTER Extract +
	// factory + autoOp assignment, so its presence guarantees the
	// goroutine has completed its synchronous setup — preventing a
	// t.TempDir cleanup race against in-flight skill-file writes.
	invs := a.manager.List()
	require.Len(t, invs, 1)
	inv := a.manager.Get(invs[0].ID)
	require.NotNil(t, inv)

	deadline := time.After(2 * time.Second)
	for {
		inv.mu.Lock()
		enabled := inv.Auto.Enabled
		sessID := inv.Auto.OperatorSessionID
		inv.mu.Unlock()
		if enabled && sessID != "" {
			require.True(t, enabled, "Auto.Enabled")
			break
		}
		select {
		case <-deadline:
			t.Fatalf("EnableAuto goroutine did not complete: Auto.Enabled=%v OperatorSessionID=%q", enabled, sessID)
		case <-time.After(10 * time.Millisecond):
		}
	}

	// writeOperatorMCPConfig must have landed an op-mcp.json under the
	// investigation's auto/ subdir.
	opPath := filepath.Join(inv.SessionDir, "auto", "op-mcp.json")
	_, err := os.Stat(opPath)
	require.NoError(t, err, "expected op-mcp.json at %s", opPath)
}

// TestWriteOperatorMCPConfig_IncludesToolPrefixEnv asserts the op-mcp.json
// env block sets TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX so the operator MCP's
// telemetry.Wrap reports tool names with the activity-panel-expected
// `mcp__triagent-agent-operator__<tool>` prefix.
func TestWriteOperatorMCPConfig_IncludesToolPrefixEnv(t *testing.T) {
	sessionDir := t.TempDir()
	a := &apiHandlers{
		opts:           Options{MCPBinaryPath: "/path/to/triagent-mcp"},
		telemetryURL:   "http://127.0.0.1:8080/api/internal/tool-events",
		telemetryToken: "tok",
	}
	inv := &Investigation{ID: "inv-1", SessionDir: sessionDir}
	path, err := a.writeOperatorMCPConfig(inv)
	require.NoError(t, err)

	buf, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(buf, &cfg))
	require.Equal(t, "mcp__triagent-agent-operator__", cfg.MCPServers["triagent-agent-operator"].Env["TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX"])
	// Sanity: the previously-set telemetry env vars are still present.
	require.Equal(t, "http://127.0.0.1:8080/api/internal/tool-events", cfg.MCPServers["triagent-agent-operator"].Env["TRIAGENT_MCP_TELEMETRY_URL"])
	require.Equal(t, "tok", cfg.MCPServers["triagent-agent-operator"].Env["TRIAGENT_MCP_TELEMETRY_TOKEN"])
	require.Equal(t, "inv-1", cfg.MCPServers["triagent-agent-operator"].Env["TRIAGENT_MCP_TRACE_ID"])
}

// TestAutoBriefing_SkipsEmptyFields covers the platform-scoped case where
// an investigation is anchored to a Slack channel rather than a cluster
// cluster — Cluster/Namespace/IncidentURL fields are empty and must not
// render as "Cluster:  (namespace )" placeholders.
func TestAutoBriefing_SkipsEmptyFields(t *testing.T) {
	inv := &Investigation{
		SlackChannelName: "controller-alerts-prod",
		Notes:            "Alert firing — what's causing this instability?",
	}
	out := autoBriefing(inv, "")
	require.Contains(t, out, "Slack: #controller-alerts-prod")
	require.Contains(t, out, "Notes: Alert firing")
	require.NotContains(t, out, "Cluster:")
	require.NotContains(t, out, "Incident:")
	require.NotContains(t, out, "namespace ")
}

// TestAutoBriefing_FullCluster covers the dense case: every field
// populated. Verifies all five anchors land and the namespace renders
// inline with the cluster id.
func TestAutoBriefing_FullCluster(t *testing.T) {
	inv := &Investigation{
		Namespace:        "abc-123-zeebe",
		SlackChannelName: "support-incidents",
		SlackChannelURL:  "https://example.slack.com/archives/C123/p1",
		IncidentURL:      "https://incident.io/incidents/42",
		Notes:            "Zeebe partitions lagging",
	}
	out := autoBriefing(inv, "abc-123")
	require.Contains(t, out, "Cluster: abc-123 (namespace abc-123-zeebe)")
	require.Contains(t, out, "Slack: #support-incidents (https://example.slack.com/archives/C123/p1)")
	require.Contains(t, out, "Incident: https://incident.io/incidents/42")
	require.Contains(t, out, "Notes: Zeebe partitions lagging")
}
