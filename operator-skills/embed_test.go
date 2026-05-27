package operatorskills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtract_WritesAllSkills(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Extract(dir))
	wantSlugs := []string{
		"operator-role", "answering-the-agent", "capture-decisions",
		"steering-investigations", "knowing-when-to-yield",
		"finishing-a-session", "resuming-after-takeover",
		"approving-drafts", "evaluating-codefixes",
	}
	for _, slug := range wantSlugs {
		p := filepath.Join(dir, ".claude", "skills", slug, "SKILL.md")
		_, err := os.Stat(p)
		require.NoErrorf(t, err, "missing skill at %s", p)
	}
}

func TestUmbrellaContent_ContainsKeyPhrase(t *testing.T) {
	require.Contains(t, string(UmbrellaContent()), "operator agent")
}

// TestCaptureDecisions_DirectsConcreteEngagement locks in the guidance
// that the operator must engage with the agent's concrete proposals
// (refine, split, add) rather than reply with a bare keyword.
func TestCaptureDecisions_DirectsConcreteEngagement(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Extract(dir))
	body, err := os.ReadFile(filepath.Join(dir, ".claude", "skills", "capture-decisions", "SKILL.md"))
	require.NoError(t, err)
	s := string(body)
	require.Contains(t, s, "Engaging with the agent's proposals",
		"capture-decisions must include guidance on engaging with the agent's concrete proposals")
	require.Contains(t, s, "distinct shape",
		"capture-decisions must instruct the operator to split distinct shapes the agent missed")
}
