//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// The four proposal tool names the investigation flow stages, in the
// order the stub emits them. The backend invariant is that they land in
// the transcript in this order — the product contract the four
// proposal-card code paths share.
var wantProposalToolsInOrder = []string{
	"mcp__triagent-strategies__playbook_proposal_draft",
	"mcp__triagent-wiki__propose_wiki_draft",
	"mcp__triagent-git-payments__draft_pr",
	"mcp__triagent-git-payments__create_github_issue",
}

// TestInvestigation_BackendInvariants drives a synthetic investigation end
// to end through the real launcher + scripted stubs and pins the
// observable golden-path contracts: the seeded fixture is listed, a fresh
// session is created via /api/preflight, the profile-derived knobs reach
// the agent (allowed-tools globs for the linked repo + extra MCP, the
// system-prompt body over stdin), the four proposals land in transcript
// order, the gh issue body matches the proposal, and a follow-up turn
// renders. Assertions are invariants, not whole-body snapshots.
func TestInvestigation_BackendInvariants(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:         "with-prompts-and-linked-repo",
		SessionFixtures: "in-progress",
		StubScript:      "investigation",
		GhScript:        "investigation",
	})

	// The pre-baked fixture investigation is listed before any live work.
	if !listContainsInvestigation(t, h, "inv-flow2-fixture") {
		t.Fatalf("seeded fixture investigation not listed by /api/investigations")
	}

	// A live SSE subscription so we can wait on the synthetic end event
	// (claude exited, transcript idle) deterministically rather than
	// polling the REST transcript on a timer.
	stream := h.Client.OpenStream(t)
	defer stream.Close()

	// Create a fresh investigation. All profile inputs are optional, so an
	// empty inputs map is a valid preflight.
	id := createInvestigation(t, h)
	if id == "inv-flow2-fixture" {
		t.Fatalf("new investigation reused the fixture id %q", id)
	}

	// Kick off the live session: the launcher execs the claude-stub, which
	// emits an assistant turn, the four proposal round-trips, then end.
	status, body := h.Client.PostJSON(t, "/api/investigations/"+id+"/start", nil)
	if status != http.StatusAccepted {
		t.Fatalf("start session status = %d (body %s)", status, body)
	}
	waitForEnd(t, stream, id)

	// Turn-1 trace: the profile-derived knobs reached the agent.
	tr := h.StubTrace(t, "main")
	assertAllowedToolsCover(t, tr.AllowedTools, []string{
		"mcp__triagent-git-payments__*", // linked_repos
		"mcp__org-docs__search",          // extra_mcps allowed_tools
	})
	// The main system prompt is delivered as the claude prompt over stdin;
	// its first line is the fixture's distinctive marker, proving the
	// profile-derived prompt reached the agent.
	assertSystemPromptReached(t, tr, "TRIAGENT-E2E-FLOW2-SYSTEM-PROMPT-MARKER")

	// The four proposals are in the transcript in order.
	got := proposalToolNamesInOrder(t, h, id)
	if len(got) != len(wantProposalToolsInOrder) {
		t.Fatalf("got %d proposal tool calls, want %d: %v", len(got), len(wantProposalToolsInOrder), got)
	}
	for i, want := range wantProposalToolsInOrder {
		if got[i] != want {
			t.Errorf("proposal[%d] = %q, want %q (full order: %v)", i, got[i], want, got)
		}
	}

	// The gh issue-create body matches the proposal that triggered it, and
	// it ran only as part of the create_github_issue round-trip.
	assertGhIssueCreate(t, h, "Payments rollout config regression", "acme/payments")

	// A follow-up message resumes the session; turn 2 renders an assistant
	// reply and ends.
	status, body = h.Client.PostJSON(t, "/api/investigations/"+id+"/messages",
		map[string]string{"text": "Confirm the regression is isolated to the payments config."})
	if status != http.StatusAccepted {
		t.Fatalf("follow-up message status = %d (body %s)", status, body)
	}
	waitForEnd(t, stream, id)

	if !transcriptHasAssistantText(t, h, id, "isolated to the payments config") {
		t.Errorf("turn-2 assistant reply not found in transcript")
	}
}

