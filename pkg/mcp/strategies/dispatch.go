package strategies

import (
	"context"
	"fmt"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

// dispatchAllowedToolsFor returns the per-playbook sub-agent tool allowlist.
// Scoped narrowly — the dispatch sub-agent should reach only the tools its
// proposal flow legitimately needs.
func dispatchAllowedToolsFor(playbookID string) string {
	switch playbookID {
	case "wiki_proposal":
		return "mcp__triagent-wiki__propose_wiki_draft,mcp__triagent-strategies__decline_proposal,mcp__triagent-wiki__wiki_get,mcp__triagent-wiki__wiki_list_entities,mcp__triagent-wiki__wiki_search"
	case "playbook_proposal":
		return "mcp__triagent-strategies__playbook_proposal_draft,mcp__triagent-strategies__decline_proposal,mcp__triagent-strategies__list_playbooks,mcp__triagent-strategies__list_proposals,mcp__triagent-strategies__get_playbook_raw,mcp__triagent-strategies__playbook_correlate,mcp__triagent-strategies__validate_playbook"
	default:
		// Unknown dispatch-mode playbooks get no MCP tools beyond what
		// claude's built-ins provide. Operator can extend the table when
		// they add a new dispatch playbook.
		return ""
	}
}

// dispatchTimeoutFor returns the per-playbook sub-agent wall-clock cap, or
// zero to fall back to the subagent package's default (5 min). Mirrors the
// dispatchAllowedToolsFor shape: opt-in per playbook id.
//
// Why per-playbook overrides: playbook_proposal and wiki_proposal both do
// schema-read → enumerate → draft → validate (sometimes draft+validate
// twice, for splits or revision rounds), which pushes against the 5-min
// default. When the default fires mid-draft, the sub-agent is SIGKILL'd
// before it can call *_proposal_draft, and the master agent reads the
// partial summary as a successful submission. Bumping the cap reduces how
// often we land in that confabulation window.
func dispatchTimeoutFor(playbookID string) time.Duration {
	switch playbookID {
	case "playbook_proposal", "wiki_proposal":
		return 15 * time.Minute
	default:
		return 0
	}
}

// dispatchProposalToolFor returns the tool a proposal flow MUST call to
// actually submit its draft, or "" for non-proposal dispatches. Drives the
// mandatory finishing instruction in the dispatch prompt and (paired) the
// subagent terminal-tool verification — the agent invokes it by its bare
// name, so that's what the prompt names.
func dispatchProposalToolFor(playbookID string) string {
	switch playbookID {
	case "playbook_proposal":
		return "playbook_proposal_draft"
	case "wiki_proposal":
		return "propose_wiki_draft"
	default:
		return ""
	}
}

// dispatchValidateToolFor returns the validator a proposal flow must run
// before submitting, or "" when the submit tool is the only validator (wiki).
// Drives the validate-before-submit step in the dispatch prompt.
func dispatchValidateToolFor(playbookID string) string {
	switch playbookID {
	case "playbook_proposal":
		return "validate_playbook"
	default:
		return ""
	}
}

// maxForceDispatchRetries bounds how many times runDispatch resumes a
// proposal sub-agent that ended without reaching a terminal, forcing it to
// call playbook_proposal_draft / propose_wiki_draft or decline_proposal.
const maxForceDispatchRetries = 2

// dispatchTerminalToolsFor returns the wire tool names that count as a valid
// terminal for a proposal flow — submit first, decline second — or nil for a
// non-proposal dispatch (no verification). The order is load-bearing:
// classifyProposalOutcome reads index 0 as the submit tool.
func dispatchTerminalToolsFor(playbookID string) []string {
	switch playbookID {
	case "playbook_proposal":
		return []string{"mcp__triagent-strategies__playbook_proposal_draft", "mcp__triagent-strategies__decline_proposal"}
	case "wiki_proposal":
		return []string{"mcp__triagent-wiki__propose_wiki_draft", "mcp__triagent-strategies__decline_proposal"}
	default:
		return nil
	}
}

// classifyProposalOutcome maps the terminals the sub-agent actually called to
// submitted | declined | none. terminals is [submit, decline] from
// dispatchTerminalToolsFor.
func classifyProposalOutcome(terminals, called []string) string {
	calledSet := make(map[string]struct{}, len(called))
	for _, c := range called {
		calledSet[c] = struct{}{}
	}
	if _, ok := calledSet[terminals[0]]; ok {
		return "submitted"
	}
	if _, ok := calledSet[terminals[1]]; ok {
		return "declined"
	}
	return "none"
}

// forceTerminalPrompt is the follow-up sent when a proposal sub-agent ends
// without a terminal — a short, unambiguous instruction to finish properly.
func forceTerminalPrompt(playbookID string) string {
	return fmt.Sprintf("You ended your last turn without reaching a terminal. You MUST now call either `%s` to submit the draft you prepared, or `decline_proposal` with a one-line reason if you are deliberately not proposing. Call the tool now — do not reply with prose.", dispatchProposalToolFor(playbookID))
}

// runDispatch executes a dispatch-mode playbook as a sub-agent run. For a
// proposal flow it verifies a terminal tool actually fired and, if not,
// resumes the same conversation to force one (bounded by
// maxForceDispatchRetries). Returns the final Result, the classified proposal
// outcome ("" for non-proposal dispatches), and any error.
func (s *Server) runDispatch(ctx context.Context, pb *Playbook, parentSessionID, notes, operatorRefinement string) (subagent.Result, string, error) {
	var (
		findings map[string]any
		summary  string
	)
	if s.parentSessionState != nil {
		if f, sm, ok := s.parentSessionState(parentSessionID); ok {
			findings = f
			summary = sm
		}
	}
	// Best-effort proposal-state injection: missing dir / read errors
	// fall back to an empty slice so dispatch still runs. The sub-agent
	// just won't see the auto-loaded section in that case — same as
	// before this code existed.
	proposals, _ := ListProposals(s.userPlaybooksDir)
	prompt := BuildDispatchPrompt(DispatchInputs{
		Playbook:           pb,
		Notes:              notes,
		Findings:           findings,
		Summary:            summary,
		OperatorRefinement: operatorRefinement,
		Proposals:          proposals,
		ProposalTool:       dispatchProposalToolFor(pb.ID),
		ValidateTool:       dispatchValidateToolFor(pb.ID),
	})
	if s.subAgentRunner == nil {
		return subagent.Result{}, "", fmt.Errorf("dispatch %q: subagent runner not configured", pb.ID)
	}
	terminals := dispatchTerminalToolsFor(pb.ID)
	baseOpts := subagent.Options{
		AllowedTools:          dispatchAllowedToolsFor(pb.ID),
		Prompt:                prompt,
		Model:                 s.models.Subagent,
		MCPConfigPath:         s.mcpConfigPath,
		ParentToolID:          telemetry.CurrentToolID(ctx),
		Timeout:               dispatchTimeoutFor(pb.ID),
		RequiredTerminalTools: terminals,
	}
	res, err := s.subAgentRunner(ctx, baseOpts)
	if err != nil {
		return res, "", err
	}
	// Non-proposal dispatches aren't verified — preserve prior behaviour.
	if len(terminals) == 0 {
		return res, "", nil
	}
	// Resume-and-force when the sub-agent ended without a terminal. A
	// timed-out run is surfaced as-is, not retried (the cap fired mid-work).
	forcing := forceTerminalPrompt(pb.ID)
	for attempt := 0; attempt < maxForceDispatchRetries && len(res.TerminalToolsCalled) == 0 && !res.TimedOut; attempt++ {
		retryOpts := baseOpts
		retryOpts.Prompt = forcing
		retryOpts.ResumeSessionID = res.SessionID
		res, err = s.subAgentRunner(ctx, retryOpts)
		if err != nil {
			return res, "", err
		}
	}
	return res, classifyProposalOutcome(terminals, res.TerminalToolsCalled), nil
}
