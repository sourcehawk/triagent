package wiki

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The drafting sub-agent runs in its own claude session, so the
// writing rules must be inside the prompt it receives.
func TestProposeWikiSubAgentPrompt_AppendsWritingSimply(t *testing.T) {
	t.Parallel()
	p := proposeWikiSubAgentPrompt(proposeWikiPromptArgs{Slug: "inc-x", Date: "2026-01-01", Status: "resolved", DraftPath: "/tmp/x.md", ProposalID: "prop-1"})
	assert.Contains(t, p, "# Writing style")
	assert.Contains(t, p, "### Self-check")
}
