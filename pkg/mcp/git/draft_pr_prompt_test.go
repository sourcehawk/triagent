package git

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDraftPRPrompt_ContainsKeyDirectives(t *testing.T) {
	t.Parallel()
	p := buildDraftPRPrompt("example-org/zeebe", "https://github.com/example-org/zeebe/issues/42", 42, "main", "only the BPMN parser, not DMN")
	must := []string{
		// Issue + scope
		"https://github.com/example-org/zeebe/issues/42",
		"only the BPMN parser, not DMN",
		// Skill directives — use
		"superpowers:test-driven-development",
		"superpowers:verification-before-completion",
		"simplify",
		// Repo-house-rules + CI discovery before deciding what to run pre-commit.
		"AGENTS.md",
		"CLAUDE.md",
		".github/workflows",
		// TDD is unconditional for code-bearing changes; missing test framework
		// is not an escape hatch.
		"TDD is mandatory",
		// Don't preempt CI on unrelated / unenforced failures.
		"preemptively bail",
		"enforced by CI",
		// Skill directives — exclude
		"Do NOT invoke",
		"superpowers:using-git-worktrees",
		"superpowers:brainstorming",
		// Status markers
		`<<<STATUS message="`,
		// PR title / body markers
		"<<<PR_TITLE",
		"PR_TITLE>>>",
		"<<<PR_BODY",
		"PR_BODY>>>",
		// Citations contract
		"<<<CITATIONS",
		// Hard rules
		"Do not run `git push`",
		"Do not run `gh pr create`",
		"gh issue view",
		"git add",
		"git commit",
		// PR body shape — section headers + key rules
		"## Description",
		"## Changes",
		"## Testing",
		"first token must be `Fixes #<num>`",
		"BODY SHAPE — draft PR",
		// Example PR_BODY threads the actual issue number so the agent
		// sees the literal Fixes #N it should emit.
		"Fixes #42.",
	}
	for _, m := range must {
		require.Containsf(t, p, m, "expected prompt to contain %q", m)
	}
	// No directive that would suggest the sub-agent should push or
	// open a PR itself.
	require.NotContains(t, strings.ToLower(p), "git push origin")
}

func TestBuildDraftPRPrompt_OmitsExtraPromptWhenEmpty(t *testing.T) {
	t.Parallel()
	p := buildDraftPRPrompt("o/n", "https://github.com/o/n/issues/1", 1, "main", "")
	require.NotContains(t, p, "Additional scope")
}
