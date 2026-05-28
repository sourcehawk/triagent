package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/require"
)

func TestDraftPR_RejectsCrossRepoIssueURL(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{}}
	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/x/y/issues/3",
	})
	require.NotNil(t, res)
	require.True(t, res.IsError)
}

func TestDraftPR_RejectsMissingIssueURL(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{}}
	res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{})
	require.NotNil(t, res)
	require.True(t, res.IsError)
}

// TestDraftPR_RejectsMalformedIssueURL covers near-miss URLs that
// aren't in the github.com/<o>/<n>/issues/<n> shape.
func TestDraftPR_RejectsMalformedIssueURL(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{}}
	cases := []string{
		"https://github.com/o/n/pull/3",   // PR not issue
		"https://gitlab.com/o/n/issues/3", // wrong host
		"github.com/o/n/issues/3",         // missing scheme
		"https://github.com/o/n/issues/abc", // non-numeric id
	}
	for _, u := range cases {
		res, _, _ := s.draftPR(context.Background(), nil, draftPRIn{IssueURL: u})
		require.True(t, res.IsError, "expected IsError for %q", u)
	}
}

// TestDraftPR_HappyPath uses a real fixture repo + stub sub-agent +
// stub gh. Verifies: worktree created, sub-agent invoked, host pushes
// (mocked via stub), gh pr create called, PR URL returned, worktree
// removed.
//
// IMPORTANT: this test exercises a real `git push` against an in-test
// remote. We avoid that by stubbing the push at the subagent-runner
// seam? No — push is performed by `runGit(ctx, wt, "push", "-u",
// "origin", branch)` from inside draftPR, which would fail in test.
// Solution: pre-configure the fixture repo's "origin" to point at a
// local bare repo we set up in the test, so the push succeeds.
func TestDraftPR_HappyPath(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := initFixtureRepo(t, "o", "n")

	// Set up a local bare repo to act as origin so `git push` succeeds.
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	// The fixture repo has no remote — add origin pointing at the bare.
	runFixtureGit(t, repoDir, "remote", "add", "origin", bareDir)
	// Push initial main so the bare has a default branch.
	runFixtureGit(t, repoDir, "push", "origin", "main")

	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/77\n")}
	var subCalls int
	var commitSeq atomic.Int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			subCalls++
			// Simulate sub-agent making a commit on its branch so
			// post-run git-log non-empty check passes. Use a sequence
			// number so repeated calls (citations retry) don't hit
			// "nothing to commit".
			seq := commitSeq.Add(1)
			require.NoError(t, os.WriteFile(
				filepath.Join(repoPath, "stub.txt"),
				[]byte(fmt.Sprintf("x%d", seq)),
				0o600,
			))
			runFixtureGit(t, repoPath, "add", "stub.txt")
			runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("stub %d", seq))
			return subagent.Result{
				Summary: withCitations("Tightened the timeout in foo.go."),
			}, nil
		},
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/pull/77", out.PRURL)
	require.Equal(t, 77, out.PRNumber)
	require.True(t, strings.HasPrefix(out.BranchName, "triagent-proposal/42-"), "branch %q", out.BranchName)
	// The citations runner may invoke the adapter twice (first attempt +
	// retry) since the stub sub-agent returns prose with no citations block.
	require.GreaterOrEqual(t, subCalls, 1)

	// gh pr create was invoked with the right shape
	var foundPRCreate bool
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			foundPRCreate = true
			require.Contains(t, c, "--draft")
			require.Contains(t, c, "--head")
			require.Contains(t, c, out.BranchName)
			// PRs must carry the same `triagent-proposal` label
			// that create_github_issue stamps on issues — the
			// codefix activity panel filters / counts on it.
			var labels []string
			for i, a := range c {
				if a == "--label" && i+1 < len(c) {
					labels = append(labels, c[i+1])
				}
			}
			require.Contains(t, labels, triagentProposalLabel,
				"PR was opened without the triagent-proposal label; args=%v", c)
		}
	}
	require.True(t, foundPRCreate)

	// Worktree for THIS test was torn down.
	// Use the specific branch dir rather than a wildcard to avoid
	// matching sibling tests' worktrees during parallel execution.
	sanitised := branchToDirSegment(out.BranchName)
	wtDir := filepath.Join(s.worktreeRoot(), fmt.Sprintf("o-n-%s", sanitised))
	_, statErr := os.Stat(wtDir)
	require.True(t, os.IsNotExist(statErr), "expected worktree dir %s to be cleaned up, stat: %v", wtDir, statErr)
}

