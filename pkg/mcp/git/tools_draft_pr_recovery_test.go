package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// recoveryFixture sets up a happy-path cache + bare origin where the
// sub-agent's worktree can push and gh pr create can be stubbed. Mirrors
// the existing draft_pr happy-path fixture but factored for reuse across
// recovery scenarios.
func recoveryFixture(t *testing.T) (cacheDir string) {
	t.Helper()
	cacheDir, repoDir := initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, repoDir, "remote", "add", "origin", bareDir)
	runFixtureGit(t, repoDir, "push", "origin", "main")
	// Re-fetch the cache so origin/main exists locally.
	runFixtureGit(t, repoDir, "fetch", "--prune", "--tags")
	return cacheDir
}

// TestDraftPR_Recovery_ForgotCommit_RecoversAndOpensPR — the main
// sub-agent leaves the worktree dirty with no commits. Recovery agent
// runs `git add` + `git commit` on the staged files; host then pushes
// and opens the PR.
func TestDraftPR_Recovery_ForgotCommit_RecoversAndOpensPR(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/200\n")}
	var recoveryCalled int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			// Edit a file but DO NOT commit — the failure mode.
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: withCitations("I made the change.")}, nil
		},
		runRecoverySubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			atomic.AddInt32(&recoveryCalled, 1)
			// Recovery agent stages + commits.
			runFixtureGit(t, repoPath, "add", "fix.txt")
			runFixtureGit(t, repoPath, "commit", "-q", "-m", "stub recovery commit")
			return subagent.Result{Summary: `Recovery committed the staged work.
<<<RECOVERY_DECISION
{"action":"committed","reason":"changes look good"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/pull/200", out.PRURL,
		"recovery committed → PR opens")
	require.Equal(t, int32(1), atomic.LoadInt32(&recoveryCalled),
		"recovery sub-agent must have been invoked exactly once")
}

// TestDraftPR_Recovery_IntentionalAbandon_BailsWithReason — the
// sub-agent edits, decides the approach is wrong, cleans up, and
// emits an abandoned decision. Host bails with the reason surfaced.
func TestDraftPR_Recovery_IntentionalAbandon_BailsWithReason(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "I tried something."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			// Recovery agent reverts.
			require.NoError(t, os.Remove(filepath.Join(repoPath, "fix.txt")))
			return subagent.Result{Summary: `Cleaned up the worktree.
<<<RECOVERY_DECISION
{"action":"abandoned","reason":"upstream metric does not exist yet"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/2",
	})
	require.NotNil(t, res)
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "abandoned the codefix")
	require.Contains(t, errorText(res), "upstream metric does not exist yet")
	// gh pr create must NOT have been called.
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			t.Fatalf("gh pr create called on abandonment: %v", c)
		}
	}
}

