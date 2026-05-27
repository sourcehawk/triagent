package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/require"
)

// reuseFixture builds a draft_pr-ready cache + bare origin + worktree
// override, mirroring recoveryFixture but exposing the cache repo dir
// so tests can pre-create worktrees to simulate "prior call left a
// worktree behind".
func reuseFixture(t *testing.T) (cacheDir, repoDir string) {
	t.Helper()
	cacheDir, repoDir = initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, repoDir, "remote", "add", "origin", bareDir)
	runFixtureGit(t, repoDir, "push", "origin", "main")
	runFixtureGit(t, repoDir, "fetch", "--prune", "--tags")
	return cacheDir, repoDir
}

// TestFindExistingWorktreeForIssue_ReturnsMatch — git worktree list
// includes a triagent-proposal/<issueNum>-* branch we created → helper
// returns the path + branch.
func TestFindExistingWorktreeForIssue_ReturnsMatch(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	branch := "triagent-proposal/42-aaaaaaaa"
	wt, err := s.createWorktree(context.Background(), repoDir, branch, "main")
	require.NoError(t, err)
	defer func() { _ = s.removeWorktree(context.Background(), repoDir, wt) }()

	gotWT, gotBranch, found := s.findExistingWorktreeForIssue(context.Background(), repoDir, 42)
	require.True(t, found, "expected to find the prior worktree")
	require.Equal(t, wt, gotWT)
	require.Equal(t, branch, gotBranch)
}

// TestFindExistingWorktreeForIssue_NoMatch — no prior worktree, helper
// returns found=false and the caller falls through to fresh creation.
func TestFindExistingWorktreeForIssue_NoMatch(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	_, _, found := s.findExistingWorktreeForIssue(context.Background(), repoDir, 999)
	require.False(t, found)
}

// TestFindExistingWorktreeForIssue_DifferentIssue_NoMatch — a worktree
// for a different issue must NOT be returned (issue numbers must match).
func TestFindExistingWorktreeForIssue_DifferentIssue_NoMatch(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	wt, err := s.createWorktree(context.Background(), repoDir, "triagent-proposal/100-aaaaaaaa", "main")
	require.NoError(t, err)
	defer func() { _ = s.removeWorktree(context.Background(), repoDir, wt) }()

	_, _, found := s.findExistingWorktreeForIssue(context.Background(), repoDir, 42)
	require.False(t, found, "issue 42 must not match a worktree branched on issue 100")
}

// TestPersistedSessionID_RoundTrip — writePersistedSessionID + read
// roundtrip preserves the value.
func TestPersistedSessionID_RoundTrip(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	require.Empty(t, readPersistedSessionID(wt), "no file yet → empty")
	writePersistedSessionID(wt, "sess-xyz-123")
	require.Equal(t, "sess-xyz-123", readPersistedSessionID(wt))
}

// TestDraftPR_ReusesExistingWorktreeAndResumesSession — the headline
// integration. Pre-create a worktree for issue 42 with a persisted
// session id; call draftPR; verify it (a) reuses the worktree (no new
// branch minted), (b) seeds resumeSessionID for the first sub-agent
// invocation, (c) force-pushes since the branch already exists.
func TestDraftPR_ReusesExistingWorktreeAndResumesSession(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("https://github.com/o/n/pull/77\n")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	// Pre-create a worktree as if a prior draft_pr left it behind, with
	// a persisted session id.
	priorBranch := "triagent-proposal/42-bbbbbbbb"
	priorWT, err := s.createWorktree(context.Background(), repoDir, priorBranch, "main")
	require.NoError(t, err)
	writePersistedSessionID(priorWT, "sess-prior-99")

	var observedResumeIDs []string
	var observedRepoPaths []string
	s.runDraftPRSubAgent = func(_ context.Context, repoPath, _, _, resumeID string) (subagent.Result, error) {
		observedResumeIDs = append(observedResumeIDs, resumeID)
		observedRepoPaths = append(observedRepoPaths, repoPath)
		// Make a unique commit per call so the citations retry's second
		// invocation doesn't trip on "nothing to commit".
		rel := fmt.Sprintf("iter-%d.txt", len(observedResumeIDs))
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
		runFixtureGit(t, repoPath, "add", rel)
		runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("iter %d", len(observedResumeIDs)))
		return subagent.Result{Summary: withCitations("iterated."), SessionID: "sess-prior-99"}, nil
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)
	require.Equal(t, priorBranch, out.BranchName,
		"reuse must keep the existing branch — not mint a new triagent-proposal/42-<random>")
	require.Equal(t, priorWT, observedRepoPaths[0],
		"sub-agent must operate in the existing worktree, not a fresh path")
	require.Equal(t, "sess-prior-99", observedResumeIDs[0],
		"first sub-agent invocation on reuse must seed resumeSessionID from the persisted file")
}

