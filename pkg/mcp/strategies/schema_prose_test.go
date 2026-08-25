package strategies

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Playbook descriptions and terminal_advice are prose an agent obeys
// under time pressure, so the schema README names the writing rules.
func TestPlaybookSchema_NamesProseStyle(t *testing.T) {
	t.Parallel()
	assert.Contains(t, playbookSchemaMarkdown, "## Prose style")
}
