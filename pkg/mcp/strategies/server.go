// Package strategies implements the triagent-mcp `strategies` MCP server: a guided
// investigation walker that loads YAML playbooks at startup, opens
// per-investigation sessions, tracks findings the agent records, and suggests
// next steps. The walker is suggest-only — it never blocks the agent from
// calling other MCPs out of order or from advancing without recording all
// expected findings. Its value is structured playbook data, an audit trail,
// and not having to re-derive the decision tree from prompt prose.
package strategies

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

// Options configures the strategies server.
//
//   - SessionDir is where the walker snapshots state; if empty, state
//     is in-memory only.
//   - PluginPlaybooksDir is the launcher-managed clone of the upstream
//     playbooks repo (the domain library: cluster_health,
//     elasticsearch, …). User dirs override its entries.
//   - SystemPlaybooksDir is the launcher-bundled directory of locked
//     meta-playbooks (the master "investigation" entrypoint, the
//     playbook_proposal / followup_conversation / wiki_proposal
//     metas). Cannot be overridden by user dirs.
//   - UserPlaybooksDir is an optional directory the launcher writes
//     operator-customised playbooks to; entries there override or
//     extend the plugin set (but cannot touch the system set).
//
// DispatchModels is the dispatch-relevant subset of profile.Models, threaded
// in by the launcher. Defined here (not imported from internal/profile) to
// keep the MCP package layering clean.
type DispatchModels struct {
	Subagent string
}

type Options struct {
	SessionDir         string
	PluginPlaybooksDir string
	SystemPlaybooksDir string
	UserPlaybooksDir   string

	// Models picks the LLM models for sub-agent dispatches. Subagent is
	// honoured when any loaded playbook is dispatch: subagent. Empty falls
	// back to inheriting the parent.
	Models DispatchModels

	// SubAgentRunner runs the actual sub-agent. Defaults to subagent.Run
	// when nil; tests pass a stub. Decoupled so the strategies package
	// tests don't spawn a real claude.
	SubAgentRunner func(ctx context.Context, opts subagent.Options) (subagent.Result, error)

	// ParentSessionState returns the findings + most-recent summary of the
	// parent investigation session, for dispatch prompt assembly. Provided
	// by the launcher; nil falls back to empty inputs (dispatch still runs,
	// just with less context).
	ParentSessionState func(parentSessionID string) (findings map[string]any, summary string, ok bool)

	// MCPConfigPath is the launcher's per-session mcp.json. When set, the
	// strategies server forwards it to subagent.Run for dispatch-mode
	// playbooks so the sub-agent can reach the parent MCPs whose tools its
	// AllowedTools whitelist promises (mcp__triagent-strategies__*,
	// mcp__triagent-wiki__*). Empty leaves the sub-agent isolated to its
	// built-in tools — sufficient for dry runs and tests, but real
	// playbook_proposal / wiki_proposal dispatches will report missing
	// tools.
	MCPConfigPath string
}

// autoAdvanceMaxHops is the maximum number of pure-transition nodes the walker
// will transparently skip in a single step_complete call. Bounds malformed
// loops in pure-transition chains so the server never spins indefinitely.
const autoAdvanceMaxHops = 10

// Server is the strategies MCP server.
type Server struct {
	impl               *mcp.Server
	playbooks          map[string]*Playbook
	store              *store
	pluginPlaybooksDir string
	systemPlaybooksDir string
	userPlaybooksDir   string // empty disables playbook_proposal

	models             DispatchModels
	subAgentRunner     func(ctx context.Context, opts subagent.Options) (subagent.Result, error)
	parentSessionState func(parentSessionID string) (map[string]any, string, bool)
	mcpConfigPath      string
}

// New loads playbooks (plugin dir + system dir + any user dir on top)
// and returns a configured server ready to Run.
func New(opts Options) (*Server, error) {
	books, err := LoadPlaybooksFrom(opts.PluginPlaybooksDir, opts.SystemPlaybooksDir, opts.UserPlaybooksDir)
	if err != nil {
		return nil, fmt.Errorf("load playbooks: %w", err)
	}

	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-strategies",
		Version: "0.1.0",
	}, nil)

	s := &Server{
		impl:               impl,
		playbooks:          books,
		store:              newStore(opts.SessionDir),
		pluginPlaybooksDir: opts.PluginPlaybooksDir,
		systemPlaybooksDir: opts.SystemPlaybooksDir,
		userPlaybooksDir:   opts.UserPlaybooksDir,
	}
	s.models = opts.Models
	if opts.SubAgentRunner != nil {
		s.subAgentRunner = opts.SubAgentRunner
	} else {
		s.subAgentRunner = subagent.Run
	}
	s.parentSessionState = opts.ParentSessionState
	s.mcpConfigPath = opts.MCPConfigPath
	s.register()
	return s, nil
}

