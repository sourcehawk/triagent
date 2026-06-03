package strategies

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the strategies-server tool catalog with each
// tool's input shape introspected from its Go struct (and its
// jsonschema tags). Aggregated by the launcher's in-process catalog
// (internal/server/meta.go::toolCatalog) to surface input pickers +
// per-input descriptions in the editor and the MCP catalog view.
//
// Order mirrors the registration order in server.go's register()
// for parity with the agent-facing list_tools view.
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{
			Server:      "triagent-strategies",
			Name:        "list_playbooks",
			Description: "Return the available playbooks. Optional type filter ('investigation' / 'general').",
			Inputs:      toolspec.FromStruct(listPlaybooksIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "playbook_types",
			Description: "Return the canonical list of playbook types with descriptions.",
			Inputs:      toolspec.FromStruct(playbookTypesIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "walk_playbook",
			Description: "Open a new investigation session for a playbook.",
			Inputs:      toolspec.FromStruct(walkPlaybookIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "get_state",
			Description: "Return the current step, findings, and next-step options.",
			Inputs:      toolspec.FromStruct(getStateIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "step_complete",
			Description: "Record findings and advance to the next node in one call. An unknown goto target rejects the whole call (no findings recorded). Pass goto + optional findings.",
			Inputs:      toolspec.FromStruct(stepCompleteIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "summarize",
			Description: "Return a markdown audit trail of the investigation; rendered inline as the formal conclusion block.",
			Inputs:      toolspec.FromStruct(summarizeIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "playbook_schema",
			Description: "Return the playbook YAML schema + authoring conventions.",
			Inputs:      toolspec.FromStruct(playbookSchemaIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "get_playbook_raw",
			Description: "Return raw YAML for one playbook by id (base for a proposed update).",
			Inputs:      toolspec.FromStruct(getPlaybookRawIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "validate_playbook",
			Description: "Validate playbook YAML; returns structural errors.",
			Inputs:      toolspec.FromStruct(validatePlaybookIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "playbook_proposal_draft",
			Description: "Submit a draft playbook for inline operator review; the launcher renders a diff card with approve/decline.",
			Inputs:      toolspec.FromStruct(proposePlaybookDraftIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "decline_proposal",
			Description: "Explicit terminal for a proposal flow that deliberately submits no draft (below the bar). Records a one-line reason; the dispatcher requires a submit OR decline call so it can tell a real decline from an unfinished sub-agent.",
			Inputs:      toolspec.FromStruct(declineProposalIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "list_proposals",
			Description: "List current and recently-resolved playbook proposals (status: awaiting_review | approved | declined) with operator decline notes; call before drafting to avoid re-submitting a just-declined shape.",
			Inputs:      toolspec.FromStruct(listProposalsIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "playbook_resolve_entities",
			Description: "Canonicalise a candidate keyword set (services/errors/symptoms) against the union of all loaded playbooks' tags. Call before playbook_correlate to canonicalize fuzzy guesses.",
			Inputs:      toolspec.FromStruct(resolveEntitiesIn{}),
		},
		{
			Server:      "triagent-strategies",
			Name:        "playbook_correlate",
			Description: "Rank playbooks by entity overlap (services/errors/symptoms) with a query set, with one-hop lifting through delegate_to/handoff. Call playbook_resolve_entities first to canonicalize fuzzy keyword guesses; correlate is exact-match and will silently miss non-canonical input.",
			Inputs:      toolspec.FromStruct(playbookCorrelateIn{}),
		},
	}
}
