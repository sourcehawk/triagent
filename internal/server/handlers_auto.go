package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sourcehawk/triagent/internal/auto"
)

// writeOperatorMCPConfig is the apiHandlers wrapper around
// buildOperatorMCPConfig. Kept so the preflight handler can stay
// terse and the createFromWatch closure (which doesn't have access
// to the apiHandlers struct, since it's wired earlier in server
// setup) can share the same logic via the free function.
func (a *apiHandlers) writeOperatorMCPConfig(inv *Investigation) (string, error) {
	return buildOperatorMCPConfig(inv.SessionDir, a.opts.MCPBinaryPath, a.telemetryURL, a.telemetryToken, inv.ID)
}

// buildOperatorMCPConfig writes a minimal MCP config — one server
// (agent-operator via triagent-mcp serve --kind=agent-operator) — into the
// investigation's auto-session dir and returns its absolute path. The
// operator's claude session is launched with --mcp-config <this>, and
// the env block here propagates the launcher's telemetry URL/token plus
// the investigation's trace id so the agent-operator MCP can POST back
// to /auto/send-message etc. without needing the env on the operator's
// process inherited from claude.
func buildOperatorMCPConfig(sessionDir, mcpBinaryPath, telemetryURL, telemetryToken, traceID string) (string, error) {
	autoDir := filepath.Join(sessionDir, "auto")
	if err := os.MkdirAll(autoDir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir auto dir: %w", err)
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"triagent-agent-operator": map[string]any{
				"command": mcpBinaryPath,
				"args":    []string{"serve", "--kind=agent-operator"},
				"env": map[string]string{
					"TRIAGENT_MCP_TELEMETRY_URL":         telemetryURL,
					"TRIAGENT_MCP_TELEMETRY_TOKEN":       telemetryToken,
					"TRIAGENT_MCP_TRACE_ID":              traceID,
					"TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX": "mcp__triagent-agent-operator__",
				},
			},
		},
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal op-mcp.json: %w", err)
	}
	path := filepath.Join(autoDir, "op-mcp.json")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return "", fmt.Errorf("write op-mcp.json: %w", err)
	}
	return path, nil
}

// autoBriefing composes the opening prompt the operator agent is woken
// with on EnableAuto. Concise on purpose — the operator-role SKILL.md
// (forwarded via --append-system-prompt) carries the actual playbook;
// this just grounds the agent in *this* investigation's context.
//
// clusterID is the raw value from the preflight request DTO (kept as a
// transient hint; not persisted). Only fields that are set get included.
// Cluster-scoped investigations have clusterID/Namespace; platform-scoped
// ones (alerts anchored to a Slack channel rather than a customer cluster)
// typically do not. The briefing must read cleanly in both shapes.
func autoBriefing(inv *Investigation, clusterID string) string {
	var b strings.Builder
	b.WriteString("You are the operator agent for an investigation that just started.\n\n")
	if clusterID != "" {
		if inv.Namespace != "" {
			fmt.Fprintf(&b, "Cluster: %s (namespace %s)\n", clusterID, inv.Namespace)
		} else {
			fmt.Fprintf(&b, "Cluster: %s\n", clusterID)
		}
	}
	if inv.SlackChannelName != "" {
		if inv.SlackChannelURL != "" {
			fmt.Fprintf(&b, "Slack: #%s (%s)\n", inv.SlackChannelName, inv.SlackChannelURL)
		} else {
			fmt.Fprintf(&b, "Slack: #%s\n", inv.SlackChannelName)
		}
	} else if inv.SlackChannelURL != "" {
		fmt.Fprintf(&b, "Slack: %s\n", inv.SlackChannelURL)
	}
	if inv.IncidentURL != "" {
		fmt.Fprintf(&b, "Incident: %s\n", inv.IncidentURL)
	}
	if notes := strings.TrimSpace(inv.Notes); notes != "" {
		fmt.Fprintf(&b, "Notes: %s\n", notes)
	}
	b.WriteString("\nThe investigation agent (separate session) is starting now. Watch for its first turn-end.")
	return b.String()
}

