package strategies

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handlers backing the playbook-proposal tools registered in server.go.
// Kept separate from the existing walker handlers so the proposal surface
// is easy to find and audit; the agent reaches them via the
// playbook_proposal meta-playbook rather than calling them directly.

// ── decline_proposal ────────────────────────────────────────────────────────

// declineProposalIn is the input for the decline_proposal terminal marker.
type declineProposalIn struct {
	Reason string `json:"reason" jsonschema:"One line on why this dispatch is deliberately NOT submitting a proposal (e.g. 'investigation was routine — below the novelty bar'). Required."`
}

// declineProposalOut acknowledges a deliberate no-proposal terminal.
type declineProposalOut struct {
	Acknowledged bool   `json:"acknowledged"`
	Message      string `json:"message"`
}

// declineProposal is the explicit terminal a proposal-flow sub-agent calls
// when it decides NOT to propose. It exists so the dispatcher can tell a
// deliberate "below the bar" decline from a confabulated submission: exactly
// one of the two terminal tools (propose / decline) must fire, and a prose
// summary is neither. The handler just records the reason; its value is being
// a verifiable tool call.
func (s *Server) declineProposal(_ context.Context, _ *mcp.CallToolRequest, in declineProposalIn) (*mcp.CallToolResult, declineProposalOut, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return errorResult("reason is required — state in one line why no proposal is being submitted"), declineProposalOut{}, nil
	}
	return nil, declineProposalOut{
		Acknowledged: true,
		Message:      "Recorded: no proposal submitted — " + reason,
	}, nil
}

// ── playbook_schema ─────────────────────────────────────────────────────────

type playbookSchemaIn struct{}

type playbookSchemaOut struct {
	Schema string `json:"schema"`
}

// playbookSchema returns a markdown description of the playbook YAML
// shape + authoring conventions + a worked example. The agent reads
// this once before drafting a proposal so the YAML it produces is
// structurally sound on the first try.
//
// Markdown rather than JSON Schema because agents drafting YAML do
// significantly better with prose + a worked example than with a raw
// schema document. The structural validator (validate_playbook) is the
// authoritative gate; this string is the "how to author" README.
func (s *Server) playbookSchema(ctx context.Context, req *mcp.CallToolRequest, _ playbookSchemaIn) (*mcp.CallToolResult, playbookSchemaOut, error) {
	return nil, playbookSchemaOut{Schema: playbookSchemaMarkdown}, nil
}

