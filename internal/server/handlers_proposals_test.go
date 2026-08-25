package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProposalResolutionRoundTrip writes a marker via the helper and
// reads it back to ensure the on-disk shape and the missing-file
// branch behave the way handleGetProposal depends on.
func TestProposalResolutionRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Missing marker → ok=false, no error.
	_, ok, err := readProposalResolution(dir, "prop-aaaaaaaaaaaa")
	require.NoError(t, err, "expected no error for missing marker")
	require.False(t, ok, "expected ok=false for missing marker")

	// Write an approved marker; verify the parent dir was created.
	approved := proposalResolution{
		Outcome:   "approved",
		ID:        "broker-crashloop",
		Type:      "investigation",
		Version:   "v3",
		Activated: true,
		At:        "2026-05-07T10:00:00Z",
	}
	require.NoError(t, writeProposalResolution(dir, "prop-aaaaaaaaaaaa", approved), "write approved")
	_, err = os.Stat(filepath.Join(dir, "proposals", ".resolved", "prop-aaaaaaaaaaaa.json"))
	require.NoError(t, err, "marker file not created")
	got, ok, err := readProposalResolution(dir, "prop-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.True(t, ok, "expected ok=true for written marker")
	assert.Equal(t, approved, got, "round-trip mismatch")

	// Declined marker drops the playbook-specific fields.
	declined := proposalResolution{
		Outcome: "declined",
		At:      "2026-05-07T10:05:00Z",
	}
	require.NoError(t, writeProposalResolution(dir, "prop-bbbbbbbbbbbb", declined), "write declined")
	got, ok, err = readProposalResolution(dir, "prop-bbbbbbbbbbbb")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "declined", got.Outcome)
	assert.Empty(t, got.ID)

	// Declined marker can carry an operator note that survives the round-trip.
	// list_proposals surfaces this to the dispatched sub-agent so it knows
	// why the operator pushed back when the agent re-walks the meta-playbook.
	declinedWithNote := proposalResolution{
		Outcome: "declined",
		At:      "2026-05-07T10:10:00Z",
		Note:    "split into two entries — one per cluster",
	}
	require.NoError(t, writeProposalResolution(dir, "prop-dddddddddddd", declinedWithNote), "write declined-with-note")
	got, ok, err = readProposalResolution(dir, "prop-dddddddddddd")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "split into two entries — one per cluster", got.Note,
		"decline note must round-trip so sub-agents can read it later")
}