// TestDraftPR_StripsStatusMarkersFromSummaryAndPR confirms that
// <<<STATUS message="...">>> markers the sub-agent embeds in its
// prose (for telemetry narration) are removed before the prose lands
// in the PR title, body, and CodefixProposalCard summary. Without
// this, real PRs would have titles like
// `triagent-proposal: <<<STATUS message="reading issue #5449">>>`.
func TestDraftPR_StripsStatusMarkersFromSummaryAndPR(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, repoDir, "remote", "add", "origin", bareDir)
	runFixtureGit(t, repoDir, "push", "origin", "main")

	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/99\n")}
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
			// Simulate the failure mode: the sub-agent's prose includes
			// inline STATUS markers (telemetry narration that leaks).
			return subagent.Result{
				Summary: withCitations(`<<<STATUS message="reading issue #5449">>>
<<<STATUS message="fixing typo">>>
Fixed the workflow typo on README.md line 212.
<<<STATUS message="committing">>>`),
			}, nil
		},
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)

	// Output.Summary must not carry any STATUS markers.
	require.NotContains(t, out.Summary, "<<<STATUS", "Summary leaked status markers: %q", out.Summary)

	// gh pr create title + body must not carry STATUS markers either.
	var foundCall bool
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			foundCall = true
			for i, a := range c {
				if a == "--title" && i+1 < len(c) {
					require.NotContains(t, c[i+1], "<<<STATUS", "PR title leaked: %q", c[i+1])
				}
				if a == "--body" && i+1 < len(c) {
					require.NotContains(t, c[i+1], "<<<STATUS", "PR body leaked: %q", c[i+1])
				}
			}
		}
	}
	require.True(t, foundCall, "expected gh pr create invocation")
}

// TestDraftPR_UsesStructuredTitleAndBodyMarkers asserts that when
// the sub-agent emits <<<PR_TITLE>>> / <<<PR_BODY>>> markers, the
// host uses them verbatim for the PR title (with "triagent-proposal: "
// prepended) and PR body (with the trailer appended) — instead of
// falling back to the first line of conversational prose. The agent
// owns the `Fixes #N` line inside the Description; the host does not
// add it.
func TestDraftPR_UsesStructuredTitleAndBodyMarkers(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, repoDir, "remote", "add", "origin", bareDir)
	runFixtureGit(t, repoDir, "push", "origin", "main")

	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/88\n")}
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
			return subagent.Result{
				Summary: withCitations("I'll fix the typo on line 238 — small README change.\n\n" +
					"<<<PR_TITLE\nFix typo: rbase -> rebase in README\nPR_TITLE>>>\n\n" +
					"<<<PR_BODY\n## Description\nFixes #42. The README at line 238 had \"rbase\"; replaced with \"rebase\". One-char fix.\nPR_BODY>>>"),
			}, nil
		},
	}

	_, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)

	// Title must come from the marker, with `triagent-proposal: ` prepended.
	// Body must carry the agent's marker content (including its own `Fixes #N`)
	// with only the trailer appended by the host.
	var titleArg, bodyArg string
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			for i, a := range c {
				if a == "--title" && i+1 < len(c) {
					titleArg = c[i+1]
				}
				if a == "--body" && i+1 < len(c) {
					bodyArg = c[i+1]
				}
			}
		}
	}
	require.Equal(t, "triagent-proposal: Fix typo: rbase -> rebase in README", titleArg)
	require.Contains(t, bodyArg, "Fixes #42.")
	require.Contains(t, bodyArg, "The README at line 238 had \"rbase\"")
	require.Contains(t, bodyArg, "🤖 Drafted by triagent-proposal")
	// The conversational "I'll fix the typo..." line must NOT leak
	// into the title or body — that was the original bug.
	require.NotContains(t, titleArg, "I'll fix")
	require.NotContains(t, bodyArg, "I'll fix")
	// The chat-side summary (out.Summary) gets the natural prose
	// with markers stripped.
	require.NotContains(t, out.Summary, "<<<PR_TITLE")
	require.NotContains(t, out.Summary, "<<<PR_BODY")
	require.Contains(t, out.Summary, "I'll fix the typo on line 238")
}

// TestExtractPRTitle_AndBody unit-tests the marker extractors so we
// don't only catch them via the integration test.
func TestExtractPRTitle_AndBody(t *testing.T) {
	t.Parallel()
	prose := "Some intro prose.\n\n" +
		"<<<PR_TITLE\nFix typo: rbase → rebase in README\nPR_TITLE>>>\n\n" +
		"<<<PR_BODY\nLine 1\nLine 2 of body\nPR_BODY>>>\n\n" +
		"Trailing prose."
	require.Equal(t, "Fix typo: rbase → rebase in README", extractPRTitle(prose))
	require.Equal(t, "Line 1\nLine 2 of body", extractPRBody(prose))
	// Missing markers return empty (host falls back).
	require.Empty(t, extractPRTitle("no marker here"))
	require.Empty(t, extractPRBody("no marker here"))
}

