package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubCodefixGh records each call and returns canned data.
// When responseFn is non-nil it overrides the static response field,
// allowing per-call dynamic responses (used by the fan-out tests).
type stubCodefixGh struct {
	calls      [][]string
	response   []byte
	stderr     []byte
	err        error
	responseFn func(args []string) []byte
}

func (s *stubCodefixGh) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, append([]string(nil), args...))
	if s.responseFn != nil {
		return s.responseFn(args), s.stderr, s.err
	}
	return s.response, s.stderr, s.err
}

func TestHandleGetCodefixProposal_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := CodefixProposalPayload{
		ProposalID:  "prop-aaaaaaaaaaaa",
		Repo:        "o/n",
		IssueNumber: 1,
		PRNumber:    2,
	}
	require.NoError(t, writeCodefixProposal(dir, p))
	a := &apiHandlers{
		opts: Options{CodefixProposalsPath: dir},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/codefix-proposals/prop-aaaaaaaaaaaa", nil)
	req.SetPathValue("id", "prop-aaaaaaaaaaaa")
	w := httptest.NewRecorder()
	a.handleGetCodefixProposal(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "prop-aaaaaaaaaaaa")
	require.Contains(t, w.Body.String(), `"repo":"o/n"`)
}

func TestHandleGetCodefixProposal_NotFound(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{
		opts: Options{CodefixProposalsPath: t.TempDir()},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/codefix-proposals/prop-missingxxxx", nil)
	req.SetPathValue("id", "prop-missingxxxx")
	w := httptest.NewRecorder()
	a.handleGetCodefixProposal(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleGetCodefixProposal_InvalidID(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/codefix-proposals/not-a-prop", nil)
	req.SetPathValue("id", "not-a-prop")
	w := httptest.NewRecorder()
	a.handleGetCodefixProposal(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleDiscardCodefixProposal_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := CodefixProposalPayload{
		ProposalID: "prop-bbbbbbbbbbbb", Repo: "o/n", PRNumber: 3, BranchName: "c1-proposal/1-x",
	}
	require.NoError(t, writeCodefixProposal(dir, p))
	stub := &stubCodefixGh{response: []byte("ok")}
	a := &apiHandlers{
		opts:      Options{CodefixProposalsPath: dir},
		codefixGh: stub,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/codefix-proposals/prop-bbbbbbbbbbbb/discard", nil)
	req.SetPathValue("id", "prop-bbbbbbbbbbbb")
	w := httptest.NewRecorder()
	a.handleDiscardCodefixProposal(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// First call: gh pr close <num> --repo --comment
	require.GreaterOrEqual(t, len(stub.calls), 2)
	require.Equal(t, "pr", stub.calls[0][0])
	require.Equal(t, "close", stub.calls[0][1])
	require.Equal(t, "3", stub.calls[0][2])
	// Second call: gh api -X DELETE repos/<repo>/git/refs/heads/<branch>
	require.Equal(t, "api", stub.calls[1][0])
	require.Contains(t, stub.calls[1], "repos/o/n/git/refs/heads/c1-proposal/1-x")
	// Discard ledger written
	_, ok, _ := readCodefixDiscardLedger(dir, "prop-bbbbbbbbbbbb")
	require.True(t, ok)
}

func TestHandleDiscardCodefixProposal_DoubleDiscardReturns409(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := CodefixProposalPayload{ProposalID: "prop-cccccccccccc", Repo: "o/n", PRNumber: 4}
	require.NoError(t, writeCodefixProposal(dir, p))
	require.NoError(t, writeCodefixDiscardLedger(dir, "prop-cccccccccccc", codefixDiscardLedger{ProposalID: "prop-cccccccccccc", DiscardedAt: "earlier"}))
	a := &apiHandlers{
		opts:      Options{CodefixProposalsPath: dir},
		codefixGh: &stubCodefixGh{err: fmt.Errorf("should not be called")},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/codefix-proposals/prop-cccccccccccc/discard", nil)
	req.SetPathValue("id", "prop-cccccccccccc")
	w := httptest.NewRecorder()
	a.handleDiscardCodefixProposal(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
}

func TestHandleDiscardCodefixProposal_NotFound(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{
		opts:      Options{CodefixProposalsPath: t.TempDir()},
		codefixGh: &stubCodefixGh{},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/codefix-proposals/prop-zzzzzzzzzzzz/discard", nil)
	req.SetPathValue("id", "prop-zzzzzzzzzzzz")
	w := httptest.NewRecorder()
	a.handleDiscardCodefixProposal(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestBackfillCodefixProposals_UsesSessionDirNotID: production session
// directories are named with a timestamp (session-YYYYMMDD-HHMMSS) while
// the investigation ID is a random hex (e.g. 9b4a5b78...). The backfill
// walker must read events.jsonl via dto.SessionDir, not by joining
// sessionsRoot + dto.ID — the latter constructs a path that never
// exists in production, so backfill silently skips every restored
// session and the repo activity panel never populates from history.
func TestBackfillCodefixProposals_UsesSessionDirNotID(t *testing.T) {
	t.Parallel()

	sessionsRoot := t.TempDir()
	proposalsDir := t.TempDir()

	// On-disk session dir uses the timestamp naming convention; the
	// investigation id baked into metadata is a different opaque hex.
	sessionDir := filepath.Join(sessionsRoot, "session-20260526-140154")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	const invID = "9b4a5b78dfd2083e974f61a127b791cf"

	require.NoError(t, writePersistedMetadata(sessionDir, persistedMetadata{
		ID:         invID,
		SessionDir: sessionDir,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}))

	// One create_github_issue tool_use + matching tool_result with the
	// payload shape the launcher persists.
	require.NoError(t, writeEventsFile(sessionDir, []EventEnvelope{
		{
			Seq:       1,
			Kind:      envKindToolUse,
			Timestamp: time.Now().UTC(),
			ToolID:    "tooluid_abc",
			ToolName:  "mcp__triagent-git-camunda-operator__create_github_issue",
			ToolInput: map[string]any{"title": "x", "body_markdown": "y"},
		},
		{
			Seq:       2,
			Kind:      envKindToolResult,
			Timestamp: time.Now().UTC(),
			ToolID:    "tooluid_abc",
			Text:      `{"proposal_id":"prop-camunda-camunda-operator-i5512","repo":"camunda/camunda-operator","number":5512,"url":"https://github.com/camunda/camunda-operator/issues/5512"}`,
		},
	}))

	mgr := NewManager(context.Background(), sessionsRoot)
	t.Cleanup(mgr.Shutdown)
	require.NoError(t, mgr.Restore())
	require.Len(t, mgr.List(), 1, "manager.Restore should have loaded the session")
	require.Equal(t, invID, mgr.List()[0].ID, "loaded DTO id must match metadata, not directory name")
	require.Equal(t, sessionDir, mgr.List()[0].SessionDir, "loaded DTO SessionDir must point at the actual on-disk dir")

	a := &apiHandlers{
		opts:    Options{CodefixProposalsPath: proposalsDir},
		manager: mgr,
	}

	a.backfillCodefixProposalsLocked(proposalsDir)

	got, err := readCodefixProposal(proposalsDir, "prop-camunda-camunda-operator-i5512")
	require.NoError(t, err, "backfill should have persisted the proposal — it failed because dir = sessionsRoot+ID instead of dto.SessionDir")
	require.Equal(t, "camunda/camunda-operator", got.Repo)
	require.Equal(t, 5512, got.IssueNumber)
	require.Equal(t, "https://github.com/camunda/camunda-operator/issues/5512", got.IssueURL)
	require.Equal(t, invID, got.InvestigationID)
}