const playbookSchemaMarkdown = `# Playbook YAML schema

Top-level fields:

| field         | type                       | required | meaning                                                                 |
|---------------|----------------------------|----------|-------------------------------------------------------------------------|
| ` + "`id`" + `          | string                     | yes      | unique slug; alphanumeric + ` + "`_-`" + `, ≤64 chars; matches the file name      |
| ` + "`symptom`" + `     | string                     | yes      | one-line operator-facing trigger ("when X is happening, run this")      |
| ` + "`description`" + ` | string (block ` + "`|`" + `)        | no       | longer prose framing (background, scope, related playbooks)             |
| ` + "`entrypoint`" + `  | string                     | yes      | id of the first node; must exist in ` + "`nodes`" + `                              |
| ` + "`nodes`" + `       | map<string, Node>          | yes      | the decision tree                                                        |
| ` + "`type`" + `        | string                     | no       | ` + "`investigation`" + ` (default — diagnostic for an operator symptom) or ` + "`general`" + ` (meta/workflow run by the agent itself). Call ` + "`playbook_types`" + ` for the canonical list. |
| ` + "`services`" + `    | list<string>               | no       | canonical service entity tags consumed by ` + "`playbook_correlate`" + ` to rank this playbook against a query set lifted from findings. Names match ` + "`^[a-z0-9][a-z0-9-]*$`" + ` — the same vocabulary the wiki vault uses. Optional; empty = "not tagged" (still discoverable via ` + "`list_playbooks`" + `, just not via correlate). |
| ` + "`errors`" + `      | list<string>               | no       | canonical error entity tags. Same shape rules as ` + "`services`" + `. |
| ` + "`symptoms`" + `    | list<string>               | no       | canonical symptom entity tags. Same shape rules as ` + "`services`" + `. |
| ` + "`schema_version`" + ` | int                      | yes      | the playbook contract version this YAML targets. Set to ` + "`1`" + ` for current schema. Bumps on breaking schema changes; absence is treated as 1 for back-compat but new authoring should always include it. |

Node shape (each value of ` + "`nodes`" + `):

| field              | type                       | required | meaning                                                                 |
|--------------------|----------------------------|----------|-------------------------------------------------------------------------|
| ` + "`description`" + `       | string (block ` + "`|`" + `)        | yes      | what the agent should be doing at this step (prose, multi-line ok)      |
| ` + "`suggested_calls`" + `   | list<SuggestedCall>        | no       | concrete tool invocations to run before deciding the branch             |
| ` + "`expected_findings`" + ` | list<string>               | no       | finding keys to surface in ` + "`step_complete`" + `'s findings array (e.g. ` + "`crash_reason`" + `) |
| ` + "`next`" + `              | list<Branch>               | no       | conditional transitions; omit on terminal nodes                         |
| ` + "`terminal_advice`" + `   | string (block ` + "`|`" + `)        | no       | leaf-only: prose handed to the operator at the end                      |
| ` + "`handoff`" + `           | list<string>               | no       | leaf-only: target playbook ids the operator/agent should switch to next |
| ` + "`delegate_to`" + `       | string                     | no       | sub-flow only: id of another playbook to walk to its non-handoff terminal, then resume ` + "`next`" + `. Mutually exclusive with ` + "`terminal_advice`" + ` / ` + "`handoff`" + ` / ` + "`suggested_calls`" + `. The node MUST have a ` + "`next`" + ` branch (the resume target). |

SuggestedCall:
- ` + "`tool`" + `: ` + "`<server>/<tool>`" + ` wire name (e.g. ` + "`triagent-k8s/get_resource`" + `, ` + "`prom/cpu_pressure`" + `)
- ` + "`args`" + `: optional map<string, any>; supports ` + "`${cluster_id}`" + ` / ` + "`${namespace}`" + ` placeholders

Branch:
- ` + "`condition`" + `: prose the AGENT reads to decide whether to take this branch (the walker doesn't evaluate it)
- ` + "`goto`" + `: the id of the next node; must exist in ` + "`nodes`" + `

## Conventions

- Prefer **broad → narrow**: early nodes scope the symptom; later nodes investigate specifics.
- Branches are ordered for human readability; the agent picks based on the condition prose, not order.
- Include at least one terminal node per playbook (a node with ` + "`terminal_advice`" + ` and no ` + "`next`" + `).
- Cross-playbook handoff: on a terminal node, set ` + "`handoff: [other_playbook_id]`" + ` (or several ids) so the launcher's UI can render a clickable link and the validator can spot typos. The terminal_advice prose is the *why*; ` + "`handoff`" + ` is the *what*.
- **Prefer handoffs over bloat.** When you'd otherwise add ≥3 new nodes to a playbook that already has ≥10 nodes, draft a new dedicated playbook for that sub-investigation and add a ` + "`handoff: [<new_id>]`" + ` edit on the parent's relevant terminal. That keeps the parent readable, gives the operator a per-piece enable/disable knob, and makes the sub-flow independently runnable when they jump straight to that symptom. Reserve in-place edits for tiny tweaks (one node's prose, one extra suggested_call, condition rewording).
- **Sub-flows via ` + "`delegate_to`" + `.** When you want a playbook to walk another playbook to completion and then continue (rather than terminating into it via ` + "`handoff`" + `), set ` + "`delegate_to: <other_id>`" + ` on a non-terminal node and give it a ` + "`next`" + ` branch (typically a single ` + "`always`" + `). Findings recorded in the delegated walk land on the same session's findings map. The delegated playbook MUST end in non-handoff terminals (` + "`terminal_advice`" + ` only); a delegate that ends in ` + "`handoff`" + ` is rejected at runtime. Use this for "enrich context, then keep going" — see ` + "`gather_incidentio_context`" + ` and ` + "`gather_slack_context`" + ` for canonical examples.
- Use placeholders ` + "`${cluster_id}`" + ` and ` + "`${namespace}`" + ` in args; the walker substitutes them at suggestion time.
- **No version field.** Playbooks no longer carry a ` + "`version`" + ` field — git history is the version record. Omit ` + "`version`" + ` entirely from any YAML you draft or propose.
- **Entity tags: prefer SPECIFIC names over broad catchalls.** Tags like ` + "`unhealthy`" + `, ` + "`degraded`" + `, ` + "`slow`" + `, ` + "`failing`" + ` match almost any incident and inflate gateway lift signals in ` + "`playbook_correlate`" + ` — they water down match precision. If the precise canonical name doesn't exist yet, coin a narrow new one (e.g. ` + "`zeebe-backpressure`" + ` instead of ` + "`backpressure`" + `, ` + "`elasticsearch-unhealthy`" + ` instead of ` + "`unhealthy`" + `). Reuse names already used by other playbooks or the wiki vault when they fit.

## Worked example

` + "```yaml" + `
id: example_minimal
schema_version: 1
symptom: "A worked-example playbook showing the minimal valid shape"
description: |
  Two-node playbook: an entry that lists pods, and a terminal that
  hands back to the operator. Useful as a structural reference; not
  meant to ship.
entrypoint: list_pods

nodes:
  list_pods:
    description: |
      List pods in the bound namespace and look for any that aren't Running+Ready.
    suggested_calls:
      - tool: triagent-k8s/list_resources
        args:
          kind: Pod
    expected_findings:
      - failing_pods
    next:
      - condition: "any pods not Ready"
        goto: terminal_unhealthy
      - condition: "all pods Ready"
        goto: terminal_healthy

  terminal_unhealthy:
    description: "Terminal: at least one pod isn't Ready."
    terminal_advice: |
      Hand back to the operator with the failing pod names and switch
      to the cluster_health playbook for deeper triage.
    handoff: [cluster_health]

  terminal_healthy:
    description: "Terminal: all pods Ready."
    terminal_advice: |
      Pod state is healthy; the symptom likely lives elsewhere. Suggest
      checking gateway / Elasticsearch via their dedicated playbooks.
    handoff: [connectivity, elasticsearch]
` + "```" + `

## Sub-flow worked example

The same shape with a ` + "`delegate_to`" + ` node showing how a parent walks
a sub-flow and resumes:

` + "```yaml" + `
id: example_parent
schema_version: 1
symptom: "Worked example: parent that delegates a sub-flow"
entrypoint: pre
nodes:
  pre:
    description: "Decide to delegate."
    next:
      - condition: "always"
        goto: walk_helper
  walk_helper:
    description: "Walk example_helper, resume here when it returns."
    delegate_to: example_helper
    next:
      - condition: "always — sub-flow returned"
        goto: terminal_done
  terminal_done:
    description: "All done."
    terminal_advice: "Return result to operator."
` + "```" + `

The walker pushes a return frame at ` + "`walk_helper`" + ` and pops on
` + "`example_helper`" + `'s non-handoff terminal, resuming at
` + "`walk_helper.next`" + `. Findings recorded inside ` + "`example_helper`" + `
are visible to ` + "`walk_helper`" + `'s post-resume branches.

The validator (call ` + "`validate_playbook`" + ` with your YAML) checks: id non-empty,
symptom non-empty, entrypoint resolves to a node, every ` + "`goto`" + ` resolves to a
node, every node has a non-empty description, ` + "`handoff`" + ` entries are
shape-valid playbook ids and only appear on terminal nodes (those with
` + "`terminal_advice`" + ` set).
`