// TestStripStructuredMarkers strips STATUS, PR_TITLE, and PR_BODY
// blocks and leaves clean prose behind.
func TestStripStructuredMarkers(t *testing.T) {
	t.Parallel()
	in := `<<<STATUS message="reading">>>
The summary sentence.
<<<PR_TITLE
Fix typo
PR_TITLE>>>
<<<PR_BODY
Body text.
PR_BODY>>>
<<<STATUS message="committing">>>`
	out := stripStructuredMarkers(in)
	require.Equal(t, "The summary sentence.", out)
}

// TestDraftPR_NoCommit_ErrorsAndCleansUp simulates a sub-agent that
// returned without committing. The host must NOT push or create a
// PR; it must tear down the worktree and surface an error.
func TestDraftPR_NoCommit_ErrorsAndCleansUp(t *testing.T) {
	t.Parallel()
	cacheDir, _ := initFixtureRepo(t, "o", "n")
	stubGhInst := &stubGh{response: []byte("unused")}
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, _ string, _ string, _ string, _ string) (subagent.Result, error) {
			return subagent.Result{Summary: "I gave up."}, nil
		},
	}
	res, noCommitOut, _ := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/1",
	})
	require.NotNil(t, res)
	require.True(t, res.IsError)
	// gh pr create should NOT be invoked when no commit landed
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			t.Fatalf("gh pr create was invoked despite no commit: %v", c)
		}
	}
	// Verify the specific worktree dir for this test's branch was cleaned up.
	if noCommitOut.BranchName != "" {
		sanitised := branchToDirSegment(noCommitOut.BranchName)
		wtDir := filepath.Join(s.worktreeRoot(), fmt.Sprintf("o-n-%s", sanitised))
		_, statErr := os.Stat(wtDir)
		require.True(t, os.IsNotExist(statErr), "worktree %s should be cleaned on error, stat: %v", wtDir, statErr)
	}
}

// TestDraftPR_CitationFailure_DoesNotPushOrOpenPR is the regression
// test for the double-PR bug. When the sub-agent's response can't
// produce a valid citation envelope after the runner's one corrective
// retry, the host must NOT push the branch or open a PR. The previous
// behavior pushed first, opened the PR, then surfaced the parse error
// in the response envelope alongside a populated pr_url — the walker
// read citations:null as failure and retried, opening a second PR.
func TestDraftPR_CitationFailure_DoesNotPushOrOpenPR(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, repoDir, "remote", "add", "origin", bareDir)
	runFixtureGit(t, repoDir, "push", "origin", "main")

	stubGhInst := &stubGh{response: []byte("https://github.com/o/n/pull/77\n")}
	var commitSeq atomic.Int32
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: stubGhInst,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
		runDraftPRSubAgent: func(_ context.Context, repoPath, _ string, _ string, _ string) (subagent.Result, error) {
			// Sub-agent commits but never emits a citations block.
			// Citation parse fails on the first call and again on the
			// runner's corrective retry — populates CitationsParseError.
			seq := commitSeq.Add(1)
			require.NoError(t, os.WriteFile(
				filepath.Join(repoPath, "stub.txt"),
				[]byte(fmt.Sprintf("x%d", seq)),
				0o600,
			))
			runFixtureGit(t, repoPath, "add", "stub.txt")
			runFixtureGit(t, repoPath, "commit", "-q", "-m", fmt.Sprintf("stub %d", seq))
			return subagent.Result{Summary: "fixed the thing"}, nil
		},
	}

	res, out, err := s.draftPR(context.Background(), nil, draftPRIn{
		IssueURL: "https://github.com/o/n/issues/42",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError, "expected error result when citation envelope can't be parsed")
	require.Empty(t, out.PRURL, "PR must not be opened when citation envelope is invalid")
	require.Zero(t, out.PRNumber, "PR number must not be set when citation envelope is invalid")
	require.NotEmpty(t, out.CitationsParseError, "citations parse error should be surfaced")

	// Crucially: gh pr create must NEVER be invoked on this path. The
	// branch must also not be pushed, but the more user-visible
	// invariant is "no orphaned PRs on GitHub".
	for _, c := range stubGhInst.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == "create" {
			t.Fatalf("gh pr create was invoked despite citation parse failure: %v", c)
		}
	}
}

// withCitations appends an empty citations envelope to a sub-agent's
// summary so the host's citation parser sees a valid block. Tests that
// exercise paths unrelated to citations use this to keep their stub
// returns minimal — the citations contract isn't what they're testing.
// The new TestDraftPR_CitationFailure_DoesNotPushOrOpenPR test
// deliberately does NOT use this, so the citation-fail branch is
// exercised.
func withCitations(prose string) string {
	return prose + "\n\n<<<CITATIONS\n[]\nCITATIONS>>>"
}

func runFixtureGit(t *testing.T, repoDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if repoDir != "" {
		cmd.Dir = repoDir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// stub for the unused fmt import in case of refactor
var _ = fmt.Sprintf
