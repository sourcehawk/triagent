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

// TestDraftPR_CitationsRetry_ResumesSession — the citations corrective
// retry must resume the main sub-agent's session (via SessionID/
// ResumeSessionID threading) rather than spawning a fresh Claude
// session that would lose all context (file edits, prior tool calls,
// orientation in the codebase).
func TestDraftPR_CitationsRetry_ResumesSession(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/400\n")}
	var observedResumeIDs []string
	var commitSeq atomic.Int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, resumeID string) (subagent.Result, error) {
			observedResumeIDs = append(observedResumeIDs, resumeID)
			seq := commitSeq.Add(1)
			rel := fmt.Sprintf("retry-%d.txt", seq)
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
			runFixtureGit(t, repoPath, "add", rel)
			runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("retry %d", seq))
			// The stub returns prose with no citations block on the first
			// call, triggering the citations retry. Both calls return
			// session "sess-main".
			return subagent.Result{
				Summary:   "Did the work.",
				SessionID: "sess-main",
			}, nil
		},
	}

	_, _, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(observedResumeIDs), 2,
		"citations retry must invoke the sub-agent at least twice")
	require.Empty(t, observedResumeIDs[0],
		"first call must NOT carry a resume id — it starts the session")
	require.Equal(t, "sess-main", observedResumeIDs[1],
		"second call (citations retry) must resume the session id captured from the first call")
}

// TestDraftPR_RecoveryRetry_ResumesMainSession — when the
// commit-or-cleanup recovery sub-agent fires, it must resume the main
// sub-agent's session so the recovery agent has the full context of
// what was edited and why. Without this, recovery is a cold sub-agent
// reading "your previous turn made these changes" with no actual
// memory of them.
func TestDraftPR_RecoveryRetry_ResumesMainSession(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/401\n")}
	var recoveryResumeID string
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			// Edit but DON'T commit — triggers recovery.
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "Edited.", SessionID: "sess-main-2"}, nil
		},
		runRecoverySubAgent: func(_ context.Context, repoPath, _ string, _ string, resumeID string) (subagent.Result, error) {
			recoveryResumeID = resumeID
			runFixtureGit(t, repoPath, "add", "fix.txt")
			runFixtureGit(t, repoPath, "commit", "-q", "-m", "recovered")
			return subagent.Result{Summary: `Committed.
<<<RECOVERY_DECISION
{"action":"committed","reason":"good"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	_, _, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/2",
	})
	require.NoError(t, err)
	require.Equal(t, "sess-main-2", recoveryResumeID,
		"recovery sub-agent must resume the main sub-agent's session id")
}
