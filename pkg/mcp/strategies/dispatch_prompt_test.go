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
