package git

import (
	"context"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/require"
)

// TestCommitSummary_ResolvesBareDefaultBranchToOrigin verifies that a
// bare branch name in the input gets rewritten to origin/<name> before
// `git rev-parse` runs — so the SHA returned reflects the freshly
// fetched tip, not the cache's stale local branch.
func TestCommitSummary_ResolvesBareDefaultBranchToOrigin(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir, _ := staleCacheFixture(t)
	originSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "origin/main"))
	localSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "main"))
	require.NotEqual(t, originSHA, localSHA, "fixture: origin/main must be ahead of local main")

	s := &Server{owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")}}
	_, out, err := s.commitSummary(context.Background(), nil, commitSummaryIn{Ref: "main"})
	require.NoError(t, err)
	require.Equal(t, originSHA, out.SHA,
		"commit_summary must read origin/main's SHA, not stale local main's")
	require.Equal(t, "main", out.Ref, "original input preserved on output for the agent")
	require.Equal(t, "origin/main", out.ResolvedRef,
		"resolved ref surfaced so the agent can see when its 'main' was rewritten")
}

// TestCommitSummary_PassesTagsThrough verifies tags don't get an
// origin/ prefix (no remote-tracking ref for a tag name).
func TestCommitSummary_PassesTagsThrough(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "o", "n")
	_, out, err := s.commitSummary(context.Background(), nil, commitSummaryIn{Ref: "v0.2.0"})
	require.NoError(t, err)
	require.Equal(t, "v0.2.0", out.ResolvedRef, "tag passes through unchanged")
}

// TestDiffSummary_ResolvesBothEnds verifies from/to are both rewritten
// to origin/<name> when they're bare branch names.
func TestDiffSummary_ResolvesBothEnds(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir, _ := staleCacheFixture(t)
	originSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "origin/main"))
	require.NotEqual(t, "", originSHA)

	s := &Server{owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")}}
	_, out, err := s.diffSummary(context.Background(), nil, diffSummaryIn{
		From: "main", // bare — must resolve to origin/main
		To:   "main", // same
	})
	require.NoError(t, err)
	require.Equal(t, "origin/main", out.ResolvedFrom)
	require.Equal(t, "origin/main", out.ResolvedTo)
	require.Equal(t, "main", out.From, "original input preserved")
	require.Equal(t, "main", out.To, "original input preserved")
}

// TestAnalyzeChange_ResolvesRefBeforeSubAgent verifies the resolved
// ref reaches the sub-agent's prompt — so when the agent runs
// `git show <ref>` it operates on the fresh tip, not a stale local
// branch.
func TestAnalyzeChange_ResolvesRefBeforeSubAgent(t *testing.T) {
	t.Parallel()
	cacheDir, _, _ := staleCacheFixture(t)
	var firstPrompt string
	var subCalls int
	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")},
		runSubAgent: func(_ context.Context, _ string, prompt, _, _ string) (subagent.Result, error) {
			subCalls++
			if subCalls == 1 {
				firstPrompt = prompt
			}
			return subagent.Result{Summary: "no relevant change."}, nil
		},
	}
	_, out, err := s.analyzeChange(context.Background(), nil, analyzeChangeIn{
		Ref:      "main",
		Question: "did this change anything?",
	})
	require.NoError(t, err)
	require.Equal(t, "origin/main", out.ResolvedRef)
	require.Equal(t, "main", out.Ref, "original input preserved")
	require.Contains(t, firstPrompt, "origin/main",
		"sub-agent prompt must reference the resolved ref so its git commands operate on the fresh tip")
}
