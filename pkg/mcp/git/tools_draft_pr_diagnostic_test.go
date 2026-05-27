package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/require"
)

// TestDraftPR_BailDiagnostic_IncludesSubAgentSummary — when the
// sub-agent terminates clean without committing, the bail string must
// include the sub-agent's last prose so the operator can see what it
// actually said. Today's failure mode: the operator sees only "the
// sub-agent terminated without making any file changes — no PR was
// opened" and has no signal about why (e.g. the sub-agent may have
// emitted "No code changes in this turn — re-emitting prior status
// response" which is the diagnostic).
func TestDraftPR_BailDiagnostic_IncludesSubAgentSummary(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			return subagent.Result{Summary: "I read the issue and decided this is too risky to attempt without further input on the metric availability."}, nil
		},
	}
	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.True(t, res.IsError)
	msg := errorText(res)
	require.Contains(t, msg, "too risky to attempt",
		"bail string must include sub-agent's last prose so the operator sees the diagnosis")
}

// TestDraftPR_BailDiagnostic_IncludesPRMarkers — even when the sub-agent
// emits PR_TITLE / PR_BODY but doesn't commit, those markers carry useful
// signal (e.g. PR_BODY="No code changes in this turn"). Surface them in
// the bail diagnostic.
func TestDraftPR_BailDiagnostic_IncludesPRMarkers(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			return subagent.Result{Summary: "Confused.\n\n<<<PR_TITLE\nchore: re-emit status\nPR_TITLE>>>\n\n<<<PR_BODY\nNo code changes in this turn — re-emitting prior status response with a valid citations block as requested.\nPR_BODY>>>"}, nil
		},
	}
	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.True(t, res.IsError)
	msg := errorText(res)
	require.Contains(t, msg, "No code changes in this turn",
		"bail string must include sub-agent's PR_BODY when present (most informative signal)")
	require.Contains(t, msg, "chore: re-emit status",
		"bail string must include sub-agent's PR_TITLE when present")
}

// TestDraftPR_BailDiagnostic_IncludesCitationsParseError — when citation
// validation soft-fails AND the sub-agent didn't commit, surface the
// citation error too. Helps the operator distinguish "agent gave up" from
// "agent's citations were rejected and the retry compounded the
// confusion".
func TestDraftPR_BailDiagnostic_IncludesCitationsParseError(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			// No citations block at all → citations runner soft-fails after
			// retry; CitationsParseError is populated.
			return subagent.Result{Summary: "I gave up."}, nil
		},
	}
	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.True(t, res.IsError)
	msg := errorText(res)
	require.Contains(t, msg, "citations",
		"bail must mention citations when the run had a CitationsParseError")
}

// TestParseNeedsClarification_HappyPath unit-tests the marker parser.
func TestParseNeedsClarification_HappyPath(t *testing.T) {
	t.Parallel()
	prose := `Some prose.

<<<NEEDS_CLARIFICATION
{"missing":["controller_runtime_reconcile_total result label"],"rationale":"cannot determine which sub-rules to add without verifying the metric's available labels"}
NEEDS_CLARIFICATION>>>

Trailing.`
	dec, ok := parseNeedsClarification(prose)
	require.True(t, ok)
	require.Equal(t, []string{"controller_runtime_reconcile_total result label"}, dec.Missing)
	require.Equal(t, "cannot determine which sub-rules to add without verifying the metric's available labels", dec.Rationale)
}

// TestParseNeedsClarification_Missing returns ok=false on no marker.
func TestParseNeedsClarification_Missing(t *testing.T) {
	t.Parallel()
	_, ok := parseNeedsClarification("plain prose")
	require.False(t, ok)
}

// TestParseNeedsClarification_MalformedJSON returns ok=false.
func TestParseNeedsClarification_MalformedJSON(t *testing.T) {
	t.Parallel()
	prose := `<<<NEEDS_CLARIFICATION
{"missing": ["x"} -- not valid json
NEEDS_CLARIFICATION>>>`
	_, ok := parseNeedsClarification(prose)
	require.False(t, ok)
}

// TestDraftPR_NeedsClarification_DistinctBail — when the sub-agent
// emits a NEEDS_CLARIFICATION marker (and clean worktree, no commits),
// the host bails with a distinct "needs clarification" message that
// surfaces what the sub-agent says it needs. This is the "I'm blocked,
// please give me X and Y" path — better operator UX than the generic
// "terminated without making changes" bail.
func TestDraftPR_NeedsClarification_DistinctBail(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			return subagent.Result{Summary: `I cannot proceed without verifying the metric's labels.

<<<NEEDS_CLARIFICATION
{"missing":["controller_runtime_reconcile_total result label values"],"rationale":"the proposed sub-rule keys off result=conflict; without verification I'd land an alert that never fires"}
NEEDS_CLARIFICATION>>>`}, nil
		},
	}
	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.True(t, res.IsError)
	msg := errorText(res)
	require.Contains(t, msg, "needs clarification",
		"bail must label this as a clarification request, not generic 'no changes'")
	require.Contains(t, msg, "controller_runtime_reconcile_total result label values",
		"bail must list what the sub-agent says it needs")
	require.Contains(t, msg, "the proposed sub-rule keys off result=conflict",
		"bail must include the sub-agent's rationale so the operator knows why")
	require.NotContains(t, msg, "terminated without making any file changes",
		"the clarification path must NOT use the generic bail string")
}

// TestDraftPR_NeedsClarification_IgnoredIfWorkCommitted — if the
// sub-agent emits NEEDS_CLARIFICATION but ALSO committed work, treat
// it as a normal commit-and-PR run; the marker becomes informational
// (carried in the PR body or the prose, not a bail).
func TestDraftPR_NeedsClarification_IgnoredIfWorkCommitted(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/300\n")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func() func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			var seq atomic.Int32
			return func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
				n := seq.Add(1)
				// Each invocation writes a unique file so the citations
				// retry (which re-fires the stub) doesn't trip on
				// "nothing to commit" with a duplicate filename.
				rel := fmt.Sprintf("partial-%d.txt", n)
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
				runFixtureGit(t, repoPath, "add", rel)
				runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("partial work %d", n))
				return subagent.Result{Summary: withCitations(`Partial.

<<<NEEDS_CLARIFICATION
{"missing":["operator-side metric tuning data"],"rationale":"landed the catch-all rule; the conflict-rate sub-rule still needs threshold validation before adding"}
NEEDS_CLARIFICATION>>>`)}, nil
			}
		}(),
	}
	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/2",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/pull/300", out.PRURL,
		"committed work proceeds to PR even when sub-agent also asks for clarification")
}
