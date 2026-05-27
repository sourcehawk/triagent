package git

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitValidator_FileExistsAtRef(t *testing.T) {
	t.Parallel()
	cacheDir, repoDir := initFixtureRepo(t, "example-org", "fixture")
	_ = cacheDir
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	cits := []citations.Citation{
		{Kind: citations.KindGithubFile, Repo: "example-org/fixture", Path: "README.md", Ref: "v0.2.0"},
	}
	assert.Empty(t, v.Validate(cits))
}

func TestGitValidator_FileMissingAtRef(t *testing.T) {
	t.Parallel()
	_, repoDir := initFixtureRepo(t, "example-org", "fixture")
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	cits := []citations.Citation{
		{Kind: citations.KindGithubFile, Repo: "example-org/fixture", Path: "does-not-exist.go", Ref: "v0.2.0"},
	}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "does-not-exist.go")
}

func TestGitValidator_CommitResolves(t *testing.T) {
	t.Parallel()
	_, repoDir := initFixtureRepo(t, "example-org", "fixture")
	// Resolve v0.2.0 to a real sha first.
	sha, err := gitOutput(context.Background(), repoDir, "rev-parse", "v0.2.0^{commit}")
	require.NoError(t, err)
	sha = trimNewline(sha)
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	cits := []citations.Citation{
		{Kind: citations.KindGithubCommit, Repo: "example-org/fixture", SHA: sha},
	}
	assert.Empty(t, v.Validate(cits))
}

func TestGitValidator_CommitDoesNotResolve(t *testing.T) {
	t.Parallel()
	_, repoDir := initFixtureRepo(t, "example-org", "fixture")
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	cits := []citations.Citation{
		// Full 40-char hex but not a real sha in this repo.
		{Kind: citations.KindGithubCommit, Repo: "example-org/fixture", SHA: "0000000000000000000000000000000000000000"},
	}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "does not resolve")
}

func TestGitValidator_RepoMismatch(t *testing.T) {
	t.Parallel()
	_, repoDir := initFixtureRepo(t, "example-org", "fixture")
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	cits := []citations.Citation{
		{Kind: citations.KindGithubFile, Repo: "other-owner/other-repo", Path: "README.md", Ref: "v0.2.0"},
	}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "scoped repo")
}

func TestGitValidator_PRRepoMismatch(t *testing.T) {
	t.Parallel()
	_, repoDir := initFixtureRepo(t, "example-org", "fixture")
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	cits := []citations.Citation{
		{Kind: citations.KindGithubPR, Repo: "wrong/repo", PRNum: 42},
	}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "scoped repo")
}

func TestGitValidator_PRMatchingRepo_NoExistenceCheck(t *testing.T) {
	t.Parallel()
	_, repoDir := initFixtureRepo(t, "example-org", "fixture")
	v := &gitValidator{repo: "example-org/fixture", repoDir: repoDir, ctx: context.Background()}
	// PR existence is the sub-agent's job (via gh pr view in its prompt).
	// Validator only enforces shape (handled by ShapeCheck) and repo-match.
	cits := []citations.Citation{
		{Kind: citations.KindGithubPR, Repo: "example-org/fixture", PRNum: 12345},
	}
	assert.Empty(t, v.Validate(cits))
}

func trimNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}

func TestAnalyzeChange_HappyPath_FlowsCitations(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	// Resolve a real sha for v0.2.0 so the validator's existence check passes.
	dir := filepath.Join(s.cacheDir, s.owner, s.name)
	sha, err := gitOutput(context.Background(), dir, "rev-parse", "v0.2.0^{commit}")
	require.NoError(t, err)
	sha = trimNewline(sha)

	s.runSubAgent = func(_ context.Context, _, _, _, _ string) (subagent.Result, error) {
		return subagent.Result{
			Summary: fmt.Sprintf(`The leader-election timeout was tightened in [1].

<<<CITATIONS
[{"kind":"github_commit","repo":"example-org/fixture","sha":"%s"}]
CITATIONS>>>`, sha),
		}, nil
	}

	_, out, err := s.analyzeChange(context.Background(), nil, analyzeChangeIn{Ref: "v0.2.0", Question: "what changed?"})
	require.NoError(t, err)
	require.Len(t, out.Citations, 1)
	assert.Equal(t, citations.KindGithubCommit, out.Citations[0].Kind)
	assert.Equal(t, sha, out.Citations[0].SHA)
	assert.Empty(t, out.CitationsParseError)
	assert.Contains(t, out.Summary, "leader-election")
}

func TestAnalyzeChange_BogusSHA_RetriesThenSoftFails(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")

	calls := 0
	s.runSubAgent = func(_ context.Context, _, prompt, _, _ string) (subagent.Result, error) {
		calls++
		// Both attempts emit a bogus sha; existence check fails both times.
		if calls == 2 {
			assert.Contains(t, prompt, "0000000000000000", "retry should include broken sha")
		}
		return subagent.Result{
			Summary: `claim [1].

<<<CITATIONS
[{"kind":"github_commit","repo":"example-org/fixture","sha":"0000000000000000000000000000000000000000"}]
CITATIONS>>>`,
		}, nil
	}

	_, out, err := s.analyzeChange(context.Background(), nil, analyzeChangeIn{Ref: "v0.2.0", Question: "what changed?"})
	require.NoError(t, err, "soft-fail must not propagate as Go error")
	assert.Equal(t, 2, calls)
	assert.Empty(t, out.Citations)
	assert.NotEmpty(t, out.CitationsParseError)
	assert.Contains(t, out.Summary, "claim [1]")
}