// TestDraftPR_ForcePushOnReuse — when reusing an existing branch the
// push must be force-with-lease (the branch already exists on origin
// from the prior call; a plain push would fail).
func TestDraftPR_ForcePushOnReuse(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("https://github.com/o/n/pull/78\n")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	// Pre-create worktree + push branch to the bare origin so the
	// non-force push that draft_pr does today would conflict.
	priorBranch := "triagent-proposal/43-cccccccc"
	priorWT, err := s.createWorktree(context.Background(), repoDir, priorBranch, "main")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(priorWT, "first.txt"), []byte("x"), 0o600))
	runFixtureGit(t, priorWT, "add", "first.txt")
	runFixtureGit(t, priorWT, "commit", "-q", "-m", "first")
	runFixtureGit(t, priorWT, "push", "-u", "origin", priorBranch)

	var subSeq atomic.Int32
	s.runDraftPRSubAgent = func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
		seq := subSeq.Add(1)
		rel := fmt.Sprintf("retry-%d.txt", seq)
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
		runFixtureGit(t, repoPath, "add", rel)
		runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("retry %d", seq))
		return subagent.Result{Summary: withCitations("iterated.")}, nil
	}

	_, _, err = s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/43",
	})
	require.NoError(t, err, "reuse path must force-push so the existing remote branch is updated cleanly")
}

// TestDraftPR_SkipsCreatePRWhenPRExists — when the gh stub reports
// that a PR already exists for the branch, draft_pr must NOT call
// `gh pr create` again (which would error with "PR already exists").
// Instead the existing PR url is surfaced.
func TestDraftPR_SkipsCreatePRWhenPRExists(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	stubExistingURL := "https://github.com/o/n/pull/55"
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			// gh pr list --head <branch> --json url,number → returns
			// JSON for an existing PR.
			if len(args) >= 2 && args[0] == "pr" && args[1] == "list" {
				return []byte(`[{"url":"https://github.com/o/n/pull/55","number":55}]`), nil, nil
			}
			if len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
				return nil, []byte("PR already exists"), fmt.Errorf("exit 1")
			}
			return nil, []byte("unexpected"), fmt.Errorf("unexpected: %v", args)
		},
	}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: gh,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	priorBranch := "triagent-proposal/44-dddddddd"
	priorWT, err := s.createWorktree(context.Background(), repoDir, priorBranch, "main")
	require.NoError(t, err)
	runFixtureGit(t, priorWT, "push", "-u", "origin", priorBranch)

	var subSeq atomic.Int32
	s.runDraftPRSubAgent = func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
		seq := subSeq.Add(1)
		rel := fmt.Sprintf("iter-%d.txt", seq)
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
		runFixtureGit(t, repoPath, "add", rel)
		runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("iter %d", seq))
		return subagent.Result{Summary: withCitations("iterated.")}, nil
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/44",
	})
	require.NoError(t, err)
	require.Equal(t, stubExistingURL, out.PRURL,
		"existing PR url must be surfaced when one already exists for the reused branch")
	require.Equal(t, 55, out.PRNumber)
	// Verify gh pr create was NOT called.
	for _, c := range gh.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			t.Fatalf("gh pr create was invoked despite existing PR: %v", c)
		}
	}
}

// TestDraftPR_ReuseHappyPath_PreservesWorktreeForNextIteration — when
// reusing, the happy path must NOT cleanup the worktree at the end.
// Otherwise the next draft_pr call won't find anything to reuse and
// the iteration breaks. Sweep eventually reaps if the operator never
// returns.
func TestDraftPR_ReuseHappyPath_PreservesWorktreeForNextIteration(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("https://github.com/o/n/pull/79\n")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	priorBranch := "triagent-proposal/45-eeeeeeee"
	priorWT, err := s.createWorktree(context.Background(), repoDir, priorBranch, "main")
	require.NoError(t, err)
	runFixtureGit(t, priorWT, "push", "-u", "origin", priorBranch)

	var subSeq atomic.Int32
	s.runDraftPRSubAgent = func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
		seq := subSeq.Add(1)
		rel := fmt.Sprintf("iter-%d.txt", seq)
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
		runFixtureGit(t, repoPath, "add", rel)
		runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("iter %d", seq))
		return subagent.Result{Summary: withCitations("iterated."), SessionID: "sess-iter"}, nil
	}

	_, _, err = s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/45",
	})
	require.NoError(t, err)
	_, statErr := os.Stat(priorWT)
	require.NoError(t, statErr,
		"reuse happy path must preserve worktree so the next iteration finds it")
	require.Equal(t, "sess-iter", readPersistedSessionID(priorWT),
		"the latest sub-agent session id must be persisted to the worktree for the next iteration")
}

// freshFirstCallStillCleans — confirms back-compat: the FIRST draft_pr
// call (no existing worktree to reuse) still cleans up at end of happy
// path. We don't want this commit to accidentally make every fresh
// call retain its worktree forever.
func TestDraftPR_FreshFirstCall_StillCleansHappyPath(t *testing.T) {
	t.Parallel()
	cacheDir, _ := reuseFixture(t)
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("https://github.com/o/n/pull/80\n")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
	var subSeq atomic.Int32
	s.runDraftPRSubAgent = func(_ context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
		seq := subSeq.Add(1)
		rel := fmt.Sprintf("iter-%d.txt", seq)
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("y"), 0o600))
		runFixtureGit(t, repoPath, "add", rel)
		runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("iter %d", seq))
		return subagent.Result{Summary: withCitations("fresh.")}, nil
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/46",
	})
	require.NoError(t, err)
	sanitised := branchToDirSegment(out.BranchName)
	wtDir := filepath.Join(s.worktreeRoot(), fmt.Sprintf("o-n-%s", sanitised))
	_, statErr := os.Stat(wtDir)
	require.True(t, os.IsNotExist(statErr),
		"first-call happy path must still cleanup as before — only reuse paths preserve")
	// Branch number prefix sanity check.
	require.True(t, strings.HasPrefix(out.BranchName, "triagent-proposal/46-"))
}