// listContainsInvestigation reports whether /api/investigations includes
// an investigation with the given id.
func listContainsInvestigation(t *testing.T, h *harness.Harness, id string) bool {
	t.Helper()
	status, body := h.Client.Get(t, "/api/investigations")
	if status != http.StatusOK {
		t.Fatalf("GET /api/investigations status = %d (body %s)", status, body)
	}
	var parsed struct {
		Investigations []struct {
			ID string `json:"id"`
		} `json:"investigations"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode investigations list: %v (body %s)", err, body)
	}
	for _, inv := range parsed.Investigations {
		if inv.ID == id {
			return true
		}
	}
	return false
}

// createInvestigation POSTs an empty-inputs preflight and returns the new
// investigation id from the snapshot response.
func createInvestigation(t *testing.T, h *harness.Harness) string {
	t.Helper()
	status, body := h.Client.PostJSON(t, "/api/preflight", map[string]any{
		"inputs": map[string]any{},
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/preflight status = %d (body %s)", status, body)
	}
	var snap struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("decode preflight snapshot: %v (body %s)", err, body)
	}
	if snap.ID == "" {
		t.Fatalf("preflight returned an empty investigation id (body %s)", body)
	}
	return snap.ID
}

// transcriptEvent is the subset of the transcript REST event shape the
// backend assertions read.
type transcriptEvent struct {
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	ToolName string         `json:"toolName"`
	ToolID   string         `json:"toolId"`
	Input    map[string]any `json:"toolInput"`
}

// fetchTranscript returns the investigation's transcript events in order.
func fetchTranscript(t *testing.T, h *harness.Harness, id string) []transcriptEvent {
	t.Helper()
	status, body := h.Client.Get(t, "/api/investigations/"+id+"/transcript")
	if status != http.StatusOK {
		t.Fatalf("GET transcript status = %d (body %s)", status, body)
	}
	var parsed struct {
		Events []transcriptEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode transcript: %v (body %s)", err, body)
	}
	return parsed.Events
}

// proposalToolNamesInOrder returns the toolName of every tool_use event in
// the transcript, in order — the four proposals the stub staged.
func proposalToolNamesInOrder(t *testing.T, h *harness.Harness, id string) []string {
	t.Helper()
	var out []string
	for _, ev := range fetchTranscript(t, h, id) {
		if ev.Kind == "tool_use" {
			out = append(out, ev.ToolName)
		}
	}
	return out
}

// transcriptHasAssistantText reports whether any assistant event's text
// contains substr.
func transcriptHasAssistantText(t *testing.T, h *harness.Harness, id, substr string) bool {
	t.Helper()
	for _, ev := range fetchTranscript(t, h, id) {
		if ev.Kind == "assistant" && strings.Contains(ev.Text, substr) {
			return true
		}
	}
	return false
}

// assertAllowedToolsCover fails the test if any want glob is absent from
// the agent's --allowedTools list.
func assertAllowedToolsCover(t *testing.T, got, want []string) {
	t.Helper()
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("allowed-tools missing %q (got %v)", w, got)
		}
	}
}

// assertSystemPromptReached confirms the profile-derived system prompt
// reached the agent. The first stdin line the stub recorded is the first
// line of the prompt body prompts.Build assembled, which starts with the
// profile's system.md.
func assertSystemPromptReached(t *testing.T, tr harness.Trace, marker string) {
	t.Helper()
	for _, line := range tr.StdinEvents {
		if strings.Contains(line, marker) {
			return
		}
	}
	t.Errorf("system-prompt marker %q not found in stub stdin events %v", marker, tr.StdinEvents)
}

// assertGhIssueCreate confirms a `gh issue create` ran with the proposal's
// title + repo. The gh-stub records every invocation in order.
func assertGhIssueCreate(t *testing.T, h *harness.Harness, title, repo string) {
	t.Helper()
	for _, argv := range h.GhTrace(t).Invocations {
		if len(argv) < 2 || argv[0] != "issue" || argv[1] != "create" {
			continue
		}
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, title) && strings.Contains(joined, repo) {
			return
		}
	}
	t.Errorf("no `gh issue create` invocation with title %q + repo %q; invocations: %v",
		title, repo, h.GhTrace(t).Invocations)
}

// waitForEnd blocks until the synthetic end envelope for the investigation
// arrives on the stream, bounding the wait so a wedged turn fails fast.
func waitForEnd(t *testing.T, s *harness.Stream, investigationID string) {
	t.Helper()
	s.WaitForEvent(t, "end for "+investigationID, func(ev harness.StreamEvent) bool {
		if ev.Kind != "end" {
			return false
		}
		var d struct {
			InvestigationID string `json:"investigationId"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		return d.InvestigationID == investigationID
	}, 30*time.Second)
}
