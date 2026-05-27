package sources

import (
	"encoding/json"
	"strings"
	"testing"
)

// Compile-time: ensure LauncherSlackAdapter implements SlackClient.
var _ SlackClient = (*LauncherSlackAdapter)(nil)

func TestParseSlackTSToUnix(t *testing.T) {
	cases := map[string]int64{
		"":                  0,
		"1715420822.000100": 1715420822,
		"1715420822":        1715420822,
		"not-a-number":      0,
		"abc.xyz":           0,
	}
	for in, want := range cases {
		if got := parseSlackTSToUnix(in); got != want {
			t.Errorf("parseSlackTSToUnix(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestSlackMessageTextFromAttachments guards against silently dropping
// monitoring/alert bot messages. Many alerting integrations post with an
// empty top-level `text` and put the real payload in `attachments[]`;
// without synthesis the poll filter drops them as "empty text".
func TestSlackMessageTextFromAttachments(t *testing.T) {
	raw := []byte(`{
		"ts": "1778677797.049359",
		"user": "U01B60N5VA5",
		"text": "",
		"attachments": [{
			"fallback": "[RESOLVED] JobFailedInZeebeNamespace critical",
			"title": ":heavy_check_mark: JobFailedInZeebeNamespace × 0",
			"text": "*Cluster:* prod-gke-australia\n*Severity:* critical"
		}]
	}`)
	var m slackMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	got := slackMessageText(m)
	if got == "" {
		t.Fatal("expected non-empty synthesized text for attachment-only bot message")
	}
	// Each attachment field should contribute so the agent can see both
	// the alert name (title) and the body (text).
	for _, want := range []string{"JobFailedInZeebeNamespace", "Cluster", "critical"} {
		if !strings.Contains(got, want) {
			t.Errorf("synthesized text missing %q: %q", want, got)
		}
	}
}

// TestSlackMessageTextFromBlocks covers modern bots that use Block Kit
// instead of attachments (Slack's preferred surface since 2019).
func TestSlackMessageTextFromBlocks(t *testing.T) {
	raw := []byte(`{
		"ts": "1",
		"text": "",
		"blocks": [
			{"type": "header", "text": {"type": "plain_text", "text": "Build failed"}},
			{"type": "section", "text": {"type": "mrkdwn", "text": "Pipeline *main* failed on step test."}}
		]
	}`)
	var m slackMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	got := slackMessageText(m)
	for _, want := range []string{"Build failed", "Pipeline", "test"} {
		if !strings.Contains(got, want) {
			t.Errorf("synthesized text missing %q: %q", want, got)
		}
	}
}

// TestSlackMessageTextPrefersTopLevel ensures the synthesis is a fallback,
// not a replacement: when a message has both `text` and attachments,
// `text` wins (matches what Slack shows to humans).
func TestSlackMessageTextPrefersTopLevel(t *testing.T) {
	m := slackMessage{Text: "main text", Attachments: []slackAttachment{{Fallback: "should not see this"}}}
	if got := slackMessageText(m); got != "main text" {
		t.Fatalf("expected top-level text to win, got %q", got)
	}
}
