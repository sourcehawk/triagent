package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Callers wrap the body in their own "## Writing style" section, so the
// body must carry no H1 and its sections must sit one level below.
func TestWritingSimply_NestsUnderCallerHeading(t *testing.T) {
	body := WritingSimply()
	assert.False(t, strings.HasPrefix(body, "---"), "frontmatter must be stripped so the body can be embedded mid-prompt")
	assert.True(t, strings.HasPrefix(body, "Write for"), "body must start at the first paragraph, got %q", firstLine(body))
	assert.NotContains(t, body, "\n# ", "no H1 may remain")
	assert.NotContains(t, body, "\n## ", "H2 headings must be demoted so they nest under the caller's H2")
	assert.Contains(t, body, "### Self-check", "the self-check section is the load-bearing part for agents")
}

func TestExtract_WritesSkillsWithReferences(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Extract(dir))
	for _, rel := range []string{
		"writing-simply/SKILL.md",
		"writing-simply/references/checklist.md",
		"writing-simply/references/simple-english.md",
		"writing-simply/references/use-cases.md",
	} {
		p := filepath.Join(dir, ".claude", "skills", rel)
		_, err := os.Stat(p)
		require.NoErrorf(t, err, "missing %s", p)
	}
	extracted, err := os.ReadFile(filepath.Join(dir, ".claude", "skills", "writing-simply", "SKILL.md"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(extracted), "---\nname: writing-simply\n"),
		"extracted SKILL.md keeps its frontmatter so the claude CLI can discover it")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