// ── get_playbook_raw ────────────────────────────────────────────────────────

type getPlaybookRawIn struct {
	ID string `json:"id" jsonschema:"the playbook id (one of the values returned by list_playbooks)"`
}

type getPlaybookRawOut struct {
	ID     string `json:"id"`
	YAML   string `json:"yaml"`
	Source string `json:"source"` // "system" or "user"
}

// getPlaybookRaw returns the raw YAML for one loaded playbook. Used by
// the agent during a proposal flow to base an update on an existing
// playbook (read it, modify it, propose under the same or a fresh id).
//
// "Raw" is stronger for the system set (we have the original YAML in
// EmbeddedRawPlaybooks and return it byte-for-byte). User playbooks
// aren't tracked with their raw bytes; we re-serialise from the parsed
// in-memory representation, which loses comments and exact formatting
// but stays semantically identical.
func (s *Server) getPlaybookRaw(ctx context.Context, req *mcp.CallToolRequest, in getPlaybookRawIn) (*mcp.CallToolResult, getPlaybookRawOut, error) {
	if in.ID == "" {
		return errorResult("id is required"), getPlaybookRawOut{}, nil
	}
	pb, ok := s.playbooks[in.ID]
	if !ok {
		return errorResult(fmt.Sprintf("playbook %q not found; call list_playbooks for valid ids", in.ID)), getPlaybookRawOut{}, nil
	}
	systemRaw, err := SystemRawPlaybooks(s.pluginPlaybooksDir)
	if err != nil {
		return errorResult(fmt.Sprintf("read system playbooks: %v", err)), getPlaybookRawOut{}, nil
	}
	if raw, isSystem := systemRaw[in.ID]; isSystem {
		return nil, getPlaybookRawOut{ID: in.ID, YAML: raw.YAML, Source: "system"}, nil
	}
	// User playbook: re-serialise the parsed form. Round-trips cleanly
	// for our own validator but loses comments/exact whitespace.
	rendered, err := RenderPlaybookYAML(pb)
	if err != nil {
		return errorResult(fmt.Sprintf("render user playbook %q: %v", in.ID, err)), getPlaybookRawOut{}, nil
	}
	return nil, getPlaybookRawOut{ID: in.ID, YAML: rendered, Source: "user"}, nil
}

