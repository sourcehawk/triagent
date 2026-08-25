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

// research_codebase answers a question about the repository as a whole
// at a ref. Unlike analyze_change its prompt must not frame the work
// around "the change at ref" — that framing makes the sub-agent open
// every answer with a relevance verdict on an unrelated commit.
func TestResearchCodebase_PromptIsAboutTheTreeNotAChange(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
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

func TestResearchCodebase_RefDefaultsToOriginDefaultBranch(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	s.gh = &stubGh{response: []byte("main\n")}
	var prompt string
	s.runSubAgent = func(_ context.Context, _ string, p, _, _ string) (subagent.Result, error) {
		prompt = p
		return subagent.Result{Summary: "answer.\n\n<<<CITATIONS\n[]\nCITATIONS>>>"}, nil
	}

	_, out, err := s.researchCodebase(context.Background(), nil, researchCodebaseIn{Question: "what is here?"})
	require.NoError(t, err)
	assert.Equal(t, "", out.Ref)
	assert.Equal(t, "origin/main", out.ResolvedRef)
	assert.Contains(t, prompt, "origin/main")
}

func TestResearchCodebase_RequiresQuestion(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	res, out, err := s.researchCodebase(context.Background(), nil, researchCodebaseIn{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	assert.NotNil(t, out.Citations, "citations must be [] not null so output-schema validation passes")
}

func TestResearchCodebase_FlowsCitations(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
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
