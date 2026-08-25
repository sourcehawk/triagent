package strategies

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sourcehawk/triagent/skills"
)

// DispatchInputs carries every input BuildDispatchPrompt needs. The caller
// (walk_playbook in dispatch mode) wires this from the walk_playbook input
// and (when available) the parent session state.
type DispatchInputs struct {
	Playbook           *Playbook
	Notes              string
	Findings           map[string]any
	Summary            string
	OperatorRefinement string
	// Proposals is the launcher's current proposal state, injected so the
	// dispatched sub-agent sees pending drafts and recent declines (with
	// the operator's note) without depending on the master agent to have
	// remembered to forward them in Notes. Empty → section omitted.
	Proposals []ProposalSummary
	// ProposalTool is the tool this flow MUST call to actually submit its
	// draft (e.g. playbook_proposal_draft / propose_wiki_draft). When set,
	// BuildDispatchPrompt appends a mandatory finishing instruction: the
	// sub-agent must end by calling it (or decline_proposal), never with a
	// prose summary or a file write. Empty → no finishing section (non-
	// proposal dispatches). Pairs with subagent terminal-tool verification.
	ProposalTool string
	// ValidateTool is the tool that validates a draft before submission
	// (e.g. validate_playbook). When set, the finishing instruction tells
	// the sub-agent to validate-until-clean before calling ProposalTool —
	// the submit tool rejects invalid input, so an unvalidated draft is
	// wasted effort. Empty → no validation step (flows whose submit tool is
	// the only validator, e.g. wiki).
	ValidateTool string
}

// BuildDispatchPrompt assembles the single-turn prompt the sub-agent receives.
// Sections (in order):
//  1. Role: one paragraph naming what playbook the sub-agent is executing.
//  2. Playbook instructions: each node's description in entrypoint-first
//     traversal order, separated by a horizontal-rule line.
//     2a. Writing style: the writing-simply skill body, because the
//     sub-agent has no launcher system prompt to carry it.
//  3. Operator-supplied context: the free-form notes passed to walk_playbook
//     — the parent agent's deliberate brief for the sub-agent.
//  4. Findings: pretty-printed JSON of the parent session's findings map.
//  5. Summary: the most recent summarize() output verbatim.
//  6. Operator refinement (optional): when non-empty, an explicit "honour
//     this refinement over the playbook's defaults" instruction.
func BuildDispatchPrompt(in DispatchInputs) string {
	var b strings.Builder
	if in.Playbook != nil {
		fmt.Fprintf(&b, "# Sub-agent dispatch: %s\n\n", in.Playbook.ID)
		fmt.Fprintf(&b, "You are running the %q playbook as a single sub-agent task. Read the playbook nodes below, then execute them as one coherent flow.\n\n", in.Playbook.ID)
		b.WriteString("## Playbook nodes\n\n")
		for _, id := range orderedNodeIDs(in.Playbook) {
			node := in.Playbook.Nodes[id]
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", id, strings.TrimSpace(node.Description))
		}
	}
	// The sub-agent is a fresh session with no launcher system prompt,
	// so the writing rules the playbook nodes refer to ride here.
	b.WriteString("## Writing style\n\nEvery draft, issue body, and chat reply you produce obeys the rules below.\n\n")
	b.WriteString(skills.WritingSimply())
	b.WriteString("\n\n")
	if strings.TrimSpace(in.Notes) != "" {
		b.WriteString("## Operator-supplied context\n\n")
		b.WriteString(strings.TrimSpace(in.Notes))
		b.WriteString("\n\n")
	}
	if len(in.Proposals) > 0 {
		b.WriteString("## Current proposal state\n\n")
		b.WriteString("Pending and recently-resolved proposals in the launcher's store. If a prior submission for the same playbook was just declined, adjust to the operator's note rather than re-submitting the same shape. \"Declared submitted\" is not a substitute for actually calling playbook_proposal_draft and receiving a proposal_id back.\n\n")
		for _, p := range in.Proposals {
			fmt.Fprintf(&b, "- **%s** %s `proposal=%s` `at=%s`\n", p.Status, p.PlaybookID, p.ProposalID, p.At)
			if strings.TrimSpace(p.Note) != "" {
				fmt.Fprintf(&b, "  operator note: %s\n", strings.TrimSpace(p.Note))
			}
		}
		b.WriteString("\n")
	}
	if len(in.Findings) > 0 {
		b.WriteString("## Findings from the parent investigation\n\n```json\n")
		findingsJSON, _ := json.MarshalIndent(in.Findings, "", "  ")
		b.Write(findingsJSON)
		b.WriteString("\n```\n\n")
	}
	if strings.TrimSpace(in.Summary) != "" {
		b.WriteString("## Investigation summary\n\n")
		b.WriteString(strings.TrimSpace(in.Summary))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(in.OperatorRefinement) != "" {
		b.WriteString("## Operator refinement\n\nHonour this refinement over the playbook's defaults:\n\n> ")
		b.WriteString(strings.TrimSpace(in.OperatorRefinement))
		b.WriteString("\n\n")
	}
	if tool := strings.TrimSpace(in.ProposalTool); tool != "" {
		var submit strings.Builder
		if validate := strings.TrimSpace(in.ValidateTool); validate != "" {
			fmt.Fprintf(&submit,
				"1. Draft the artifact from the playbook nodes above and the context in this prompt.\n"+
					"2. Validate before you submit: call `%s` on your draft and fix EVERY error it reports; repeat until it returns no errors. `%s` rejects invalid input — it returns `validation_errors` and an empty `proposal_id`, which produces nothing the operator can act on — so never submit a draft you have not validated clean.\n"+
					"3. Only after validation is clean, call `%s` with the draft and confirm it returns a non-empty `proposal_id`. That returned id is the only signal the operator sees.\n",
				validate, tool, tool)
		} else {
			fmt.Fprintf(&submit,
				"1. Draft the artifact from the playbook nodes above and the context in this prompt.\n"+
					"2. Call `%s` with your draft and confirm it returns a non-empty `proposal_id`. That returned id is the only signal the operator sees.\n",
				tool)
		}
		fmt.Fprintf(&b, `## Finishing — required

Your result MUST be a real tool call. A prose summary describing what you would submit, or writing the draft to a file, does NOT count and the operator will never see it.

**To submit:**

%s
**To deliberately not propose** (the work is below the bar): call `+"`decline_proposal`"+` with a one-line reason.

Do not end the task any other way. If you are about to write a final summary without having called one of these tools, stop and call the tool first.
`, submit.String())
	}
	return b.String()
}

// orderedNodeIDs returns the playbook's node ids in entrypoint-first order
// followed by the remaining ids alphabetically. Deterministic for caching
// and for tests; the sub-agent reads the nodes as one prose document, so
// order matters only for human readability of the prompt.
func orderedNodeIDs(pb *Playbook) []string {
	if pb == nil {
		return nil
	}
	out := make([]string, 0, len(pb.Nodes))
	if pb.Entrypoint != "" {
		if _, ok := pb.Nodes[pb.Entrypoint]; ok {
			out = append(out, pb.Entrypoint)
		}
	}
	rest := make([]string, 0, len(pb.Nodes))
	for id := range pb.Nodes {
		if id == pb.Entrypoint {
			continue
		}
		rest = append(rest, id)
	}
	sort.Strings(rest)
	return append(out, rest...)
}
