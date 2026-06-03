//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// TestProposalSurfacing_NestedBackendInvariants drives a turn whose
// proposals are drafted INSIDE a walk_playbook sub-agent dispatch (their
// tool-events carry parentToolId=dispatch-nested). This is the shape that
// regressed: a sub-agent-drafted proposal never surfaced. The launcher's
// nesting-independent surfacing paths must still fire end to end:
//
//   - codefix: draft_pr's end-event persists the proposal regardless of
//     nesting, so /api/codefix-proposals lists it.
//   - wiki: propose_wiki_draft's end-event fans a wiki_proposal_created
//     global event so the sidebar pending list refreshes live.
//
// The inline cards (wiki + playbook) are hoisted out of the nesting on the
// frontend; that DOM half is pinned in the browser layer
// (TestProposalSurfacing_NestedBrowser).
func TestProposalSurfacing_NestedBackendInvariants(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:    "with-prompts-and-linked-repo",
		StubScript: "nested-proposals",
	})

	// Subscribe before starting so the mid-turn global event isn't missed.
	stream := h.Client.OpenStream(t)
	defer stream.Close()

	id := createInvestigation(t, h)
	status, body := h.Client.PostJSON(t, "/api/investigations/"+id+"/start", nil)
	if status != http.StatusAccepted {
		t.Fatalf("start session status = %d (body %s)", status, body)
	}

	// The nested wiki proposal fans a global wiki_proposal_created event —
	// the sidebar-refresh path that does not depend on transcript nesting.
	ev := stream.WaitForKind(t, "wiki_proposal_created", 30*time.Second)
	var env struct {
		WikiProposalCreated struct {
			ProposalID      string `json:"proposalID"`
			InvestigationID string `json:"investigationID"`
		} `json:"wikiProposalCreated"`
	}
	if err := json.Unmarshal(ev.Data, &env); err != nil {
		t.Fatalf("decode wiki_proposal_created envelope: %v (data %s)", err, ev.Data)
	}
	if env.WikiProposalCreated.ProposalID != "prop-wiki-nested" {
		t.Errorf("wiki_proposal_created proposalID = %q, want prop-wiki-nested", env.WikiProposalCreated.ProposalID)
	}
	if env.WikiProposalCreated.InvestigationID != id {
		t.Errorf("wiki_proposal_created investigationID = %q, want %q", env.WikiProposalCreated.InvestigationID, id)
	}

	waitForEnd(t, stream, id)

	// The nested codefix proposal persisted from its draft_pr end-event,
	// surfacing on the repos activity panel despite being nested.
	assertCodefixProposalPersisted(t, h, codefixWant{
		proposalID: "prop-cf-nested", repo: "acme/payments", prNumber: 53, issueNumber: 52,
	})

	// All three proposal tool calls reached the transcript (nested under the
	// dispatch) — the precondition that makes the surfacing assertions above
	// meaningful rather than vacuous.
	for _, name := range []string{
		"mcp__triagent-strategies__playbook_proposal_draft",
		"mcp__triagent-wiki__propose_wiki_draft",
		"mcp__triagent-git-payments__draft_pr",
	} {
		if !transcriptHasToolCall(t, h, id, name) {
			t.Errorf("nested proposal %q missing from transcript", name)
		}
	}
}

// TestProposalSurfacing_NestedBrowser pins the DOM half: a wiki and a
// playbook proposal drafted inside a walk_playbook sub-agent dispatch (nested
// tool-events) still render their inline cards in the session view, because
// the transcript folder hoists them out of the nesting. The Go side launches
// the seeded launcher + scripted stub; the Playwright spec drives the SPA.
func TestProposalSurfacing_NestedBrowser(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:    "with-prompts-and-linked-repo",
		StubScript: "nested-proposals",
		Browser:    true,
	})
	h.Browser.Run(t, "nested-proposals.spec.ts")
}

// transcriptHasToolCall reports whether the investigation transcript carries
// a tool_use for the named tool (nested or not — the REST transcript flattens
// every event).
func transcriptHasToolCall(t *testing.T, h *harness.Harness, id, toolName string) bool {
	t.Helper()
	for _, e := range fetchTranscript(t, h, id) {
		if e.Kind == "tool_use" && e.ToolName == toolName {
			return true
		}
	}
	return false
}