// Run serves MCP requests over stdio until the client disconnects or ctx is
// cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "list_playbooks",
		Description: "Return the available playbooks. Each entry has an id (use it for walk_playbook), a one-line symptom (the entry-point trigger), and a type. Pass type=\"investigation\" when triaging an operator-reported symptom or type=\"general\" for meta/workflow playbooks. Pass filter=\"<substring>\" to narrow by id or symptom (case-insensitive). Pass include_description=true when you need the longer prose (rarely needed for routing). Defaults are small — prefer narrow filters over full listings.",
	}, telemetry.Wrap("list_playbooks", s.listPlaybooks))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "playbook_types",
		Description: "Return the canonical list of playbook types with descriptions. Use this when you're unsure which type to filter list_playbooks by, or when authoring a new playbook and need to pick the right type for its YAML. The list is small (currently 'investigation' and 'general') and stable across sessions.",
	}, telemetry.Wrap("playbook_types", s.playbookTypes))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "walk_playbook",
		Description: "Open a new walker session for a given playbook. Returns the session_id (use it for the other tools), and the entrypoint step including its description, suggested_calls (concrete tool invocations on other MCPs to perform), and expected_findings (keys to record via step_complete once the call results are in). When this start is a handoff from another playbook's terminal node, also pass parent_session_id=<previous session_id>; the walker will reject circular handoffs so the agent can't loop between playbooks.",
	}, telemetry.Wrap("walk_playbook", s.walkPlaybook))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "get_state",
		Description: "Return the current state of an investigation: the current step, all findings recorded so far, all calls already performed, and the next-step options (each with a condition string the agent should evaluate against the findings to pick a branch).",
	}, telemetry.Wrap("get_state", s.getState))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "step_complete",
		Description: "Record findings and advance to the next node in one call. An unknown goto target rejects the whole call (no findings recorded). Pass findings: [] for a pure transition. The walker may transparently advance past nodes that have no agent-facing work (no expected_findings / suggested_calls / branches); the returned step.node_id is always your current position, and auto_advanced_through lists any skipped intermediate ids.",
	}, telemetry.Wrap("step_complete", s.stepComplete))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "summarize",
		Description: "Produce the formal markdown conclusion of an investigation. The agent passes the four operator-facing fields (symptom, likely root cause, evidence, recommended next steps) as concise prose — the launcher renders them inline in chat as the conclusion block, with session metadata (cluster / namespace / timestamps) appended in a small footer. Keep each field tight: this is what the operator pastes into a ticket. The activity panel is the audit trail; do NOT restate every tool call here.",
	}, telemetry.Wrap("summarize", s.summarize))

	// ── Playbook proposal surface (used by the playbook_proposal
	// meta-playbook the agent traverses post-summary). The agent SHOULD
	// reach these via walk_playbook { playbook_id: "playbook_proposal" }
	// rather than calling them directly — the meta-playbook encodes the
	// "should I propose?" decision criteria so they're visible + editable.
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "playbook_schema",
		Description: "Return the playbook YAML schema + authoring conventions as markdown. Read this once before drafting a proposed playbook so the YAML is structurally correct on the first try.",
	}, telemetry.Wrap("playbook_schema", s.playbookSchema))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "get_playbook_raw",
		Description: "Return the raw YAML for the currently-loaded playbook with the given id. Use it as the base when drafting a proposal that extends an existing playbook — modify the YAML and call playbook_proposal_draft with the SAME id.",
	}, telemetry.Wrap("get_playbook_raw", s.getPlaybookRaw))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "validate_playbook",
		Description: "Validate playbook YAML against the same checks the strategies MCP runs at load time (entrypoint resolves, every goto resolves, no empty descriptions, …). Returns ok=true OR a list of structural errors. Use iteratively while drafting a proposal.",
	}, telemetry.Wrap("validate_playbook", s.validatePlaybook))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "list_proposals",
		Description: "Return the current state of every known playbook proposal in this launcher: pending drafts (status=awaiting_review) plus recently-resolved ones (status=approved | declined). Each row carries proposal_id, playbook_id, type, at, and (for declines) the operator's note explaining the pushback. Call this BEFORE drafting a new proposal so you don't re-submit a shape the operator just declined — read the most recent declined row's note and adjust. Optional filters: playbook_id (only entries targeting this id), status (one of 'awaiting_review','approved','declined').",
	}, telemetry.Wrap("list_proposals", s.listProposals))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "playbook_proposal_draft",
		Description: "Submit a draft playbook for the operator's inline review. Writes the YAML to a draft store; the launcher's chat UI renders a diff card vs the currently-loaded version, and the operator approves or declines via that card. NO chat-side confirmation is required before this call — the agent should call it as soon as it has a candidate. On approve, the launcher writes the proposal body to <userDir>/<type>/<id>.yaml (overwriting any existing user file) and records a git commit in the user dir's repo with the operator's chosen message (or an auto-generated one). The version field is not stamped — git history is the version axis. Returns the proposal_id (operator-facing UI uses it) and base_yaml/new_yaml (for the diff view).",
	}, telemetry.Wrap("playbook_proposal_draft", s.proposePlaybookDraft))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "decline_proposal",
		Description: "The explicit terminal for a proposal flow that decides NOT to submit a draft (the work is below the bar). Call this with a one-line reason instead of ending with a prose summary — the dispatcher requires either playbook_proposal_draft / propose_wiki_draft (submit) or this (decline) to fire, so it can tell a deliberate no-proposal from a sub-agent that quit without finishing. Records the reason and returns acknowledged.",
	}, telemetry.Wrap("decline_proposal", s.declineProposal))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "playbook_resolve_entities",
		Description: "Canonicalise a candidate keyword set (services / errors / symptoms) against the union of all loaded playbooks' entity tags. **Call this BEFORE playbook_correlate** when you have specific keywords you want to map to canonical names — pass fuzzy guesses ('Zeebe Broker', 'crash looping', 'failing reconciliations') and the tool returns one Resolution per input telling you whether it was exact-match and which canonical names are close by edit distance / substring.\n\nUnlike playbook_correlate this tool does NOT validate input shape — pass fuzzy guesses as you have them. Returns one Resolution per input keyword: {field, input, exact, near}. Use `near[0]` as the canonical name to feed into playbook_correlate.\n\nLighter than dumping every loaded playbook's tags (which is what playbook_correlate's `resolution` field does as a side-effect on every call) — discrete canonicalize-then-correlate flow keeps the audit trail legible.",
	}, telemetry.Wrap("playbook_resolve_entities", s.playbookResolveEntities))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "playbook_correlate",
		Description: "Rank the loaded playbooks by entity overlap with a query set of canonical services / errors / symptoms (the same vocabulary wiki_correlate uses; pass entities you already lifted from findings). Returns metadata-only matches (id, symptom, description, type, score, match_path) — not the body, so this is cheap to call before deciding which playbook to walk. Score = 3 * direct hits + 1 * lifted hits, where 'lifted' means an entity tagged on a one-hop child (delegate_to / handoff target). The match_path tells you why a playbook scored: direct entities + per-lifted-entity 'via' child id. Empty input returns empty results — pass at least one entity. **Call playbook_resolve_entities first** to canonicalize fuzzy keyword guesses, then pass the canonical names here. Use this BEFORE list_playbooks when you have entities; fall back to list_playbooks only when correlate returns nothing or you want the full menu.",
	}, telemetry.Wrap("playbook_correlate", s.playbookCorrelate))
}

