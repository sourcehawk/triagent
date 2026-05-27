package wiki

import (
	"context"
	"fmt"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the wiki MCP server.
type Options struct {
	// VaultPath is the absolute path to the local checkout of the
	// investigations wiki git repo. Required.
	VaultPath string
	// ProposalsPath is the local directory where in-flight drafts
	// live. Auto-created on first use if missing. Required.
	ProposalsPath string
	// ClaudeBinary is the path to `claude` for the propose_wiki_draft
	// sub-agent. Empty falls back to `claude` on $PATH.
	ClaudeBinary string
	// SubAgentTimeout caps the propose_wiki_draft sub-agent's
	// wallclock. Zero falls back to subagent.Run's 90s default.
	SubAgentTimeout time.Duration
}

// Server holds the MCP server and its configuration.
type Server struct {
	impl            *mcp.Server
	vaultPath       string
	proposalsPath   string
	claudeBin       string
	subAgentTimeout time.Duration

	// runSubAgent is the seam for testing. Production wires it to
	// subagent.Run; tests replace it with a stub. Keeps the rest of
	// the package from depending on the subagent package directly.
	runSubAgent func(ctx context.Context, prompt, parentToolID string) (string, error)
}

// New constructs a Server.
func New(opts Options) (*Server, error) {
	if opts.VaultPath == "" {
		return nil, fmt.Errorf("VaultPath is required")
	}
	if opts.ProposalsPath == "" {
		return nil, fmt.Errorf("ProposalsPath is required")
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-wiki",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:            impl,
		vaultPath:       opts.VaultPath,
		proposalsPath:   opts.ProposalsPath,
		claudeBin:       opts.ClaudeBinary,
		subAgentTimeout: opts.SubAgentTimeout,
	}
	s.runSubAgent = func(ctx context.Context, prompt, parentToolID string) (string, error) {
		res, err := subagent.Run(ctx, subagent.Options{
			ClaudeBinary: s.claudeBin,
			WorkingDir:   s.vaultPath,
			AllowedTools: "Read,Glob,Grep,Write,Edit",
			Prompt:       prompt,
			Timeout:      s.subAgentTimeout,
			ParentToolID: parentToolID,
		})
		if err != nil {
			return res.Summary, err
		}
		return res.Summary, nil
	}
	s.register()
	return s, nil
}

// Run serves MCP requests over stdio.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// errorResult formats a tool-level error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// register wires every tool the wiki server exposes. Each handler is
// wrapped in telemetry.Wrap so the launcher's activity panel sees the
// real call boundaries.
func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "propose_wiki_draft",
		Description: "Draft a wiki entry for the current investigation. Spawns a focused sub-agent that writes a schema-conformant markdown file to the user's local proposals dir; the launcher's UI surfaces a review/approve/decline card. The orchestrator supplies slug + summary + status + 0..4 link URLs (investigation_url / incidentio_url / slack_channel_url / slack_message_url); the sub-agent writes the body and entity stubs.",
	}, telemetry.Wrap("propose_wiki_draft", s.proposeWikiDraft))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "wiki_search",
		Description: "Free-text + filter search over the investigations wiki. **Primary audience: the wiki-proposal flow** — when drafting a new incident note via propose_wiki_draft (walked through the `wiki_proposal` meta-playbook), use wiki_search to find similar prior entries by title / body keyword so the new draft can model after them and avoid duplicating an existing entry.\n\nFor live investigation recall (\"have we seen this symptom before?\") prefer wiki_correlate — it ranks by entity-overlap, which is the right signal at triage time. wiki_search is the by-keyword counterpart for curation, not the opening move at investigation time.\n\nFilters: services / errors / symptoms (entity names), severity (sev1|sev2|sev3), status (resolved|open|wontfix). Filters AND across fields, OR within. Empty query returns filter-only matches.\n\n**Entity-name filters are EXACT-MATCH** against canonical lowercase-with-hyphens names (regex ^[a-z0-9][a-z0-9-]*$). Passing 'Zeebe Broker', 'broker', or 'zeebe_broker' will NOT find 'zeebe-broker' — call wiki_resolve_entities first to canonicalize fuzzy guesses (or wiki_list_entities to browse the full vault). Malformed names return a structured error (not silent empty results) so retry with the right shape.\n\nReturns hits AND a `resolution` field that explains how each entity-name filter mapped against the vault — when an input didn't exact-match, `resolution[].near` lists close canonical names worth retrying with.",
	}, telemetry.Wrap("wiki_search", s.wikiSearch))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "wiki_get",
		Description: "Fetch a single wiki note by vault-relative path. For incident notes, returns parsed frontmatter; for entity notes, returns backlinks (incidents that reference this entity).",
	}, telemetry.Wrap("wiki_get", s.wikiGet))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "wiki_list_entities",
		Description: "List entity stubs in the wiki, optionally filtered by type (service|error|symptom|component). Names are lowercase-with-hyphens (e.g. 'zeebe-broker'). **Call this BEFORE wiki_search / wiki_correlate** so subsequent calls use canonical names — those tools are exact-match and silently miss 'Zeebe Broker' / 'broker' / 'zeebe_broker' guesses. Includes incident_count per entity. For a targeted lookup of specific keyword guesses (rather than dumping the whole vault), use wiki_resolve_entities instead.",
	}, telemetry.Wrap("wiki_list_entities", s.wikiListEntities))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "wiki_resolve_entities",
		Description: "Canonicalise a candidate keyword set against the vault's known entity names. **Targeted lookup tool — call this BEFORE wiki_search / wiki_correlate** when you have specific keywords you want to map to canonical names. Unlike wiki_list_entities (which dumps every entity), this takes a small set of guesses and returns one `resolution` per (field, input) telling you whether each was an exact match or which canonical names are close by edit distance / substring.\n\nUnlike wiki_search / wiki_correlate this tool does NOT validate input shape — pass fuzzy guesses ('Zeebe Broker', 'oom kill', 'crash looping') as you have them. Returns one Resolution per input keyword: {field, input, exact, near}. Use `near[0]` as the canonical name to retry with.\n\nLighter than wiki_list_entities when you already have candidate keywords; falls back to wiki_list_entities for full enumeration when you're browsing the vault.",
	}, telemetry.Wrap("wiki_resolve_entities", s.wikiResolveEntities))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "wiki_correlate",
		Description: "Find prior incidents that share entities with the current findings, ranked by overlap. **Primary audience: live investigation recall** — given a small candidate set of services / errors / symptoms (lifted from the operator's symptom description), returns the top N most-correlated past incidents with what was tried and what fixed them. Strong starting move at the beginning of an investigation, before deeper k8s/log inspection: a prior incident with overlapping entities often tells you what to look for.\n\nFor wiki curation (finding similar entries to model a new draft after) use wiki_search instead — keyword/title hits are the right signal there. Default limit is 5; pass at least one candidate entity (empty input returns empty). Score = total entity overlap across services/errors/symptoms; OR semantics across fields.\n\n**Entity names are EXACT-MATCH** against canonical lowercase-with-hyphens names (regex ^[a-z0-9][a-z0-9-]*$). Call wiki_resolve_entities first to canonicalize fuzzy guesses (or wiki_list_entities to browse), or walk the `wiki_recall` meta-playbook for the full canonicalize-then-correlate flow. Malformed names return a structured error.\n\nReturns correlations AND a `resolution` field that explains how each input keyword mapped against the vault — when an input didn't exact-match, `resolution[].near` lists close canonical names worth retrying with.",
	}, telemetry.Wrap("wiki_correlate", s.wikiCorrelate))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "validate_wiki",
		Description: "Validate wiki markdown against the canonical schema. Auto-detects incident notes (frontmatter has `id:`) vs entity stubs (frontmatter has `type:`+`name:`). Returns {ok, kind, errors[]}. Use before calling propose_wiki_draft to sanity-check a manually-composed draft, or to lint an existing wiki entry pulled via wiki_get. Cheap and deterministic — no model cost.",
	}, telemetry.Wrap("validate_wiki", s.validateWiki))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "wiki_schema",
		Description: "Return the wiki frontmatter schema + authoring conventions as markdown. Read this before proposing or editing a wiki entry so the YAML is structurally correct on the first try.",
	}, telemetry.Wrap("wiki_schema", s.wikiSchema))
}