// handleAutoSendMessage receives a chat message from the operator agent
// (the auto-mode-side claude session running in triagent-mcp's agent-operator
// kind) and forwards it to the investigation agent as a follow-up prompt.
// Publishes a synthetic user envelope tagged Origin="operator" so the
// transcript distinguishes operator-agent messages from human turns.
//
// POST /api/internal/investigations/{id}/auto/send-message
// Body: {"text": string}
// Auth: Bearer telemetry token (the same shared secret triagent-mcp uses for
// tool-event posts).
//
// 202 — accepted; envelope published and follow-up dispatched.
// 400 — invalid body or empty text.
// 401 — missing or wrong bearer.
// 404 — unknown investigation.
// 409 — investigation agent is currently streaming a turn.
// 423 — auto-mode not active (paused, finished, aborted, or disabled).
func (a *apiHandlers) handleAutoSendMessage(w http.ResponseWriter, r *http.Request) {
	if a.telemetryToken == "" || !checkBearer(r, a.telemetryToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	inv.mu.Lock()
	st := inv.Auto
	streaming := inv.streaming
	inv.mu.Unlock()
	if !st.IsActive() {
		writeError(w, http.StatusLocked, "auto mode is "+string(st.Phase))
		return
	}
	if streaming {
		writeError(w, http.StatusConflict, "investigation agent is streaming")
		return
	}
	if err := inv.SendFollowUp("operator", body.Text); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleAutoFinish marks an auto-mode investigation as PhaseFinished.
// Called by the operator agent (via triagent-mcp's agent-operator kind) when
// its skill decides the investigation has reached a natural end (root
// cause located, runbook complete, etc.). The operator-side claude
// session is expected to exit shortly after.
//
// Idempotent: a second call on an already-finished investigation
// returns 200 with {"ok":true,"already":true} and does not re-publish
// the auto_mode_state envelope. This keeps the operator agent honest
// about retries without flooding the transcript on flakiness.
//
// POST /api/internal/investigations/{id}/auto/finish
// Body: {"reason": string} (optional)
// Auth: Bearer telemetry token (shared secret with triagent-mcp).
//
// 200 — finished (fresh transition or idempotent no-op).
// 401 — missing or wrong bearer.
// 404 — unknown investigation.
func (a *apiHandlers) handleAutoFinish(w http.ResponseWriter, r *http.Request) {
	if a.telemetryToken == "" || !checkBearer(r, a.telemetryToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	body.Reason = strings.TrimSpace(body.Reason)

	inv.mu.Lock()
	already := inv.Auto.Phase == auto.PhaseFinished
	if !already {
		inv.Auto.Phase = auto.PhaseFinished
	}
	op := inv.autoOp
	inv.mu.Unlock()

	if !already {
		inv.publish(EventEnvelope{
			Kind:     envKindAutoModeState,
			AutoMode: &AutoModePayload{Phase: "finished", Reason: body.Reason},
		})
		a.manager.persistAutoState(inv)
		// Sync the operator's internal phase so a subsequent
		// recordSuccess → snapshot → applyAutoState doesn't see a
		// phase change (its local state would still be PhaseStarted)
		// and re-publish auto_mode_state{started}, clobbering the
		// finished envelope we just emitted.
		if op != nil {
			op.Finish(body.Reason)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if already {
		_, _ = w.Write([]byte(`{"ok":true,"already":true}`))
	} else {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// handleAutoRequestTakeover is the operator-initiated yield: the
// operator agent (via triagent-mcp's agent-operator kind) decides it can't
// proceed safely and hands control back to the human. Flips the phase
// from active (started/resumed) to PhasePaused, snapshots PausedAtSeq
// so a later resume can compose the catch-up span, and lays down two
// envelopes: an auto_mode_state marker tagged TakenOverBy="operator"
// and a synthetic operator user envelope carrying the yield reason
// (the pink chat note the human sees on the transcript).
//
// POST /api/internal/investigations/{id}/auto/request-takeover
// Body: {"reason": string} (optional)
// Auth: Bearer telemetry token (shared secret with triagent-mcp).
//
// 200 — paused; envelopes published; metadata.json updated.
// 401 — missing or wrong bearer.
// 404 — unknown investigation.
// 423 — auto mode is not currently active (already paused/finished/aborted).
func (a *apiHandlers) handleAutoRequestTakeover(w http.ResponseWriter, r *http.Request) {
	if a.telemetryToken == "" || !checkBearer(r, a.telemetryToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	body.Reason = strings.TrimSpace(body.Reason)

	inv.mu.Lock()
	if !inv.Auto.IsActive() {
		inv.mu.Unlock()
		writeError(w, http.StatusLocked, "auto mode not active")
		return
	}
	inv.Auto.Phase = auto.PhasePaused
	// publish() increments nextSeq before stamping, so the very next
	// envelope we emit (the auto_mode_state marker below) will land on
	// seq = nextSeq + 1. Snapshot that boundary so a later resume can
	// compose the catch-up span from envelopes published while paused.
	inv.Auto.PausedAtSeq = inv.nextSeq + 1
	pausedAt := inv.Auto.PausedAtSeq
	op := inv.autoOp
	inv.mu.Unlock()

	inv.publish(EventEnvelope{
		Kind:     envKindAutoModeState,
		AutoMode: &AutoModePayload{Phase: "paused", TakenOverBy: "operator", Reason: body.Reason},
	})
	if body.Reason != "" {
		inv.publish(EventEnvelope{
			Kind:   envKindUser,
			Origin: "operator",
			Text:   "auto-mode yielded: " + body.Reason,
		})
	}
	// Keep the operator's internal phase in sync so a later /auto/resume
	// finds state.Phase == PhasePaused and op.Resume actually drives a
	// catch-up turn. applyAutoState skips re-publishing the envelope
	// because inv.Auto.Phase is already Paused (set above).
	if op != nil {
		op.Pause(pausedAt)
	}
	a.manager.persistAutoState(inv)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleAutoApproveProposal lets the operator agent approve a wiki or
// playbook draft via the triagent-agent-operator MCP. Routes by kind to the
// same underlying helpers the browser-facing Approve buttons use, so
// the side effects (PR open / playbook promote / resolution marker
// write) are identical regardless of who initiated the approval.
//
// POST /api/internal/investigations/{id}/auto/approve-proposal
// Body: {"kind": "wiki"|"playbook", "proposal_id": "<id>"}
// Auth: Bearer telemetry token.
func (a *apiHandlers) handleAutoApproveProposal(w http.ResponseWriter, r *http.Request) {
	if !checkBearer(r, a.telemetryToken) || a.telemetryToken == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	var body struct {
		Kind       string `json:"kind"`
		ProposalID string `json:"proposal_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Kind = strings.TrimSpace(body.Kind)
	body.ProposalID = strings.TrimSpace(body.ProposalID)
	if body.ProposalID == "" {
		writeError(w, http.StatusBadRequest, "proposal_id required")
		return
	}
	switch body.Kind {
	case "wiki":
		a.approveWikiByID(w, r, body.ProposalID)
	case "playbook":
		a.approvePlaybookByID(w, r, body.ProposalID)
	default:
		writeError(w, http.StatusBadRequest, `kind must be "wiki" or "playbook"`)
	}
}

// handleAutoTakeover is the human-initiated yield: the operator clicks
// "Take over" in the SessionView while auto mode is running. Mirrors
// handleAutoRequestTakeover (operator agent yields to human) but flips
// TakenOverBy to "human" on the auto_mode_state envelope and carries no
// reason — there's no synthetic operator chat note here because the
// human's next composer message will speak for itself.
//
// Browser-callable, cookie auth via the existing middleware. NOT a
// `/api/internal/...` route — no bearer token required on the handler.
//
// POST /api/investigations/{id}/auto/takeover
//
// 200 — paused; auto_mode_state envelope published; metadata.json updated.
// 404 — unknown investigation.
// 409 — auto mode is not currently active (already paused/finished/aborted).
func (a *apiHandlers) handleAutoTakeover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	inv.mu.Lock()
	if !inv.Auto.IsActive() {
		phase := inv.Auto.Phase
		inv.mu.Unlock()
		writeError(w, http.StatusConflict, "auto mode is "+string(phase))
		return
	}
	inv.Auto.Phase = auto.PhasePaused
	// Same boundary snapshot as the operator-yield path: publish()
	// increments nextSeq before stamping, so the very next envelope we
	// emit (the auto_mode_state marker below) lands on nextSeq+1.
	// Resume composes the catch-up span from envelopes >= PausedAtSeq.
	inv.Auto.PausedAtSeq = inv.nextSeq + 1
	pausedAt := inv.Auto.PausedAtSeq
	op := inv.autoOp
	inv.mu.Unlock()

	inv.publish(EventEnvelope{
		Kind:     envKindAutoModeState,
		AutoMode: &AutoModePayload{Phase: "paused", TakenOverBy: "human"},
	})
	// Sync the operator's internal phase so a follow-up /auto/resume
	// finds state.Phase == PhasePaused and op.Resume drives the
	// catch-up turn. applyAutoState skips re-publishing because
	// inv.Auto.Phase is already Paused.
	if op != nil {
		op.Pause(pausedAt)
	}
	a.manager.persistAutoState(inv)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleAutoResume is the human handing control back to the operator
// agent after a take-over. Flips PhasePaused → PhaseResumed, emits an
// auto_mode_state envelope, and notifies the manager so the real
// AutoOperator (Task 17) can compose the catch-up prompt and resume its
// claude session. Today notifyAutoResume is a stub.
//
// Browser-callable, cookie auth via the existing middleware. NOT a
// `/api/internal/...` route — no bearer token required on the handler.
//
// POST /api/investigations/{id}/auto/resume
//
// 200 — resumed; auto_mode_state envelope published; metadata.json updated.
// 404 — unknown investigation.
// 409 — auto mode is not currently paused (nothing to resume).
func (a *apiHandlers) handleAutoResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	inv.mu.Lock()
	if inv.Auto.Phase != auto.PhasePaused {
		inv.mu.Unlock()
		writeError(w, http.StatusConflict, "not paused")
		return
	}
	inv.Auto.Phase = auto.PhaseResumed
	inv.mu.Unlock()

	inv.publish(EventEnvelope{
		Kind:     envKindAutoModeState,
		AutoMode: &AutoModePayload{Phase: "resumed"},
	})
	a.manager.notifyAutoResume(inv)
	a.manager.persistAutoState(inv)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}