// ── tool handlers ────────────────────────────────────────────────────────────

type listPlaybooksIn struct {
	Type               string `json:"type,omitempty" jsonschema:"optional filter — 'investigation' (diagnostic playbooks for operator symptoms) or 'general' (meta/workflow). Omit for all."`
	Filter             string `json:"filter,omitempty" jsonschema:"optional case-insensitive substring matched against id + symptom. Empty returns the full set (after type filter)."`
	IncludeDescription bool   `json:"include_description,omitempty" jsonschema:"optional. When true, include the longer description prose for each entry. Default false — id + symptom + type only, which is ~5x smaller."`
}

type listPlaybooksOut struct {
	Playbooks []PlaybookSummary `json:"playbooks"`
}

func (s *Server) listPlaybooks(ctx context.Context, req *mcp.CallToolRequest, in listPlaybooksIn) (*mcp.CallToolResult, listPlaybooksOut, error) {
	return nil, listPlaybooksOut{
		Playbooks: summariesFiltered(s.playbooks, strings.TrimSpace(in.Type), strings.TrimSpace(in.Filter), in.IncludeDescription),
	}, nil
}

type playbookTypesIn struct{}

type playbookTypesOut struct {
	Types []PlaybookType `json:"types"`
}

func (s *Server) playbookTypes(ctx context.Context, req *mcp.CallToolRequest, _ playbookTypesIn) (*mcp.CallToolResult, playbookTypesOut, error) {
	// Source of truth is the system playbooks dir on disk: each
	// subdirectory is a type, and <type>/type.txt holds the
	// description. Falls through to an empty list when the system
	// dir isn't configured (tests, standalone smoke).
	types, err := LoadPlaybookTypes(s.pluginPlaybooksDir)
	if err != nil {
		return errorResult(fmt.Sprintf("read playbook types: %v", err)), playbookTypesOut{}, nil
	}
	return nil, playbookTypesOut{Types: types}, nil
}