// ── validate_playbook ──────────────────────────────────────────────────────

type validatePlaybookIn struct {
	YAML string `json:"yaml" jsonschema:"the full playbook YAML to validate"`
}

type validatePlaybookOut struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

// validatePlaybook runs the same structural checks the strategies MCP
// applies at load time. Wraps ParseAndValidatePlaybookYAML so the
// agent self-validates before calling playbook_proposal (validation
// runs server-side again on propose, but doing it here means the
// agent can iterate cheaply without polluting the user playbooks dir).
func (s *Server) validatePlaybook(ctx context.Context, req *mcp.CallToolRequest, in validatePlaybookIn) (*mcp.CallToolResult, validatePlaybookOut, error) {
	if strings.TrimSpace(in.YAML) == "" {
		return nil, validatePlaybookOut{OK: false, Errors: []string{"yaml is required"}}, nil
	}
	_, errs := ParseAndValidatePlaybookYAML([]byte(in.YAML))
	if len(errs) > 0 {
		return nil, validatePlaybookOut{OK: false, Errors: errs}, nil
	}
	return nil, validatePlaybookOut{OK: true}, nil
}

// ── playbook_proposal_draft ─────────────────────────────────────────────────

type proposePlaybookDraftIn struct {
	YAML string `json:"yaml" jsonschema:"the full playbook YAML to propose. Omit the version field — playbooks no longer carry a version field; git history is the version record."`
	Type string `json:"type" jsonschema:"the type slot the playbook belongs to (one of the names returned by playbook_types — e.g. 'investigation' or 'general'). Required: the launcher routes the draft into <type>/ under the user playbooks dir, and the type slot is the source of truth for classification (no longer a YAML field)."`
	Why  string `json:"why,omitempty" jsonschema:"one-sentence justification — surfaced in the diff card so the operator can audit later why the agent thought this was worth proposing"`
}

// proposePlaybookDraftOut identifies the draft; it deliberately does not
// carry the YAML bodies. The tool result lands in the model's context and
// is subject to the CLI's per-result size cap, and a real playbook is tens
// of KB per side, so inlining base + new would blow the cap and replace the
// JSON with an error string the chat UI can't parse. The frontend's diff
// card fetches both bodies from `GET /api/playbook-proposals/<id>` using
// the proposal_id.
type proposePlaybookDraftOut struct {
	ProposalID  string `json:"proposal_id"`
	PlaybookID  string `json:"playbook_id"`
	Type        string `json:"type"`                   // the type slot the draft was filed under
	BaseVersion string `json:"base_version,omitempty"` // empty when this is a brand-new id
	Why         string `json:"why,omitempty"`
	Message     string `json:"message"`
	// ValidationErrors lists structural validator failures when the supplied
	// YAML is malformed. Populated alongside an empty ProposalID; valid
	// proposals leave this nil. The agent inspects this field on the same
	// response shape rather than calling validate_playbook separately.
	ValidationErrors []string `json:"validation_errors,omitempty"`
}

