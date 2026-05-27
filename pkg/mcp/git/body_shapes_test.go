package git

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIssueBodyShape_Sections(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"BODY SHAPE — investigation-filed issue",
		"## Description",
		"## Why",
		"## What & Impact",
		"## Evidence",
		"## Out of scope",
		"Impact:",
		"Plain-English title",
	} {
		require.Containsf(t, issueBodyShape, want, "issueBodyShape missing %q", want)
	}
}

func TestPRBodyShape_Sections(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"BODY SHAPE — draft PR",
		"## Description",
		"## Changes",
		"## Testing",
		"## Challenges",
		"Don't include `Fixes #N`",
	} {
		require.Containsf(t, prBodyShape, want, "prBodyShape missing %q", want)
	}
}

// The issue shape is what the create_github_issue caller sees, and
// the PR shape is what the draft_pr sub-agent sees. They share the
// Description heading, but the surrounding sections must not bleed:
// the PR shape has no Why / Evidence / Impact, and the issue shape
// has no Changes / Testing / Challenges.
func TestBodyShapes_DontBleed(t *testing.T) {
	t.Parallel()
	for _, prOnly := range []string{"## Changes", "## Testing", "## Challenges"} {
		require.Falsef(t, strings.Contains(issueBodyShape, prOnly), "issueBodyShape unexpectedly contains %q", prOnly)
	}
	for _, issueOnly := range []string{"## Why", "## What & Impact", "## Evidence", "## Out of scope"} {
		require.Falsef(t, strings.Contains(prBodyShape, issueOnly), "prBodyShape unexpectedly contains %q", issueOnly)
	}
}