type walkPlaybookIn struct {
	PlaybookID string `json:"playbook_id" jsonschema:"the playbook to start; one of the ids returned by list_playbooks"`
	ClusterID  string `json:"cluster_id" jsonschema:"the cluster id under investigation"`
	Namespace  string `json:"namespace" jsonschema:"the Kubernetes namespace bound to the k8s MCP for this session"`
	Notes      string `json:"notes,omitempty" jsonschema:"Context for this walk. For a dispatch-mode proposal playbook (wiki_proposal, playbook_proposal) this is CRITICAL and must be exhaustive: the dispatched sub-agent runs in a SEPARATE session with NO access to this investigation — your notes are its ENTIRE context. Write a complete, self-contained brief — the symptom, the key findings and evidence, the root cause and resolution, and the actual content that should end up in the artifact (entry sections and prose for a wiki entry; node descriptions, suggested_calls, and terminals for a playbook). Do not assume the sub-agent can see anything you have seen or summarised; if a detail belongs in the playbook/wiki, write it out here in full. For a non-dispatch playbook, a short incident summary plus what was tried is enough."`
	// ParentSessionID is set when this walk_playbook is the
	// follow-up to a terminal node's handoff. The walker uses it to
	// detect circular handoffs (A → B → A) and reject them. Always
	// pass it on a handoff; omit it on a fresh top-level investigation.
	// Skipping it on a handoff disables the loop guard for this hop.
	ParentSessionID    string `json:"parent_session_id,omitempty" jsonschema:"set this to the previous session_id when starting via a terminal node's handoff. The walker walks the handoff chain and rejects loops; pass it to opt in."`
	OperatorRefinement string `json:"operator_refinement,omitempty" jsonschema:"Optional. When the operator pushed back on a proposal (e.g. 'split into two entries'), pass their refinement so the dispatched sub-agent honours it. Ignored for default-dispatch playbooks."`
}

