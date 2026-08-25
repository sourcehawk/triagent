package strategies

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildDispatchPrompt_IncludesPlaybookNodesInOrder(t *testing.T) {
	t.Parallel()
	pb := &Playbook{
		ID:         "pb",
		Symptom:    "s",
		Entrypoint: "a",
		Nodes: map[string]Node{
			"a": {ID: "a", Description: "first step prose"},
			"b": {ID: "b", Description: "second step prose"},
		},
	}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Findings: map[string]any{"x": 1},
		Summary:  "the summary",
	})
	assert.True(t, strings.Contains(prompt, "first step prose"))
	assert.True(t, strings.Contains(prompt, "second step prose"))
	assert.True(t, strings.Contains(prompt, "\"x\""))
	assert.True(t, strings.Contains(prompt, "the summary"))
}

// Dispatched playbooks refer to "the Writing style rules in your prompt";
// the sub-agent has no launcher system prompt, so the section must be in
// the dispatch prompt itself, before the operator context it applies to.
func TestBuildDispatchPrompt_AppendsWritingStyle(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "wiki_proposal", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "draft"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{Playbook: pb, Notes: "the brief"})
	assert.Contains(t, prompt, "## Writing style")
	assert.Contains(t, prompt, "## Self-check")
	assert.Less(t, strings.Index(prompt, "## Writing style"), strings.Index(prompt, "## Operator-supplied context"))
}

func TestBuildDispatchPrompt_NamesTerminalToolWhenSet(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "playbook_proposal", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "draft a playbook"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook:     pb,
		ProposalTool: "playbook_proposal_draft",
	})
	// The terminal instruction must name the submit tool and forbid ending
	// with only a prose summary or a file write — the exact divergence that
	// produced a confabulated "drafted" with no actual proposal.
	assert.Contains(t, prompt, "playbook_proposal_draft",
		"finishing instruction must name the proposal tool the flow has to call")
	low := strings.ToLower(prompt)
	assert.Contains(t, low, "summary", "must warn that a prose summary is not a submission")
	assert.Contains(t, low, "must", "finishing instruction is mandatory, not advisory")
}

func TestBuildDispatchPrompt_RequiresValidationBeforeSubmit(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "playbook_proposal", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "draft a playbook"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook:     pb,
		ProposalTool: "playbook_proposal_draft",
		ValidateTool: "validate_playbook",
	})
	// The agent must be told to validate (the real session submitted invalid
	// YAML five times before one validated). The instruction must name the
	// validator and tie it to submitting.
	assert.Contains(t, prompt, "validate_playbook",
		"finishing instruction must name the validator the flow has to run before submitting")
	assert.Contains(t, strings.ToLower(prompt), "before",
		"validation must be ordered before the submit")
}

func TestBuildDispatchPrompt_NoValidateLineWhenValidateToolUnset(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "wiki_proposal", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "draft a wiki entry"},
	}}
	// The wiki flow has a submit tool but no separate validator.
	prompt := BuildDispatchPrompt(DispatchInputs{Playbook: pb, ProposalTool: "propose_wiki_draft"})
	assert.NotContains(t, prompt, "validate_playbook")
}

func TestBuildDispatchPrompt_NoTerminalSectionWhenToolUnset(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{Playbook: pb})
	assert.NotContains(t, prompt, "## Finishing",
		"non-proposal dispatches get no mandatory-terminal section")
}

func TestBuildDispatchPrompt_AppendsRefinementWhenPresent(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook:           pb,
		Findings:           nil,
		Summary:            "s",
		OperatorRefinement: "split the wiki into two entries",
	})
	assert.True(t, strings.Contains(prompt, "split the wiki into two entries"))
	assert.True(t, strings.Contains(strings.ToLower(prompt), "refinement"))
}

func TestBuildDispatchPrompt_NoRefinementOmitsSection(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Findings: nil,
		Summary:  "s",
	})
	assert.False(t, strings.Contains(strings.ToLower(prompt), "refinement"))
}

func TestBuildDispatchPrompt_AppendsNotesWhenPresent(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Notes:    "MODE: reflexive. INVESTIGATION SUMMARY: alert self-resolved during upgrade.",
	})
	assert.True(t, strings.Contains(prompt, "MODE: reflexive"))
	assert.True(t, strings.Contains(prompt, "alert self-resolved during upgrade"))
}

func TestBuildDispatchPrompt_NoNotesOmitsSection(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Summary:  "s",
	})
	assert.False(t, strings.Contains(strings.ToLower(prompt), "operator-supplied context"))
}

// TestBuildDispatchPrompt_RendersProposalsSection covers the
// auto-injected proposal state. The sub-agent uses this to avoid
// re-submitting a shape the operator just declined: the section
// surfaces status, playbook_id, proposal_id, and (when present) the
// decline note.
func TestBuildDispatchPrompt_RendersProposalsSection(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook: pb,
		Proposals: []ProposalSummary{
			{ProposalID: "de222222bbbb", PlaybookID: "stuck_reconciliation", Type: "investigation",
				Status: "declined", At: "2026-05-20T20:51:30Z", Note: "split into two entries — one per cluster"},
			{ProposalID: "pending00001", PlaybookID: "cluster_health", Type: "investigation",
				Status: "awaiting_review", At: "2026-05-20T22:51:00Z"},
		},
	})
	lower := strings.ToLower(prompt)
	assert.Contains(t, lower, "proposal state", "section header must signal what this block is")
	assert.Contains(t, prompt, "stuck_reconciliation", "declined entry's playbook_id must appear")
	assert.Contains(t, prompt, "de222222bbbb", "proposal_id must appear so the sub-agent can reference it")
	assert.Contains(t, prompt, "split into two entries — one per cluster",
		"decline note must surface — this is the whole reason we inject this section")
	assert.Contains(t, prompt, "cluster_health", "awaiting_review entry must also appear")
	assert.Contains(t, prompt, "awaiting_review", "status labels must be present so the sub-agent can filter")
	assert.Contains(t, prompt, "declined")
}

// TestBuildDispatchPrompt_NoProposalsOmitsSection: a fresh launcher
// with no proposals must not emit an empty header.
func TestBuildDispatchPrompt_NoProposalsOmitsSection(t *testing.T) {
	t.Parallel()
	pb := &Playbook{ID: "pb", Entrypoint: "a", Nodes: map[string]Node{
		"a": {ID: "a", Description: "step"},
	}}
	prompt := BuildDispatchPrompt(DispatchInputs{Playbook: pb})
	assert.False(t, strings.Contains(strings.ToLower(prompt), "proposal state"))
}
