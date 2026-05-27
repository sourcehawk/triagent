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

// staleCacheFixture sets up:
//   - upstream bare repo with TWO commits on main (C1, C2)
//   - cache clone whose local `main` is parked at C1 (the stale state)
//   - origin/main in the cache pointing at C2 (the post-fetch tip)
//
// Mirrors the production failure mode: the cache was first cloned a
// while ago at C1; a later fetch updated remote-tracking refs to C2;
// local `main` was never fast-forwarded.
func staleCacheFixture(t *testing.T) (cacheDir, repoDir, bareDir string) {
	t.Helper()
	bareDir = filepath.Join(t.TempDir(), "upstream.git")
	runFixtureGit(t, "", "init", "--bare", "--initial-branch=main", bareDir)

	// Author commits + pushes C1.
	authorDir := filepath.Join(t.TempDir(), "author")
	runFixtureGit(t, "", "init", "--initial-branch=main", authorDir)
	runFixtureGit(t, authorDir, "config", "commit.gpgsign", "false")
	runFixtureGit(t, authorDir, "remote", "add", "origin", bareDir)
	require.NoError(t, os.WriteFile(filepath.Join(authorDir, "README.md"), []byte("c1\n"), 0o600))
	runFixtureGit(t, authorDir, "add", "README.md")
	runFixtureGit(t, authorDir, "commit", "-q", "-m", "commit-1")
	runFixtureGit(t, authorDir, "push", "-u", "origin", "main")

	// Cache clone — captures local main at C1.
	cacheDir = t.TempDir()
	repoDir = filepath.Join(cacheDir, "o", "n")
	runFixtureGit(t, "", "clone", "--filter=blob:none", bareDir, repoDir)
	runFixtureGit(t, repoDir, "config", "commit.gpgsign", "false")

	// Author commits + pushes C2.
	require.NoError(t, os.WriteFile(filepath.Join(authorDir, "README.md"), []byte("c2\n"), 0o600))
	runFixtureGit(t, authorDir, "add", "README.md")
	runFixtureGit(t, authorDir, "commit", "-q", "-m", "c2")
	runFixtureGit(t, authorDir, "push", "origin", "main")

	// Cache fetches — origin/main moves to C2, local main stays at C1.
	runFixtureGit(t, repoDir, "fetch", "--prune", "--tags")
	return cacheDir, repoDir, bareDir
}

// TestDraftPR_BasesWorktreeOnOriginNotStaleLocal verifies the worktree
// for the new branch is forked off origin/<baseRef>, not the
// potentially-stale local ref. The post-run commit-list check must use
// the same resolved ref so only the sub-agent's commits count.
func TestDraftPR_BasesWorktreeOnOriginNotStaleLocal(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir, _ := staleCacheFixture(t)

	originSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "origin/main"))
	localSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "main"))
	require.NotEqual(t, originSHA, localSHA, "fixture: origin/main must be ahead of local main")

	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/100\n")}
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
			return subagent.Result{Summary: withCitations("Did a thing.")}, nil
		},
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/pull/100", out.PRURL)
	require.Equal(t, "origin/main", out.ResolvedBaseRef,
		"draft_pr output must surface the resolved ref so the agent can see what it actually ran against")

	// gh pr create's --base argument must still be the bare name —
	// GitHub doesn't know about origin/* refs.
	var foundBaseArg bool
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			for i, a := range c {
				if a == "--base" && i+1 < len(c) {
					require.Equal(t, "main", c[i+1],
						"gh pr create --base must be the bare remote branch name, not a local rev-parse output")
					foundBaseArg = true
				}
			}
		}
	}
	require.True(t, foundBaseArg, "expected gh pr create --base invocation")
}

// TestDraftPR_WorktreeForksFromOriginMainSha asserts the worktree
// committed against (when the sub-agent makes its commit) sits on top
// of origin/main, not local main. We capture the worktree's HEAD
// parent SHA during the sub-agent's turn — when it's still alive —
// and compare against the cache's origin/main and local main.
func TestDraftPR_WorktreeForksFromOriginMainSha(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir, _ := staleCacheFixture(t)
	originSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "origin/main"))
	localSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "main"))
	require.NotEqual(t, originSHA, localSHA, "fixture: origin/main and local main must differ")

	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/101\n")}
	var observedParentSHA string
	var subSeq atomic.Int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			seq := subSeq.Add(1)
			if seq == 1 {
				// First invocation only: capture the worktree's parent
				// commit BEFORE the sub-agent's own commit lands. That
				// parent is whatever ref `git worktree add` based the
				// branch on — the thing this test asserts.
				observedParentSHA = strings.TrimSpace(mustGitOutput(t, repoPath, "rev-parse", "HEAD"))
			}
			// Idempotent: each call writes a unique file so the citations
			// retry doesn't trip on "nothing to commit".
			rel := fmt.Sprintf("stub-%d.txt", seq)
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, rel), []byte("x"), 0o600))
			runFixtureGit(t, repoPath, "add", rel)
			runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("stub %d", seq))
			return subagent.Result{Summary: withCitations("stub commit.")}, nil
		},
	}

	_, _, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)
	require.Equal(t, originSHA, observedParentSHA,
		"worktree must fork from origin/main (not stale local main)")
	require.NotEqual(t, localSHA, observedParentSHA,
		"worktree must NOT fork from stale local main")
}

func mustGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(context.Background(), dir, args...)
	require.NoError(t, err, "git %v", args)
	return out
}
