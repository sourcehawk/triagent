package strategies

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSummarize_SplitsEvidenceFromVerdict asserts the summarize tool
// returns two separate markdown bodies — a verdict (symptom + root
// cause + next steps + confidence + session footer) that the operator
// can paste into Slack, and an evidence body rendered as a sibling
// card. The split is what lets the verdict stay tight; mixing evidence
// back into the verdict markdown defeats the purpose.
func TestSummarize_SplitsEvidenceFromVerdict(t *testing.T) {
	t.Parallel()

	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err)

	_, sum, err := srv.summarize(ctx, nil, summarizeIn{
		SessionID:  start.SessionID,
		Symptom:    "ZeebeClusterUnhealthy on cluster abc.",
		RootCause:  "Elasticsearch rolling restart collided with spot-node disruption.",
		Evidence:   "- es-master-1 FailedAttachVolume at 12:30:07Z\n- peer recovery interrupted 4× between 12:54Z and 13:05Z",
		NextSteps:  "- Watch 10–15 min for self-heal; rotate stuck pods if not.",
		Confidence: "High — log line + node hostname corroborate.",
	})
	require.NoError(t, err, "summarize")

	// Verdict body carries symptom / root cause / next steps /
	// confidence + the session footer, but NOT the evidence body or
	// its heading.
	assert.Contains(t, sum.Markdown, "## Symptom", "verdict should keep Symptom heading")
	assert.Contains(t, sum.Markdown, "ZeebeClusterUnhealthy on cluster abc.", "verdict should carry symptom prose")
	assert.Contains(t, sum.Markdown, "## Likely root cause", "verdict should keep root cause heading")
	assert.Contains(t, sum.Markdown, "## Recommended next steps", "verdict should keep next steps heading")
	assert.Contains(t, sum.Markdown, "## Confidence", "verdict should keep confidence heading")
	assert.Contains(t, sum.Markdown, "cluster `abc`", "verdict should keep the session footer")

	assert.NotContains(t, sum.Markdown, "## Evidence", "evidence heading must not appear in the verdict body")
	assert.NotContains(t, sum.Markdown, "FailedAttachVolume", "evidence bullets must not appear in the verdict body")

	// Evidence body carries the heading + the bullets.
	assert.Contains(t, sum.EvidenceMarkdown, "## Evidence", "evidence body should carry its own heading")
	assert.Contains(t, sum.EvidenceMarkdown, "FailedAttachVolume", "evidence body should carry the bullets")
	assert.Contains(t, sum.EvidenceMarkdown, "peer recovery interrupted", "evidence body should carry the bullets")
}

// TestSummarize_OmitsEvidenceMarkdownWhenAbsent ensures that an agent
// that conclud3s without evidence (rare, but legal) doesn't get an
// empty evidence card rendered downstream. The frontend uses an empty
// string as the "don't render" signal.
func TestSummarize_OmitsEvidenceMarkdownWhenAbsent(t *testing.T) {
	t.Parallel()

	srv, err := New(Options{SystemPlaybooksDir: localTestPlaybooksDir})
	require.NoError(t, err, "New")

	ctx := context.Background()
	_, start, err := srv.walkPlaybook(ctx, nil, walkPlaybookIn{
		PlaybookID: "cluster_health",
		ClusterID:  "abc",
		Namespace:  "abc-zeebe",
	})
	require.NoError(t, err)

	_, sum, err := srv.summarize(ctx, nil, summarizeIn{
		SessionID: start.SessionID,
		Symptom:   "smoke",
		RootCause: "smoke",
		NextSteps: "- nothing",
	})
	require.NoError(t, err, "summarize")

	assert.Equal(t, "", strings.TrimSpace(sum.EvidenceMarkdown),
		"EvidenceMarkdown should be empty when no evidence was supplied")
	assert.NotContains(t, sum.Markdown, "## Evidence", "verdict body must not include the Evidence heading when empty")
}
