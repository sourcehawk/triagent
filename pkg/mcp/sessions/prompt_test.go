package sessions

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The post-mortem drafter is a fresh sub-agent session; the writing
// rules must be inside the prompt it receives, after the file contract.
func TestBuildDraftPrompt_AppendsWritingStyle(t *testing.T) {
	t.Parallel()
	p := buildDraftPrompt("/tmp/out.md", "/tmp/meta.json", "/tmp/events.jsonl")
	assert.Contains(t, p, `"/tmp/out.md"`)
	assert.Contains(t, p, "/tmp/events.jsonl")
	assert.Contains(t, p, "# Writing style")
	assert.Contains(t, p, "### Self-check")
}