// TestDraftPR_Recovery_ClaimedCommittedButDidnt_BailsAsUnresolved —
// agent says it committed, but didn't actually run `git commit`. The
// trust-but-verify check catches the mismatch and bails.
func TestDraftPR_Recovery_ClaimedCommittedButDidnt_BailsAsUnresolved(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "I made the change."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			// Lies about committing — doesn't actually run git commit.
			return subagent.Result{Summary: `<<<RECOVERY_DECISION
{"action":"committed","reason":"applied as-is"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/3",
	})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "did not resolve")
}

// TestDraftPR_Recovery_ClaimedAbandonedButDidnt_BailsAsUnresolved —
// agent says it abandoned, but left the worktree dirty. Caught by
// the post-state probe.
func TestDraftPR_Recovery_ClaimedAbandonedButDidnt_BailsAsUnresolved(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "Tried."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			// Lies about cleaning up.
			return subagent.Result{Summary: `<<<RECOVERY_DECISION
{"action":"abandoned","reason":"no good"}
RECOVERY_DECISION>>>`}, nil
		},
	}

	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/4",
	})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "did not resolve")
	require.Contains(t, errorText(res), "still dirty")
}

// TestDraftPR_Recovery_TransportFailure_Bails — recovery sub-agent
// itself fails (timeout / crash). Host bails with diagnostic.
func TestDraftPR_Recovery_TransportFailure_Bails(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "fix.txt"), []byte("y"), 0o600))
			return subagent.Result{Summary: "Tried."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			return subagent.Result{}, fmt.Errorf("context deadline exceeded")
		},
	}

	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/5",
	})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "recovery")
	require.Contains(t, errorText(res), "context deadline exceeded")
}

// TestDraftPR_Recovery_NoEditsAtAll_RecoveryNotInvoked — the sub-agent
// emitted text but never touched the filesystem. There's nothing for
// recovery to recover, so the host bails directly without invoking it.
func TestDraftPR_Recovery_NoEditsAtAll_RecoveryNotInvoked(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("unused")}
	var recoveryCalled int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			return subagent.Result{Summary: "I gave up."}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			atomic.AddInt32(&recoveryCalled, 1)
			return subagent.Result{}, nil
		},
	}

	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/6",
	})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "without making any file changes")
	require.Equal(t, int32(0), atomic.LoadInt32(&recoveryCalled),
		"recovery must not fire when worktree is clean and log is empty")
}

// TestDraftPR_Recovery_CleanCommit_RecoveryNotInvoked — sub-agent
// committed on its own. Recovery must not fire even when (as is normal)
// the citations soft-fail produces a CitationsParseError.
func TestDraftPR_Recovery_CleanCommit_RecoveryNotInvoked(t *testing.T) {
	t.Parallel()
	cacheDir := recoveryFixture(t)
	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/201\n")}
	var recoveryCalled int32
	var commitSeq atomic.Int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			seq := commitSeq.Add(1)
			require.NoError(t, os.WriteFile(
				filepath.Join(repoPath, "stub.txt"),
				[]byte(fmt.Sprintf("x%d", seq)),
				0o600,
			))
			runFixtureGit(t, repoPath, "add", "stub.txt")
			runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("stub %d", seq))
			return subagent.Result{Summary: withCitations("Did it.")}, nil
		},
		runRecoverySubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			atomic.AddInt32(&recoveryCalled, 1)
			return subagent.Result{}, nil
		},
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/7",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/pull/201", out.PRURL)
	require.Equal(t, int32(0), atomic.LoadInt32(&recoveryCalled),
		"recovery must not fire when sub-agent committed cleanly")
}

// TestParseRecoveryDecision_HappyPath unit-tests the marker parser.
func TestParseRecoveryDecision_HappyPath(t *testing.T) {
	t.Parallel()
	prose := `Some prose.

<<<RECOVERY_DECISION
{"action":"committed","reason":"applied as-is"}
RECOVERY_DECISION>>>

Trailing prose.`
	dec, ok := parseRecoveryDecision(prose)
	require.True(t, ok)
	require.Equal(t, "committed", dec.Action)
	require.Equal(t, "applied as-is", dec.Reason)
}

// TestParseRecoveryDecision_Missing returns ok=false on no marker —
// host can branch on the absence rather than guessing intent.
func TestParseRecoveryDecision_Missing(t *testing.T) {
	t.Parallel()
	_, ok := parseRecoveryDecision("just prose, no marker")
	require.False(t, ok)
}

// TestParseRecoveryDecision_MalformedJSON returns ok=false on JSON parse
// failure. The host treats this the same as a missing marker — falls
// through to "did not resolve worktree" bail.
func TestParseRecoveryDecision_MalformedJSON(t *testing.T) {
	t.Parallel()
	prose := `<<<RECOVERY_DECISION
{"action":"committed" — not valid json}
RECOVERY_DECISION>>>`
	_, ok := parseRecoveryDecision(prose)
	require.False(t, ok)
}

// errorText returns the first TextContent message from an MCP tool
// result so tests can assert on the error string.
func errorText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return fmt.Sprintf("%+v", res.Content[0])
}
