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
		return "mcp__triagent-wiki__propose_wiki_draft,mcp__triagent-wiki__wiki_get,mcp__triagent-wiki__wiki_list_entities,mcp__triagent-wiki__wiki_search"
	case "playbook_proposal":
		return "mcp__triagent-strategies__playbook_proposal_draft,mcp__triagent-strategies__list_playbooks,mcp__triagent-strategies__list_proposals,mcp__triagent-strategies__get_playbook_raw,mcp__triagent-strategies__playbook_correlate,mcp__triagent-strategies__validate_playbook"
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
		return 10 * time.Minute
	default:
		return 0
	}
}

// runDispatch executes a dispatch-mode playbook as one sub-agent run and
// returns a Result the caller can stuff into walkPlaybookOut.Dispatched.
func (s *Server) runDispatch(ctx context.Context, pb *Playbook, parentSessionID, notes, operatorRefinement string) (subagent.Result, error) {
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
	})
	if s.subAgentRunner == nil {
		return subagent.Result{}, fmt.Errorf("dispatch %q: subagent runner not configured", pb.ID)
	}
	return s.subAgentRunner(ctx, subagent.Options{
		AllowedTools:  dispatchAllowedToolsFor(pb.ID),
		Prompt:        prompt,
		Model:         s.models.Subagent,
		MCPConfigPath: s.mcpConfigPath,
		ParentToolID:  telemetry.CurrentToolID(ctx),
		Timeout:       dispatchTimeoutFor(pb.ID),
	})
}