// DispatchedResult is what walk_playbook returns for dispatch: subagent
// playbooks instead of a stepView. The Summary is the sub-agent's final
// message — the agent reads it as "this is what the dispatched flow
// concluded".
//
// TimedOut / ExitCode / StderrTail are surfaced so the master agent can
// tell the difference between "subagent completed its walk" and "subagent
// was SIGKILL'd at the wall-clock cap". Without these, the master reads
// the partial summary as success and confabulates a proposal that was
// never actually submitted.
type DispatchedResult struct {
	Summary    string `json:"summary"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
	// ProposalOutcome is the verified terminal a proposal dispatch reached:
	// "submitted" (called *_proposal_draft), "declined" (called
	// decline_proposal), or "none" (ended without a terminal even after the
	// force-retries). Empty for non-proposal dispatches. The master reads
	// this so a missing proposal cannot be confabulated as a success.
	ProposalOutcome string `json:"proposal_outcome,omitempty"`
}

type walkPlaybookOut struct {
	SessionID           string            `json:"session_id"`
	Step                stepView          `json:"step"`
	AutoAdvancedThrough []string          `json:"auto_advanced_through,omitempty"`
	Dispatched          *DispatchedResult `json:"dispatched,omitempty"`
}

func (s *Server) walkPlaybook(ctx context.Context, req *mcp.CallToolRequest, in walkPlaybookIn) (*mcp.CallToolResult, walkPlaybookOut, error) {
	pb, ok := s.playbooks[in.PlaybookID]
	if !ok {
		return errorResult(fmt.Sprintf("unknown playbook_id %q; call list_playbooks for valid ids", in.PlaybookID)), walkPlaybookOut{}, nil
	}
	if pb.Dispatch == DispatchSubagent {
		res, outcome, err := s.runDispatch(ctx, pb, in.ParentSessionID, in.Notes, in.OperatorRefinement)
		if err != nil {
			return errorResult(fmt.Sprintf("dispatch %q: %v", pb.ID, err)), walkPlaybookOut{}, nil
		}
		summary := res.Summary
		if outcome == "none" {
			// Make the missing proposal impossible to read as success, even
			// for a master that ignores the structured ProposalOutcome.
			summary = "NO PROPOSAL WAS SUBMITTED — the sub-agent ended without calling a terminal tool (a *_proposal_draft or decline_proposal). Do not report this as a successful proposal.\n\n" + summary
		}
		return nil, walkPlaybookOut{Dispatched: &DispatchedResult{
			Summary:         summary,
			TimedOut:        res.TimedOut,
			ExitCode:        res.ExitCode,
			StderrTail:      res.StderrTail,
			ProposalOutcome: outcome,
		}}, nil
	}
	// cluster_id is required only for investigation-type playbooks (they
	// triage a specific cluster and reference ${cluster_id} in
	// suggested_calls). Meta-playbooks (the "system" / "general" slots
	// — wiki_proposal, wiki_backfill_ingestion, playbook_proposal, …)
	// run in editor / sub-flow contexts that have no cluster identity,
	// so the requirement is dropped for them. Investigation-side callers
	// always pass cluster_id, so they're unaffected.
	//
	// Note: pb.Type is derived from the type-slot directory name on
	// disk (loader sets Type=<dirname>), NOT from the YAML's `type:`
	// field (which is yaml:"-"). The investigate launcher uses "system"
	// as the meta slot; the test fixtures use "general". Both are
	// non-investigation and behave the same way here.
	if pb.Type == "investigation" && in.ClusterID == "" {
		return errorResult("cluster_id is required"), walkPlaybookOut{}, nil
	}

	// Loop guard: when this is a handoff (parent_session_id is set),
	// walk the handoff chain and reject re-entry into a playbook
	// already in the chain. The current session was reached by
	// traversing those playbooks, so re-entering one means the agent
	// is bouncing between the same playbooks rather than converging
	// on a root cause. Operators can break out of the guard
	// intentionally by starting a fresh top-level investigation
	// (omit parent_session_id) — that's the documented escape hatch.
	if in.ParentSessionID != "" {
		parent, err := s.store.get(in.ParentSessionID)
		if err != nil {
			return errorResult(fmt.Sprintf("parent_session_id %q not found; pass an existing session id or omit the field for a top-level investigation", in.ParentSessionID)), walkPlaybookOut{}, nil
		}
		chain := s.playbookChain(parent)
		for _, prior := range chain {
			if prior == pb.ID {
				return errorResult(fmt.Sprintf("circular handoff: playbook %q is already in this handoff chain (%s). The agent is bouncing between playbooks instead of converging — review the recorded findings, then either continue the current playbook (call get_state) or start a fresh top-level investigation (omit parent_session_id) if a re-entry is genuinely warranted.", pb.ID, strings.Join(append(chain, pb.ID), " → "))), walkPlaybookOut{}, nil
			}
		}
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:              newSessionID(),
		PlaybookID:      pb.ID,
		ParentSessionID: in.ParentSessionID,
		ClusterID:       in.ClusterID,
		Namespace:       in.Namespace,
		Notes:           in.Notes,
		CurrentNode:     pb.Entrypoint,
		Visited:         []string{pb.Entrypoint},
		Findings:        map[string]any{},
		RecordedCalls:   []RecordedCall{},
		StartedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.store.create(sess); err != nil {
		return errorResult(err.Error()), walkPlaybookOut{}, nil
	}

	node, err := findNode(pb, sess.CurrentNode)
	if err != nil {
		return errorResult(err.Error()), walkPlaybookOut{}, nil
	}
	return nil, walkPlaybookOut{SessionID: sess.ID, Step: renderStep(node, sess)}, nil
}

// playbookChain walks Session.ParentSessionID links and returns the
// list of playbook ids the agent traversed to reach `start`, ordered
// root → start (so the start playbook is the LAST element). Includes
// `start` itself. Cuts off at a missing parent or a cycle in the
// link itself (defensive — shouldn't happen, but a malformed disk
// snapshot shouldn't infinite-loop the walker).
func (s *Server) playbookChain(start *Session) []string {
	const maxDepth = 32 // chains this deep are pathological; bail
	seen := make(map[string]struct{}, 4)
	var chain []string
	cursor := start
	for i := 0; i < maxDepth && cursor != nil; i++ {
		if _, dup := seen[cursor.ID]; dup {
			break // self-cycle in the parent link; stop
		}
		seen[cursor.ID] = struct{}{}
		chain = append([]string{cursor.PlaybookID}, chain...) // prepend
		if cursor.ParentSessionID == "" {
			break
		}
		parent, err := s.store.get(cursor.ParentSessionID)
		if err != nil {
			break // dangling parent; treat as chain root
		}
		cursor = parent
	}
	return chain
}

type getStateIn struct {
	SessionID string `json:"session_id"`
}

type getStateOut struct {
	Session       Session  `json:"session"`
	Step          stepView `json:"step"`
	PlaybookTitle string   `json:"playbook_title,omitempty"`
}

func (s *Server) getState(ctx context.Context, req *mcp.CallToolRequest, in getStateIn) (*mcp.CallToolResult, getStateOut, error) {
	sess, err := s.store.get(in.SessionID)
	if err != nil {
		return errorResult(err.Error()), getStateOut{}, nil
	}
	pb, ok := s.playbooks[sess.PlaybookID]
	if !ok {
		return errorResult(fmt.Sprintf("playbook %q referenced by session no longer exists", sess.PlaybookID)), getStateOut{}, nil
	}
	node, err := findNode(pb, sess.CurrentNode)
	if err != nil {
		return errorResult(err.Error()), getStateOut{}, nil
	}
	return nil, getStateOut{Session: *sess, Step: renderStep(node, sess), PlaybookTitle: pb.Symptom}, nil
}

// DelegateReturn surfaces a popped delegate sub-flow's identity and
// terminal advice on the same advance response that resumes the
// parent. Lets the agent read the sub-flow's conclusion without an
// extra round-trip.
type DelegateReturn struct {
	From   string `json:"from"`
	Advice string `json:"advice,omitempty"`
}

// FindingEntry is one finding to record in a batch. Used by step_complete
// (and internally by the recordAndAdvance helper).
type FindingEntry struct {
	Key        string `json:"key"`
	Value      any    `json:"value"`
	SourceTool string `json:"source_tool,omitempty"`
}

// applyAdvance executes the transition from the session's current node to
// gotoID, handling delegate_to push, handoff-in-delegate rejection, and
// terminal auto-pop. Called after findings have been committed (if any).
// The session must already be loaded and the goto node must have been
// validated by the caller.
//
// Returns (*mcp.CallToolResult, stepView, *DelegateReturn, []string skipped, error).
// The skipped slice lists node ids the walker transparently advanced past
// (pure-transition nodes). Nil when no auto-advance occurred.
func (s *Server) applyAdvance(sess *Session, pb *Playbook, gotoID string) (*mcp.CallToolResult, stepView, *DelegateReturn, []string, error) {
	target, err := findNode(pb, gotoID)
	if err != nil {
		return errorResult(err.Error()), stepView{}, nil, nil, nil
	}

	// Push: advancing INTO a delegate_to node transparently switches the
	// walker to the delegate's entrypoint. The agent never sees the
	// delegate node itself as a "step"; it goes straight from "I picked
	// goto=walk_x" to "here's gather_x's entrypoint step".
	if target.DelegateTo != "" {
		delegate, ok := s.playbooks[target.DelegateTo]
		if !ok {
			return errorResult(fmt.Sprintf("delegate_to target %q is not a loaded playbook", target.DelegateTo)), stepView{}, nil, nil, nil
		}
		// Cycle guard: refuse to push a playbook id already on the stack
		// (or the active id, which the agent is delegating from).
		if delegate.ID == sess.PlaybookID {
			return errorResult(fmt.Sprintf("delegate_to cycle: playbook %q is already active", delegate.ID)), stepView{}, nil, nil, nil
		}
		for _, frame := range sess.CallStack {
			if frame.ParentPlaybookID == delegate.ID {
				return errorResult(fmt.Sprintf("delegate_to cycle: playbook %q is already in this delegate chain", delegate.ID)), stepView{}, nil, nil, nil
			}
		}
		entry, err := findNode(delegate, delegate.Entrypoint)
		if err != nil {
			return errorResult(fmt.Sprintf("delegate_to target %q has unresolvable entrypoint %q: %v", delegate.ID, delegate.Entrypoint, err)), stepView{}, nil, nil, nil
		}
		sess, err = s.store.update(sess.ID, func(s *Session) {
			s.CallStack = append(s.CallStack, DelegateFrame{
				ParentPlaybookID: s.PlaybookID,
				ReturnNodeID:     gotoID,
			})
			s.PlaybookID = delegate.ID
			s.CurrentNode = delegate.Entrypoint
			// Record both: the delegate node itself (visible in the audit
			// trail as the step that triggered the sub-flow) AND the
			// sub-flow's entrypoint (the actual next step the agent sees).
			s.Visited = append(s.Visited, gotoID, delegate.Entrypoint)
		})
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, nil, nil
		}
		return nil, renderStep(entry, sess), nil, nil, nil
	}

	// Reject: a delegated terminal that ALSO carries handoff is
	// disallowed — delegated playbooks must return. Surface this at
	// the terminal-reach time so the agent gets a clear signal rather
	// than silently double-jumping.
	if len(target.Next) == 0 && len(target.Handoff) > 0 && len(sess.CallStack) > 0 {
		return errorResult(fmt.Sprintf("delegated playbook %q cannot end in handoff (node %q has handoff=%v); delegated playbooks must return to the parent via terminal_advice only", sess.PlaybookID, gotoID, target.Handoff)), stepView{}, nil, nil, nil
	}

	// Decide whether this advance also pops a delegate frame. Folding
	// the pop into the SAME store.update as the normal advance keeps
	// the on-disk snapshot consistent: a crash mid-handler can never
	// leave the session at a terminal node with the stack still
	// populated (which would stall the agent on `--resume`).
	willPop := len(target.Next) == 0 && len(target.Handoff) == 0 && len(sess.CallStack) > 0
	var (
		poppedFrom   string
		poppedAdvice string
		parentNode   Node
	)
	if willPop {
		frame := sess.CallStack[len(sess.CallStack)-1]
		poppedFrom = sess.PlaybookID
		poppedAdvice = target.TerminalAdvice
		parentPB, ok := s.playbooks[frame.ParentPlaybookID]
		if !ok {
			return errorResult(fmt.Sprintf("delegate pop: parent playbook %q no longer loaded", frame.ParentPlaybookID)), stepView{}, nil, nil, nil
		}
		parentNode, err = findNode(parentPB, frame.ReturnNodeID)
		if err != nil {
			return errorResult(fmt.Sprintf("delegate pop: parent return node %q in %q not found: %v", frame.ReturnNodeID, frame.ParentPlaybookID, err)), stepView{}, nil, nil, nil
		}
	}

	sess, err = s.store.update(sess.ID, func(s *Session) {
		s.CurrentNode = gotoID
		s.Visited = append(s.Visited, gotoID)
		if willPop {
			frame := s.CallStack[len(s.CallStack)-1]
			s.CallStack = s.CallStack[:len(s.CallStack)-1]
			s.PlaybookID = frame.ParentPlaybookID
			s.CurrentNode = frame.ReturnNodeID
		}
	})
	if err != nil {
		return errorResult(err.Error()), stepView{}, nil, nil, nil
	}

	if willPop {
		return nil, renderStep(parentNode, sess), &DelegateReturn{
			From:   poppedFrom,
			Advice: poppedAdvice,
		}, nil, nil
	}

	// Auto-advance through pure-transition nodes.
	var skipped []string
	for hops := 0; hops < autoAdvanceMaxHops; hops++ {
		current, err := findNode(pb, sess.CurrentNode)
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, skipped, nil
		}
		if !isPureTransition(current) {
			return nil, renderStep(current, sess), nil, skipped, nil
		}
		skipped = append(skipped, current.ID)
		nextID := current.Next[0].Goto
		next, err := findNode(pb, nextID)
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, skipped, nil
		}
		// Disallow auto-advancing INTO delegate_to / handoff terminals —
		// those need the structured handling in the non-auto path.
		if next.DelegateTo != "" || len(next.Handoff) > 0 {
			return s.applyAdvance(sess, pb, nextID)
		}
		sess, err = s.store.update(sess.ID, func(sn *Session) {
			sn.CurrentNode = nextID
			sn.Visited = append(sn.Visited, nextID)
		})
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, skipped, nil
		}
	}
	return errorResult(fmt.Sprintf("walker exceeded auto-advance hop limit (%d) starting from %q in playbook %q — possible loop in pure-transition nodes",
		autoAdvanceMaxHops, sess.CurrentNode, pb.ID)), stepView{}, nil, skipped, nil
}

// recordAndAdvance applies a batch of findings and transitions to gotoID in
// one atomic update. Returns the rendered step view (plus DelegateReturn if
// popping a delegate frame, and the skipped slice from auto-advance). Used by
// step_complete.
//
// Atomicity contract: if gotoID is invalid, findings are NOT recorded.
func (s *Server) recordAndAdvance(sessionID string, findings []FindingEntry, gotoID string) (*mcp.CallToolResult, stepView, *DelegateReturn, []string, error) {
	sess, err := s.store.get(sessionID)
	if err != nil {
		return errorResult(err.Error()), stepView{}, nil, nil, nil
	}
	pb, ok := s.playbooks[sess.PlaybookID]
	if !ok {
		return errorResult(fmt.Sprintf("playbook %q referenced by session no longer exists", sess.PlaybookID)), stepView{}, nil, nil, nil
	}
	// Validate goto FIRST so an invalid call doesn't record findings.
	if gotoID != "" {
		if _, err := findNode(pb, gotoID); err != nil {
			return errorResult(err.Error()), stepView{}, nil, nil, nil
		}
	}
	for _, f := range findings {
		if f.Key == "" {
			return errorResult("finding key is required"), stepView{}, nil, nil, nil
		}
	}
	if len(findings) > 0 {
		sess, err = s.store.update(sessionID, func(sn *Session) {
			for _, f := range findings {
				sn.Findings[f.Key] = f.Value
				sn.RecordedCalls = append(sn.RecordedCalls, RecordedCall{
					Tool:       f.SourceTool,
					FindingKey: f.Key,
					At:         time.Now().UTC(),
				})
			}
		})
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, nil, nil
		}
	}
	if gotoID == "" {
		node, err := findNode(pb, sess.CurrentNode)
		if err != nil {
			return errorResult(err.Error()), stepView{}, nil, nil, nil
		}
		return nil, renderStep(node, sess), nil, nil, nil
	}
	return s.applyAdvance(sess, pb, gotoID)
}

type stepCompleteIn struct {
	SessionID string         `json:"session_id"`
	Findings  []FindingEntry `json:"findings,omitempty" jsonschema:"findings to record before transitioning; pass [] if none"`
	Goto      string         `json:"goto" jsonschema:"the node id to advance to; one of get_state's next_options[].goto"`
}

type stepCompleteOut struct {
	Step                stepView        `json:"step"`
	DelegateReturned    *DelegateReturn `json:"delegate_returned,omitempty"`
	AutoAdvancedThrough []string        `json:"auto_advanced_through,omitempty"`
}

func (s *Server) stepComplete(ctx context.Context, _ *mcp.CallToolRequest, in stepCompleteIn) (*mcp.CallToolResult, stepCompleteOut, error) {
	if strings.TrimSpace(in.Goto) == "" {
		return errorResult("step_complete requires goto; use get_state to list next_options"), stepCompleteOut{}, nil
	}
	res, step, deleg, skipped, err := s.recordAndAdvance(in.SessionID, in.Findings, in.Goto)
	return res, stepCompleteOut{Step: step, DelegateReturned: deleg, AutoAdvancedThrough: skipped}, err
}

type summarizeIn struct {
	SessionID  string `json:"session_id" jsonschema:"the active investigation session id (from walk_playbook)"`
	Symptom    string `json:"symptom" jsonschema:"Slack-shareable TL;DR of the user-facing symptom — what the operator brought you, normalised. TWO SENTENCES MAX, simple past, active voice, each sentence under 25 words, NO bullets, NO log-line citations, NO timestamps (those belong in evidence). e.g. 'ZeebeClusterUnhealthy fired on prod-gke-us-east1-worker-2 for ZeebeCluster <id>. The CR reported Ready=False for elasticsearch and the Operate, Tasklist, and Optimize webapps, while the brokers and gateway stayed Available.'"`
	RootCause  string `json:"root_cause" jsonschema:"Slack-shareable TL;DR of the likely root cause as plain prose. Name the offending component / commit / change. TWO TO THREE SENTENCES MAX, simple past, active voice, each sentence under 25 words, NO bullets, NO log-line citations, NO embedded timestamps — bullets, log lines, file:line, sha, and condition reasons all belong in evidence. e.g. 'Commit 3c602a58 in example-service (PR #4525) swapped the parameters of PortForwardService. The rebalance subcommand passed (namespace, serviceName) in the Forwarder order, so the port-forward never bound. The POST hung until the 30s client timeout fired.'"`
	Evidence   string `json:"evidence" jsonschema:"reviewer-facing proof. Markdown bullets enumerating the concrete signals supporting the root cause — log lines, conditions, commits, diffs, timestamps. Cite specifics (file:line, commit sha, condition reason). Each bullet one line. This renders as a separate card from the verdict, so put EVERYTHING citation-shaped here — symptom and root_cause stay prose-only."`
	NextSteps  string `json:"next_steps" jsonschema:"markdown bullets with what the operator does next: revert / hotfix / config change / hand off to team X. One imperative sentence per bullet, condition first when there is one ('If the pod restarts again, ...'). No hedge phrases, no 'should'."`
	Confidence string `json:"confidence,omitempty" jsonschema:"optional one-line confidence note: 'High — diff scope is 2 files, fix branch already drafted' / 'Medium — symptom matches but the failing pod was GC'd before logs could be pulled.' Omit for high-confidence calls where the evidence speaks for itself."`
}

type summarizeOut struct {
	// Markdown is the operator-facing verdict — symptom, root cause,
	// next steps, confidence, and the session footer. Slack-shareable;
	// kept tight on purpose.
	Markdown string `json:"markdown"`
	// EvidenceMarkdown is the reviewer-facing evidence body, rendered
	// as a sibling card on the frontend. Empty when the agent passed
	// no evidence — the frontend uses empty as "don't render".
	EvidenceMarkdown string `json:"evidence_markdown,omitempty"`
}

// summarize formats the agent's distilled conclusion as the formal
// session conclusion. The agent provides the four operator-facing
// fields (symptom, root cause, evidence, next steps) — the launcher
// adds session metadata (cluster, namespace, timestamps) as a small
// footer.
//
// The previous shape templated the playbook's node descriptions + a
// per-call audit trail into the summary, which was fine for short
// playbooks but produced multi-page dumps for prose-heavy meta-
// playbooks like git_inspect where every node carries paragraphs of
// operator instructions. The activity panel is the audit trail —
// summarize is the conclusion the operator reads.
func (s *Server) summarize(ctx context.Context, req *mcp.CallToolRequest, in summarizeIn) (*mcp.CallToolResult, summarizeOut, error) {
	sess, err := s.store.get(in.SessionID)
	if err != nil {
		return errorResult(err.Error()), summarizeOut{}, nil
	}
	if strings.TrimSpace(in.Symptom) == "" || strings.TrimSpace(in.RootCause) == "" {
		return errorResult("summarize requires non-empty symptom and root_cause — write the conclusion the operator should read, don't pass placeholders"), summarizeOut{}, nil
	}

	// Verdict body — symptom / root cause / next steps / confidence
	// + session footer. Evidence is split into its own body so the
	// verdict stays Slack-shareable; the frontend renders evidence
	// as a sibling card.
	var b strings.Builder
	b.WriteString("## Symptom\n\n")
	b.WriteString(strings.TrimSpace(in.Symptom))
	b.WriteString("\n\n## Likely root cause\n\n")
	b.WriteString(strings.TrimSpace(in.RootCause))
	if strings.TrimSpace(in.NextSteps) != "" {
		b.WriteString("\n\n## Recommended next steps\n\n")
		b.WriteString(strings.TrimSpace(in.NextSteps))
	}
	if strings.TrimSpace(in.Confidence) != "" {
		b.WriteString("\n\n## Confidence\n\n")
		b.WriteString(strings.TrimSpace(in.Confidence))
	}

	// Small session-metadata footer so the operator can recover the
	// session context from the summary alone — but no per-step or
	// per-call dumps. The activity panel + visited-list in get_state
	// are the audit trail.
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "_Session: cluster `%s`", sess.ClusterID)
	if sess.Namespace != "" {
		fmt.Fprintf(&b, " · namespace `%s`", sess.Namespace)
	}
	fmt.Fprintf(&b, " · started %s · concluded %s_\n",
		sess.StartedAt.Format(time.RFC3339),
		sess.UpdatedAt.Format(time.RFC3339))

	var evidence string
	if strings.TrimSpace(in.Evidence) != "" {
		evidence = "## Evidence\n\n" + strings.TrimSpace(in.Evidence)
	}

	return nil, summarizeOut{Markdown: b.String(), EvidenceMarkdown: evidence}, nil
}

// errorResult formats an error so the MCP protocol returns it as a tool-level
// error message rather than a transport-level failure.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
