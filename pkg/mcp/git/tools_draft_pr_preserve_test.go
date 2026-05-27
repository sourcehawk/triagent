package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/require"
)

// worktreeStillExists is a tiny helper for the preserve-on-bail tests
// — checks that the worktree dir for the supplied branch still exists
// under the server's worktreeRoot.
func worktreeStillExists(t *testing.T, s *Server, branch string) bool {
	t.Helper()
	if branch == "" {
		t.Fatalf("worktreeStillExists: empty branch")
	}
	sanitised := branchToDirSegment(branch)
	wtDir := filepath.Join(s.worktreeRoot(), fmt.Sprintf("%s-%s-%s", s.owner, s.name, sanitised))
	_, err := os.Stat(wtDir)
	return err == nil
}

// TestDraftPR_RecoveryTransportFailure_PreservesWorktree — when the
// recovery sub-agent's transport fails (timeout / crash), the worktree
// has real sub-agent edits that the operator may want to salvage. We
// preserve it (the sweep will reap after 1h) and include the path in
// the diagnostic so the operator can navigate to it.
func TestDraftPR_RecoveryTransportFailure_PreservesWorktree(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("unused")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "edited."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _, _, _, _ string) (subagent.Result, error) {
			return subagent.Result{}, fmt.Errorf("context deadline exceeded")
		},
	}

	res, out, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.True(t, res.IsError)
	msg := errorText(res)
	require.Contains(t, msg, "recovery", "diag should mention recovery failure")
	require.Contains(t, msg, "worktree retained at", "diag must surface the worktree path so the operator can salvage")
	require.True(t, worktreeStillExists(t, s, out.BranchName),
		"worktree must NOT be wiped — operator may want to manually inspect/salvage the sub-agent's edits")
}

// TestDraftPR_RecoveryDidNotResolve_PreservesWorktree — recovery agent
// lied about committing OR cleaning up; worktree is still dirty. Same
// rationale: the work is real, preserve so operator can salvage.
func TestDraftPR_RecoveryDidNotResolve_PreservesWorktree(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("unused")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "edited."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _, _, _, _ string) (subagent.Result, error) {
			// Lies about cleaning up — leaves worktree dirty.
			return subagent.Result{Summary: `<<<RECOVERY_DECISION
{"action":"abandoned","reason":"no good"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	res, out, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/2",
	})
	require.True(t, res.IsError)
	msg := errorText(res)
	require.Contains(t, msg, "did not resolve")
	require.Contains(t, msg, "worktree retained at",
		"diag must surface the worktree path so the operator can salvage")
	require.True(t, worktreeStillExists(t, s, out.BranchName),
		"worktree must NOT be wiped — operator may want to inspect the sub-agent's edits")
}

// TestDraftPR_AbandonedClean_StillWipes — sub-agent's recovery cleanly
// abandoned (worktree clean, decision says abandoned). Nothing to
// preserve, wipe as before.
func TestDraftPR_AbandonedClean_StillWipes(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("unused")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "edited."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
			require.NoError(t, os.Remove(filepath.Join(repoPath, "fix.txt")))
			return subagent.Result{Summary: `<<<RECOVERY_DECISION
{"action":"abandoned","reason":"upstream metric does not exist"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	res, out, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/3",
	})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "abandoned the codefix")
	require.False(t, worktreeStillExists(t, s, out.BranchName),
		"clean abandonment has nothing to preserve — worktree should be wiped")
}

// TestDraftPR_NoEditsAtAll_StillWipes — sub-agent did nothing; nothing
// to preserve, wipe as before.
func TestDraftPR_NoEditsAtAll_StillWipes(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("unused")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _, _, _, _ string) (subagent.Result, error) {
			return subagent.Result{Summary: "I gave up."}, nil
		},
	}

	res, out, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/4",
	})
	require.True(t, res.IsError)
	require.False(t, worktreeStillExists(t, s, out.BranchName),
		"no sub-agent edits = nothing to preserve; worktree should be wiped")
}