// proposePlaybookDraft writes a draft to <userdir>/proposals/<id>__<proposal_id>.yaml
// and returns everything the launcher's chat UI needs to render a diff
// card with Approve / Decline buttons. NO operator confirmation is
// expected before this call: the agent calls it as soon as it has a
// candidate. Approval lives in the UI surface, not in the chat
// transcript.
//
// The draft does NOT enter the loaded playbook set; only an explicit
// approve via the launcher's API promotes it to a versioned file.
// Multiple proposals against the same id can coexist (each with its
// own proposal_id) — the UI renders one diff card per call, and the
// operator picks which (if any) to approve.
func (s *Server) proposePlaybookDraft(ctx context.Context, req *mcp.CallToolRequest, in proposePlaybookDraftIn) (*mcp.CallToolResult, proposePlaybookDraftOut, error) {
	if s.userPlaybooksDir == "" {
		return errorResult("playbook_proposal_draft is unavailable — strategies MCP started without a user playbooks dir (set $TRIAGENT_MCP_USER_PLAYBOOKS_DIR or use the triagent launcher)"), proposePlaybookDraftOut{}, nil
	}
	if strings.TrimSpace(in.YAML) == "" {
		return errorResult("yaml is required"), proposePlaybookDraftOut{}, nil
	}
	if strings.TrimSpace(in.Type) == "" {
		return errorResult("type is required — call playbook_types to see the available slots, then pick one (e.g. 'investigation' or 'general')"), proposePlaybookDraftOut{}, nil
	}
	if !playbookTypePattern.MatchString(in.Type) {
		return errorResult(fmt.Sprintf("invalid type %q (allowed: alphanumeric, _-, up to 64 chars)", in.Type)), proposePlaybookDraftOut{}, nil
	}
	pb, errs := ParseAndValidatePlaybookYAML([]byte(in.YAML))
	if len(errs) > 0 {
		return nil, proposePlaybookDraftOut{ValidationErrors: errs}, nil
	}

	// Drafts no longer carry a version stamp. The version field has been
	// removed from playbooks; git history is the version record. pb.Active
	// is still stripped below because active is NOT the agent's decision — it's decided at promote
	// time by the operator's approve-button choice. Strip it from the
	// canonical diff so the operator doesn't see the agent's value
	// and assume the AI is making that call.
	pb.Active = nil
	canonicalNew, err := RenderPlaybookYAML(pb)
	if err != nil {
		return errorResult(fmt.Sprintf("render canonical proposal yaml: %v", err)), proposePlaybookDraftOut{}, nil
	}

	proposalID := NewProposalID()
	if writeErrs, err := WriteProposalDraft(s.userPlaybooksDir, in.Type, proposalID, canonicalNew); err != nil {
		return errorResult(err.Error()), proposePlaybookDraftOut{}, nil
	} else if len(writeErrs) > 0 {
		return errorResult("validation failed: " + strings.Join(writeErrs, "; ")), proposePlaybookDraftOut{}, nil
	}

	// Report the currently loaded version (if any) so the agent knows
	// whether it proposed an update or a brand-new id.
	var baseVersion string
	if existing, ok := s.playbooks[pb.ID]; ok {
		baseVersion = existing.Version
	}

	msg := fmt.Sprintf("Proposal %s queued for %s/%s. The operator reviews + approves in the chat panel; nothing is loaded into the running playbook set until then.", proposalID, in.Type, pb.ID)
	if in.Why != "" {
		msg += " Justification: " + in.Why
	}
	return nil, proposePlaybookDraftOut{
		ProposalID:  proposalID,
		PlaybookID:  pb.ID,
		Type:        in.Type,
		BaseVersion: baseVersion,
		Why:         in.Why,
		Message:     msg,
	}, nil
}
