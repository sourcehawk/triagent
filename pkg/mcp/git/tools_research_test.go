package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// researchFixtureServer builds a Server over the fixture repo without
// New(), whose startup sweep goroutine would race the per-test
// worktreeRootOverride.
func researchFixtureServer(t *testing.T) *Server {
	t.Helper()
	cacheDir, _ := initFixtureRepo(t, "example-org", "fixture")
	return &Server{
		owner: "example-org", name: "fixture", cacheDir: cacheDir,
		gh:                   &stubGh{response: []byte("main\n")},
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}
}

// research_codebase answers a question about the repository as a whole
// at a ref. Unlike analyze_change its prompt must not frame the work
// around "the change at ref" — that framing makes the sub-agent open
// every answer with a relevance verdict on an unrelated commit.
func TestResearchCodebase_PromptIsAboutTheTreeNotAChange(t *testing.T) {
	t.Parallel()
	s := researchFixtureServer(t)
	var prompt string
	s.runSubAgent = func(_ context.Context, _ string, p, _, _ string) (subagent.Result, error) {
		prompt = p
		return subagent.Result{Summary: "answer.\n\n<<<CITATIONS\n[]\nCITATIONS>>>"}, nil
	}

	_, out, err := s.researchCodebase(context.Background(), nil, researchCodebaseIn{
		Ref:      "v0.2.0",
		Question: "which metrics does the operator expose?",
	})
	require.NoError(t, err)
	assert.Equal(t, "v0.2.0", out.Ref)
	assert.Equal(t, "v0.2.0", out.ResolvedRef)
	assert.Contains(t, out.Summary, "answer.")
	assert.Contains(t, prompt, "which metrics does the operator expose?")
	assert.Contains(t, prompt, "v0.2.0")
	assert.NotContains(t, prompt, "single git change")
	assert.NotContains(t, prompt, "If the change does not appear relevant")
}

// Read/Glob/Grep operate on the working tree, and the cache clone's
// checkout is never fast-forwarded, so the sub-agent must run in a
// detached worktree at the resolved ref — not in the clone itself.
func TestResearchCodebase_RunsSubAgentInDetachedWorktreeAtRef(t *testing.T) {
	t.Parallel()
	s := researchFixtureServer(t)
	cloneDir := filepath.Join(s.cacheDir, s.owner, s.name)
	wantSHA, err := gitOutput(context.Background(), cloneDir, "rev-parse", "v0.2.0^{commit}")
	require.NoError(t, err)
	wantSHA = trimNewline(wantSHA)

	var runDir, headSHA string
	s.runSubAgent = func(ctx context.Context, repoPath, _, _, _ string) (subagent.Result, error) {
		runDir = repoPath
		out, err := gitOutput(ctx, repoPath, "rev-parse", "HEAD")
		require.NoError(t, err)
		headSHA = trimNewline(out)
		return subagent.Result{Summary: "answer.\n\n<<<CITATIONS\n[]\nCITATIONS>>>"}, nil
	}

	_, _, err = s.researchCodebase(context.Background(), nil, researchCodebaseIn{Ref: "v0.2.0", Question: "q"})
	require.NoError(t, err)
	assert.NotEqual(t, cloneDir, runDir, "sub-agent must not run in the cache clone")
	assert.Equal(t, wantSHA, headSHA, "worktree HEAD must be the resolved ref")
	_, statErr := os.Stat(runDir)
	assert.True(t, os.IsNotExist(statErr), "worktree must be removed after the run")
}

// With no ref the tool inspects the remote default branch — and the
// worktree it hands the sub-agent is at origin/main's tip, not the cache
// clone's stale local main.
func TestResearchCodebase_RefDefaultsToOriginDefaultBranch(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir, _ := staleCacheFixture(t)
	originSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "origin/main"))
	localSHA := strings.TrimSpace(mustGitOutput(t, repoDir, "rev-parse", "main"))
	require.NotEqual(t, originSHA, localSHA, "fixture: origin/main must be ahead of local main")

	s := &Server{owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")}}
	s.worktreeRootOverride = t.TempDir()
	var prompt, headSHA string
	s.runSubAgent = func(ctx context.Context, repoPath, p, _, _ string) (subagent.Result, error) {
		prompt = p
		headSHA = strings.TrimSpace(mustGitOutput(t, repoPath, "rev-parse", "HEAD"))
		return subagent.Result{Summary: "answer.\n\n<<<CITATIONS\n[]\nCITATIONS>>>"}, nil
	}

	_, out, err := s.researchCodebase(context.Background(), nil, researchCodebaseIn{Question: "what is here?"})
	require.NoError(t, err)
	assert.Equal(t, "", out.Ref)
	assert.Equal(t, "origin/main", out.ResolvedRef)
	assert.Contains(t, prompt, "origin/main")
	assert.Equal(t, originSHA, headSHA, "sub-agent worktree must be at origin/main's tip, not stale local main")
}

func TestResearchCodebase_RequiresQuestion(t *testing.T) {
	t.Parallel()
	s := researchFixtureServer(t)
	res, out, err := s.researchCodebase(context.Background(), nil, researchCodebaseIn{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	assert.NotNil(t, out.Citations, "citations must be [] not null so output-schema validation passes")
}

func TestResearchCodebase_FlowsCitations(t *testing.T) {
	t.Parallel()
	s := researchFixtureServer(t)
	dir := filepath.Join(s.cacheDir, s.owner, s.name)
	sha, err := gitOutput(context.Background(), dir, "rev-parse", "v0.2.0^{commit}")
	require.NoError(t, err)
	sha = trimNewline(sha)
	s.runSubAgent = func(_ context.Context, _, _, _, _ string) (subagent.Result, error) {
		return subagent.Result{Summary: fmt.Sprintf(`Election lives in [1].

<<<CITATIONS
[{"kind":"github_commit","repo":"example-org/fixture","sha":"%s"}]
CITATIONS>>>`, sha)}, nil
	}
	_, out, err := s.researchCodebase(context.Background(), nil, researchCodebaseIn{Ref: "v0.2.0", Question: "where is election?"})
	require.NoError(t, err)
	require.Len(t, out.Citations, 1)
	assert.Equal(t, citations.KindGithubCommit, out.Citations[0].Kind)
	assert.Empty(t, out.CitationsParseError)
}