// TestHandleDeclineProposal_PersistsNoteFromBody covers the new
// optional { note } body the decline endpoint accepts. The frontend
// posts the operator's pushback into this field; the launcher stores
// it in the resolution marker so dispatched sub-agents (via
// list_proposals) read it on the next walk and don't re-submit the
// same shape.
func TestHandleDeclineProposal_PersistsNoteFromBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mcpBin := writeStubMCPBinary(t)
	a := &apiHandlers{opts: Options{UserPlaybooksDir: dir, MCPBinaryPath: mcpBin}}

	body := strings.NewReader(`{"note":"split into two entries — one per cluster"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/playbook-proposals/prop-aaaaaaaaaaaa/decline", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "prop-aaaaaaaaaaaa")
	rec := httptest.NewRecorder()
	a.handleDeclineProposal(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, "body %s", rec.Body.String())

	got, ok, err := readProposalResolution(dir, "prop-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.True(t, ok, "decline endpoint must still write the resolution marker")
	assert.Equal(t, "declined", got.Outcome)
	assert.Equal(t, "split into two entries — one per cluster", got.Note,
		"note from POST body must be persisted to the .resolved marker")
}

// TestHandleDeclineProposal_NoBodyStillWorks locks in that the
// decline endpoint stays compatible with callers that don't send a
// body (the prior contract). Body parse failure must not block the
// decline.
func TestHandleDeclineProposal_NoBodyStillWorks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mcpBin := writeStubMCPBinary(t)
	a := &apiHandlers{opts: Options{UserPlaybooksDir: dir, MCPBinaryPath: mcpBin}}

	req := httptest.NewRequest(http.MethodPost, "/api/playbook-proposals/prop-eeeeeeeeeeee/decline", nil)
	req.SetPathValue("id", "prop-eeeeeeeeeeee")
	rec := httptest.NewRecorder()
	a.handleDeclineProposal(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, "body %s", rec.Body.String())

	got, ok, err := readProposalResolution(dir, "prop-eeeeeeeeeeee")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "declined", got.Outcome)
	assert.Empty(t, got.Note, "no body → no note")
}

// writeStubMCPBinary writes a tiny shell script that exits 0 so the
// decline handler's deleteProposalViaSubprocess call succeeds without
// needing a real triagent-mcp binary. The stub is invoked as
// `<bin> proposal-delete --dir=... <id>` but ignores its args.
func writeStubMCPBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "triagent-mcp")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return path
}

// TestHandleGetProposal_ResolvesFromLedger verifies that when the
// draft is gone but a resolution marker exists, the handler returns
// 200 with the outcome — the chat-side card depends on this to stop
// rendering Approve/Decline buttons after a page reload.
func TestHandleGetProposal_ResolvesFromLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{opts: Options{UserPlaybooksDir: dir}}

	// Seed an approved marker.
	require.NoError(t, writeProposalResolution(dir, "prop-aaaaaaaaaaaa", proposalResolution{
		Outcome:   "approved",
		ID:        "broker-crashloop",
		Type:      "investigation",
		Version:   "v3",
		Activated: true,
		At:        "2026-05-07T10:00:00Z",
	}), "seed marker")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-proposals/prop-aaaaaaaaaaaa", nil)
	req.SetPathValue("id", "prop-aaaaaaaaaaaa")
	a.handleGetProposal(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "approved", body["status"])
	assert.Equal(t, "v3", body["version"])
	assert.Equal(t, true, body["activated"])
}

// TestHandleGetProposal_DeclinedFromLedger covers the declined branch
// which drops the playbook-specific fields.
func TestHandleGetProposal_DeclinedFromLedger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{opts: Options{UserPlaybooksDir: dir}}

	require.NoError(t, writeProposalResolution(dir, "prop-cccccccccccc", proposalResolution{
		Outcome: "declined",
		At:      "2026-05-07T10:05:00Z",
	}), "seed marker")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-proposals/prop-cccccccccccc", nil)
	req.SetPathValue("id", "prop-cccccccccccc")
	a.handleGetProposal(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "declined", body["status"])
	_, present := body["version"]
	assert.False(t, present, "declined response should not carry a version field; got %v", body["version"])
}

// TestHandleGetProposal_404WhenNeitherExists confirms we still 404
// when there's no draft and no marker — the frontend treats this as
// "resolved-unknown" which still hides the action buttons.
func TestHandleGetProposal_404WhenNeitherExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "proposals"), 0o755), "mkdir proposals")
	a := &apiHandlers{opts: Options{UserPlaybooksDir: dir}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-proposals/prop-dddddddddddd", nil)
	req.SetPathValue("id", "prop-dddddddddddd")
	a.handleGetProposal(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body %s", rec.Body.String())
}

// TestHandleGetProposal_PendingHasStatusField makes sure the existing
// pending response now carries a status field so the frontend can
// rely on it instead of inferring from missing fields.
//
// The strategies-side WriteProposalDraft files drafts under a per-type
// subdirectory (proposals/<type>/<id>__<proposalID>.yaml), so the
// reader has to walk type subdirs — not just the proposals root — to
// find a fresh draft. Seed accordingly.
func TestHandleGetProposal_PendingHasStatusField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	typeDir := filepath.Join(dir, "proposals", "investigation")
	require.NoError(t, os.MkdirAll(typeDir, 0o755), "mkdir")
	draft := filepath.Join(typeDir, "broker-crashloop__prop-eeeeeeeeeeee.yaml")
	require.NoError(t, os.WriteFile(draft, []byte("id: broker-crashloop\n"), 0o644), "seed draft")

	a := &apiHandlers{opts: Options{UserPlaybooksDir: dir}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-proposals/prop-eeeeeeeeeeee", nil)
	req.SetPathValue("id", "prop-eeeeeeeeeeee")
	a.handleGetProposal(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "pending", body["status"])
	assert.Contains(t, body["new_yaml"].(string), "broker-crashloop", "new_yaml missing draft body")
}

// The chat card diffs against this endpoint's base_yaml (the tool
// result no longer inlines it), so the base must mirror the strategies
// MCP's loaded set: a system-tier playbook with no user override is
// still a real base, and it is rendered canonically so the diff shows
// semantic deltas rather than the on-disk file's formatting.
func TestHandleGetProposal_BaseComesFromLoadedSetRenderedCanonically(t *testing.T) {
	t.Parallel()
	userDir := t.TempDir()
	systemDir := t.TempDir()
	const pb = "id: broker-crashloop\nschema_version: 1\nsymptom:    'broker restarts'\nentrypoint: a\nnodes:\n  a:\n    description: a\n    terminal_advice: done\n"
	require.NoError(t, os.MkdirAll(filepath.Join(systemDir, "investigation"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(systemDir, "investigation", "broker-crashloop.yaml"), []byte(pb), 0o644))
	typeDir := filepath.Join(userDir, "proposals", "investigation")
	require.NoError(t, os.MkdirAll(typeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(typeDir, "broker-crashloop__prop-ffffffffffff.yaml"), []byte(pb), 0o644))

	a := &apiHandlers{opts: Options{UserPlaybooksDir: userDir, SystemPlaybooksDir: systemDir}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-proposals/prop-ffffffffffff", nil)
	req.SetPathValue("id", "prop-ffffffffffff")
	a.handleGetProposal(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	base, _ := body["base_yaml"].(string)
	assert.Contains(t, base, "broker restarts", "system-tier playbook must serve as the diff base")
	assert.NotContains(t, base, "symptom:    ", "base must be canonically rendered, not the raw file")
}
