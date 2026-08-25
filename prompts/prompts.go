// Package prompts assembles the opening prompt the Claude launcher sends
// at investigation start. Prompt content comes from the profile; session
// substitutions come from Env.
package prompts

import (
	"strconv"
	"strings"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/repos"
)

// Env holds the per-session substitutions.
type Env struct {
	Context   string
	UserNotes string // kept for callers that read it; Build reads notes from InputValues
	// InputValues carries operator-supplied values for the profile's
	// InvestigationInputs, keyed by input ID then by template field
	// (e.g. {"cluster_id": {"value": "abc"}, "slack_channel": {"id": "C1", ...}}).
	InputValues            map[string]map[string]any
	// SlackMCPAvailable reports whether the triagent-slack MCP server is wired
	// in this session (operator pasted a channel id AND a slack token is
	// configured on the launcher's connections panel). The agent uses
	// this — exposed in the Environment block as `mcp__triagent-slack__*` —
	// to decide whether the gather_slack_context sub-playbook can run.
	SlackMCPAvailable bool
	// IncidentioMCPAvailable reports whether the triagent-incidentio MCP server
	// is wired (operator pasted an incident URL parseable to a ref AND
	// an incident.io token is configured). Same Environment-block
	// surfacing pattern as SlackMCPAvailable.
	IncidentioMCPAvailable bool
	LinkedRepos            []repos.LinkedRepo // GitHub repos available as mcp__triagent-git-<alias>__*
	// OriginatingSignalSet is true when this investigation was spawned
	// from a signal-watch (autoStart or manual). The opening prompt
	// includes a capture_offer hint nudging the agent toward `wiki` for
	// known-noop outcomes so the ingestion agent can dismiss similar
	// signals on the next poll.
	OriginatingSignalSet bool
}

// incidentioRefFromURL extracts the trailing path segment of an
// incident.io dashboard URL (https://app.incident.io/<org>/incidents/<ref>)
// for prompt substitution. Returns "" for non-matching URLs; the caller
// then writes a generic "derived from the URL" hint instead. Kept local
// to prompts to avoid an import cycle with the editor package.
func incidentioRefFromURL(raw string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(raw), "/"), "/")
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i-1] == "incidents" && parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}

// orUnset renders empty values as <unset> so the parameter block stays
// greppable: every key is present, and Claude can distinguish "no value
// set" from "Environment block missing".
func orUnset(s string) string {
	if s == "" {
		return "<unset>"
	}
	return s
}

// inputStr returns a string field from the InputValues map. Returns "" when
// the input ID or field key is absent, or when the value is not a string.
func inputStr(iv map[string]map[string]any, inputID, field string) string {
	if iv == nil {
		return ""
	}
	ctx, ok := iv[inputID]
	if !ok {
		return ""
	}
	v, ok := ctx[field]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Build assembles the opening prompt the Claude CLI receives via -p.
// Profile-supplied prompt sections (system / architecture / strategies)
// and suggested-playbook IDs come from the profile. The parameter block
// is a static prefix (kubernetes-context + suggested-*-playbook) followed
// by a dynamic section walked from profile.InvestigationInputs via
// profile.RenderPromptKeys. The post-Environment content (tool bullets,
// LinkedRepos block, Incident identifiers) is rendered from InputValues.
func Build(env Env, prof *profile.Profile) string {
	var b strings.Builder
	b.WriteString(prof.Prompts["system.md"])
	b.WriteString("\n\n## Cluster architecture\n")
	b.WriteString(prof.Prompts["architecture.md"])
	b.WriteString("\n\n## Investigation strategies\n")
	b.WriteString(prof.Prompts["strategies.md"])
	b.WriteString("\n\n## Environment\n")
	b.WriteString("```\n---\n")

	// Static prefix.
	b.WriteString("kubernetes-context: ")
	b.WriteString(orUnset(env.Context))
	b.WriteString("\nsuggested-entrypoint-playbook: ")
	b.WriteString(prof.Playbooks.Entrypoint)
	b.WriteString("\nsuggested-closing-playbook: ")
	b.WriteString(prof.Playbooks.Closing)
	b.WriteString("\n")

	// Dynamic section: walk profile inputs in declaration order.
	for _, in := range prof.InvestigationInputs {
		ctx, ok := env.InputValues[in.ID]
		if !ok {
			// No operator-supplied value for this input; skip its keys.
			continue
		}
		if ctx == nil {
			ctx = map[string]any{}
		}
		keys, err := profile.RenderPromptKeys(in, ctx)
		if err != nil {
			// Profile-author error; surface in the block so it's visible.
			b.WriteString("# render-failed[")
			b.WriteString(in.ID)
			b.WriteString("]: ")
			b.WriteString(err.Error())
			b.WriteString("\n")
			continue
		}
		for _, k := range keys {
			b.WriteString(k.Key)
			b.WriteString(": ")
			b.WriteString(k.Value)
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n```\n")
	if env.OriginatingSignalSet {
		b.WriteString("\n> **Auto-triggered investigation — capture_offer hint:**  ")
		b.WriteString("This investigation was auto-triggered by signal-watch ingestion. ")
		b.WriteString("If the conclusion is a known false-positive / noop, choose `wiki` ")
		b.WriteString("at capture_offer time (not `no`) — the entry lets the ingestion ")
		b.WriteString("agent dismiss similar signals automatically next time. ")
		b.WriteString("Author the wiki entry with `status: wontfix` and include enough ")
		b.WriteString("symptom keywords for `wiki_correlate` to find it.\n")
	}
	b.WriteString("- Investigation playbooks: mcp__triagent-strategies__*   (start with `walk_playbook` against the `suggested-entrypoint-playbook` from the parameter block; walk `suggested-closing-playbook` after every `summarize`)")
	b.WriteString("\n- Cluster-inspection tools: mcp__triagent-k8s__*   (read-only: list_resource_kinds, list_resources, get_resource, get_logs, list_events, list_namespaces, trace_crossplane). Pass `namespace` per call; default to `cluster-resource-namespace` from the parameter block, or call `list_namespaces` if it is `<unset>`.")
	if env.IncidentioMCPAvailable {
		b.WriteString("\n- incident.io tools: mcp__triagent-incidentio__*   (incidentio_get_incident, incidentio_get_timeline, incidentio_get_postmortem, incidentio_search_related). Pass `incident_id` on every call.")
	}
	if env.SlackMCPAvailable {
		b.WriteString("\n- Slack tools: mcp__triagent-slack__*   (slack_get_channel_id, slack_channel_overview, slack_search_messages, summarize_thread, analyze_channel). Pass `channel_id` on every channel-aware call; resolve a channel name with slack_get_channel_id first.")
	}
	for _, m := range prof.ExtraMCPs {
		b.WriteString("\n- mcp__")
		b.WriteString(m.Alias)
		b.WriteString("__*: ")
		b.WriteString(strings.TrimSpace(m.Description))
	}
	if len(env.LinkedRepos) > 0 {
		b.WriteString("\n\n## Linked repositories\n")
		b.WriteString("Each linked GitHub repo is exposed via its own MCP server. **Always call `mcp__triagent-git-<alias>__get_repo_architecture_summary` first** when investigating a question that touches the repo — it's a cached digest the launcher generated upfront, free at retrieval, and falls through to the description below when no summary is yet cached. Discovery tools (`latest_tags`, `commit_summary`, `diff_summary`, `search_log`) are cheap deterministic git plumbing; sub-agent tools spawn a focused sub-Claude in the cloned repo and return a summary so this session's context stays clean: `research_codebase` answers questions about the code as a whole at a ref (exact metric names, CRD conditions, flags, alert rules), `analyze_change` explains one specific commit, `correlate_with_findings` ranks recent commits against symptoms. Don't use `analyze_change` with `ref=HEAD` to ask whole-repo questions — that's `research_codebase`. Tools are addressable as `mcp__triagent-git-<alias>__<tool>`.\n")
		for _, r := range env.LinkedRepos {
			b.WriteString("- `")
			b.WriteString(r.EffectiveAlias())
			b.WriteString("` — ")
			b.WriteString(r.Owner)
			b.WriteString("/")
			b.WriteString(r.Name)
			if r.Description != "" {
				b.WriteString(" — ")
				b.WriteString(r.Description)
			}
			b.WriteString("\n")
		}
	}

	incidentURL := inputStr(env.InputValues, "incident_url", "value")
	slackChannelURL := inputStr(env.InputValues, "slack_channel", "url")
	slackChannelID := inputStr(env.InputValues, "slack_channel", "id")
	slackChannelName := inputStr(env.InputValues, "slack_channel", "name")
	userNotes := inputStr(env.InputValues, "notes", "value")

	hasIncidentScope := incidentURL != "" || slackChannelURL != "" || slackChannelID != ""
	if hasIncidentScope {
		b.WriteString("\n\n## Incident identifiers\n")
		b.WriteString("The operator supplied these at investigation start.\n")
		b.WriteString("- For `mcp__triagent-wiki__propose_wiki_draft`: pass `slack_link` and `incidentio_link` verbatim. The required `slug` argument is a free-form lowercase-with-hyphens filename slug (`inc-` for incident.io tickets, `inv-` for investigation-only, `alert-` for alerts/Slack threads — the orchestrator picks the right prefix) — derive it from the strongest source plus a kebab-case description.\n")
		if env.IncidentioMCPAvailable && incidentURL != "" {
			ref := incidentioRefFromURL(incidentURL)
			b.WriteString("- For `mcp__triagent-incidentio__*`: pass `incident_id`")
			if ref != "" {
				b.WriteString("=`")
				b.WriteString(ref)
				b.WriteString("` (parsed from the URL below)")
			} else {
				b.WriteString(" (numeric reference or UUID derived from the URL below)")
			}
			b.WriteString(".\n")
		}
		if env.SlackMCPAvailable && slackChannelID != "" {
			b.WriteString("- For `mcp__triagent-slack__*`: pass `channel_id`=`")
			b.WriteString(slackChannelID)
			b.WriteString("`.\n")
		}
		if incidentURL != "" {
			b.WriteString("- incidentio_link: ")
			b.WriteString(incidentURL)
			b.WriteString("\n")
		}
		if slackChannelURL != "" {
			b.WriteString("- slack_link: ")
			b.WriteString(slackChannelURL)
			b.WriteString("\n")
			if slackChannelName != "" {
				b.WriteString("- slack_channel_name: #")
				b.WriteString(slackChannelName)
				b.WriteString("\n")
			}
		} else if slackChannelID != "" {
			b.WriteString("- slack_channel_id: ")
			b.WriteString(slackChannelID)
			b.WriteString("\n")
			if slackChannelName != "" {
				b.WriteString("- slack_channel_name: #")
				b.WriteString(slackChannelName)
				b.WriteString("\n")
			}
			b.WriteString("- (no Slack URL available for this investigation; pass slack_link omitted to propose_wiki_draft)\n")
		}
	}
	b.WriteString("\n## User-supplied context\n")
	if strings.TrimSpace(userNotes) == "" {
		b.WriteString("(none provided — start by asking the operator what they observed, or explore broadly.)")
	} else {
		b.WriteString(userNotes)
	}
	b.WriteString("\n\nBegin by forming a hypothesis from the user-supplied context (or ask a single clarifying question if empty), then gather targeted evidence using the MCP tools. Produce a final summary when done.")
	return b.String()
}

// Subject identifies what an editor session is editing. Implementations
// carry kind-specific data the prompt builder needs. The launcher's
// editor.Session embeds a Subject; the prompt builder dispatches on its
// dynamic type to pick the right template and substitution set.
type Subject interface {
	// SubjectKind returns "playbook" or "wiki". Used by the manager
	// for keying and by the prompt builder as the discriminator.
	SubjectKind() string

	// Key returns a globally-unique resume key. The manager looks up
	// existing sessions by this string when the drawer reattaches.
	Key() string
}

// PlaybookSubject names a single playbook (id + version) plus the
// snapshot YAML the agent edits.
type PlaybookSubject struct {
	ID      string
	Version string
	Type    string // type slot (e.g. "investigation")
	YAML    string // current YAML, immutable for the session lifetime
}

func (s PlaybookSubject) SubjectKind() string { return "playbook" }
func (s PlaybookSubject) Key() string         { return "playbook:" + s.ID + "@" + s.Version }

// WikiSubject names one wiki entry — either an "entry" (a top-level
// wiki entry under entries/<slug>.md) or an "entity" (<type>/<name>).
// The existing markdown body, when present, is included so the agent
// can revise rather than redraft from scratch.
type WikiSubject struct {
	Kind             string // "entry" | "entity"
	ID               string // "inc-12345-broker-ooms" or "service/zeebe-broker"
	ExistingMarkdown string // empty for new entries
}

func (s WikiSubject) SubjectKind() string { return "wiki" }
func (s WikiSubject) Key() string         { return "wiki:" + s.Kind + ":" + s.ID }

// Sources are the operator-attached scope for an editor session: the
// specific Slack channel and/or incident.io incident the operator wants
// the agent to treat as primary evidence. The MCP wiring no longer keys
// off these fields — the launcher wires the slack/incidentio MCPs
// whenever the matching tokens are linked, and the agent threads the
// scope through per-tool-call args. The prompt builder consumes Sources
// to narrate "your scoped channel is X / incident is Y; pass these as
// channel_id / incident_id" to the agent.
type Sources struct {
	InvestigationID  string // launcher session id (UUID); empty when no investigation attached
	InvestigationDir string // absolute path to the investigation's session dir (events.jsonl + metadata.json)
	IncidentioURL    string
	IncidentioRef    string // parsed numeric/UUID derived from URL
	SlackChannelID   string
	SlackChannelName string
	SlackSinceUnix   int64
}

// HasSlack reports whether the operator attached a specific Slack
// channel to this session. Drives prompt narrative — NOT MCP wiring.
func (s Sources) HasSlack() bool { return s.SlackChannelID != "" }

// HasIncidentio reports whether the operator attached a specific
// incident.io incident to this session. Drives prompt narrative — NOT
// MCP wiring.
func (s Sources) HasIncidentio() bool { return s.IncidentioRef != "" }

// HasInvestigation reports whether the operator attached a specific
// investigation session to this editor session. Drives prompt
// narrative — the agent is pointed at the session's events.jsonl as
// primary evidence when drafting a wiki entry.
func (s Sources) HasInvestigation() bool { return s.InvestigationID != "" }

// BaseEnv holds the per-session inputs that aren't subject-specific:
// linked-repo MCP set, full tool catalog, the Sources block
// (operator-attached scope), and which MCPs are wired.
type BaseEnv struct {
	LinkedRepos    []repos.LinkedRepo // each → mcp__triagent-git-<alias>__*
	ToolCatalog    []ToolCatalogEntry // full triagent-mcp catalog for `suggested_calls` references
	Sources        Sources
	// SlackMCPAvailable is true when triagent-slack is registered in the
	// session's mcp.json (slack token linked). Independent of whether
	// Sources.HasSlack() is true: an operator can have the token linked
	// without having pinned a specific channel for this session.
	SlackMCPAvailable bool
	// IncidentioMCPAvailable mirrors SlackMCPAvailable for triagent-incidentio.
	IncidentioMCPAvailable bool
}

// ToolCatalogEntry is one tool in the triagent-mcp catalog as exposed to
// the editor agent. Mirrors the on-the-wire shape of the launcher's
// in-process tool catalog (internal/server/meta.go), minus the
// per-input description prose
// (kept tight for the prompt budget — the agent's playbook-authoring
// job needs tool name + arg names + a one-line description, not full
// schemas).
type ToolCatalogEntry struct {
	Server      string
	Name        string
	Description string
	Inputs      []ToolCatalogInput
}

// ToolCatalogInput is one arg of a tool. Required flag mirrors the
// MetaTool input shape so the prompt can mark which args are
// mandatory at call time.
type ToolCatalogInput struct {
	Name     string
	Required bool
}

// BuildEditor dispatches on the subject's dynamic type to render the
// right system prompt. This is the single entrypoint editor.Session
// uses; the dispatch keeps the editor package free of prompt-template
// concerns. Profile-supplied content (editor.md / wiki_editor.md) comes
// from prof.Prompts.
func BuildEditor(subject Subject, env BaseEnv, prof *profile.Profile) string {
	switch s := subject.(type) {
	case PlaybookSubject:
		return buildPlaybookEditor(s, env, prof)
	case WikiSubject:
		return buildWikiEditor(s, env, prof)
	default:
		// Defensive fallback: an unknown subject still returns a
		// well-formed prompt rather than panicking. Real callers
		// pick from the two known kinds; the failure mode is a
		// programming error in editor.go, not an operator action.
		return prof.Prompts["editor.md"]
	}
}

func buildPlaybookEditor(subject PlaybookSubject, env BaseEnv, prof *profile.Profile) string {
	var b strings.Builder
	b.WriteString(prof.Prompts["editor.md"])
	b.WriteString("\n\n## Environment\n")
	b.WriteString("- Playbook id: ")
	b.WriteString(subject.ID)
	b.WriteString("\n- Active version in editor: ")
	b.WriteString(subject.Version)
	if subject.Type != "" {
		b.WriteString("\n- Type: ")
		b.WriteString(subject.Type)
	}
	b.WriteString("\n- Authoring tools: mcp__triagent-strategies__*   (")
	b.WriteString("playbook_schema, list_playbooks, get_playbook_raw, validate_playbook, playbook_proposal_draft are the ones you'll actually use)")
	for _, m := range prof.ExtraMCPs {
		b.WriteString("\n- mcp__")
		b.WriteString(m.Alias)
		b.WriteString("__*: ")
		b.WriteString(strings.TrimSpace(m.Description))
	}
	writeSourcesSection(&b, env.Sources, env.SlackMCPAvailable, env.IncidentioMCPAvailable)
	writeLinkedReposSection(&b, env.LinkedRepos)
	writeToolCatalogSection(&b, env.ToolCatalog)
	b.WriteString("\n## Current playbook\n\n```yaml\n")
	b.WriteString(strings.TrimRight(subject.YAML, "\n"))
	b.WriteString("\n```\n\nWait for the operator's first request. Do not propose anything proactively.")
	return b.String()
}

func buildWikiEditor(subject WikiSubject, env BaseEnv, prof *profile.Profile) string {
	var b strings.Builder
	b.WriteString(prof.Prompts["wiki_editor.md"])
	b.WriteString("\n\n## Environment\n")
	b.WriteString("- Wiki entry kind: ")
	b.WriteString(subject.Kind)
	b.WriteString("\n- Wiki entry id: ")
	b.WriteString(subject.ID)
	b.WriteString("\n- Authoring tools: mcp__triagent-wiki__*   (wiki_search, wiki_get, wiki_list_entities, wiki_correlate, validate_wiki, propose_wiki_draft)")
	b.WriteString("\n- Investigation playbooks: mcp__triagent-strategies__*   (list_playbooks, walk_playbook, get_state, step_complete — call list_playbooks first to see what's available)")
	for _, m := range prof.ExtraMCPs {
		b.WriteString("\n- mcp__")
		b.WriteString(m.Alias)
		b.WriteString("__*: ")
		b.WriteString(strings.TrimSpace(m.Description))
	}
	writeSourcesSection(&b, env.Sources, env.SlackMCPAvailable, env.IncidentioMCPAvailable)
	writeLinkedReposSection(&b, env.LinkedRepos)
	writeToolCatalogSection(&b, env.ToolCatalog)
	if strings.TrimSpace(subject.ExistingMarkdown) != "" {
		b.WriteString("\n## Existing wiki entry\n\n```markdown\n")
		b.WriteString(strings.TrimRight(subject.ExistingMarkdown, "\n"))
		b.WriteString("\n```\n\nThe operator wants to revise this. Read it first; preserve structure and prior wording where the new sources don't change it. Wait for their first request before modifying anything.")
	} else if env.Sources.HasSlack() || env.Sources.HasIncidentio() || env.Sources.HasInvestigation() {
		// Backfill mode: operator attached at least one specific source.
		// Walk the meta-playbook end-to-end without confirmation.
		b.WriteString("\n## Backfill resolved incident\n\nThis session was created from the homepage's *New wiki entry* modal, with sources attached. Walk the `wiki_backfill_ingestion` meta-playbook end-to-end via `mcp__triagent-strategies__walk_playbook` — ingest the sources, draft, validate, and propose. Don't ask the operator to confirm; the modal already did. Begin now by calling `mcp__triagent-strategies__list_playbooks` to confirm the playbook is loaded, then `mcp__triagent-strategies__walk_playbook` with id `wiki_backfill_ingestion`.")
	} else {
		// No specific scope attached. Even when slack/incidentio MCPs
		// are wired (token linked), nothing is pre-pinned, so don't
		// auto-walk the backfill playbook — wait for the operator.
		b.WriteString("\n## New wiki entry\n\nNo existing entry yet — this session drafts one from whatever the operator provides. Wait for their first request before producing a draft. If they want a backfill, ask them for an incident.io URL or a Slack channel first, or use `slack_get_channel_id` to resolve a name they mention.")
	}
	return b.String()
}

func writeSourcesSection(b *strings.Builder, src Sources, slackAvail, ioAvail bool) {
	if !slackAvail && !ioAvail && !src.HasInvestigation() {
		return
	}
	hasScope := src.HasSlack() || src.HasIncidentio() || src.HasInvestigation()
	b.WriteString("\n\n## Sources\n")
	if hasScope {
		b.WriteString("The operator linked these as primary evidence for this session — start here, but you can investigate any channel/incident the operator's tokens grant access to.\n")
	} else {
		b.WriteString("Slack and incident.io tools are wired (the operator linked their tokens) but the session is not pinned to a specific channel or incident. Ask the operator which one to look at, or use `slack_get_channel_id` to resolve a channel by name.\n")
	}
	if src.HasInvestigation() {
		b.WriteString("- Investigation transcript: the session that produced this entry is at `")
		b.WriteString(src.InvestigationDir)
		b.WriteString("/events.jsonl`. Read it with the Read tool when drafting — it's a JSON-lines transcript (one event per line: assistant turns, tool calls, results). For long files, use offset+limit.\n")
	}
	if ioAvail {
		b.WriteString("- incident.io tools: `mcp__triagent-incidentio__*` (incidentio_get_incident, incidentio_get_timeline, incidentio_get_postmortem, incidentio_search_related). Pass `incident_id` on every call.")
		if src.HasIncidentio() {
			b.WriteString(" Session default `incident_id`: `")
			b.WriteString(src.IncidentioRef)
			b.WriteString("`")
			if src.IncidentioURL != "" {
				b.WriteString(" (URL: ")
				b.WriteString(src.IncidentioURL)
				b.WriteString(")")
			}
			b.WriteString(".")
		}
		b.WriteString("\n")
	}
	if slackAvail {
		b.WriteString("- Slack tools: `mcp__triagent-slack__*` (slack_get_channel_id, slack_channel_overview, slack_search_messages, summarize_thread, analyze_channel). Pass `channel_id` on every channel-aware call. If you only have a channel name, call `slack_get_channel_id` first.")
		if src.HasSlack() {
			b.WriteString(" Session default `channel_id`: `")
			b.WriteString(src.SlackChannelID)
			b.WriteString("`")
			if src.SlackChannelName != "" {
				b.WriteString(" (#")
				b.WriteString(src.SlackChannelName)
				b.WriteString(")")
			}
			if src.SlackSinceUnix > 0 {
				b.WriteString(". Suggested `since_unix`: ")
				b.WriteString(strconv.FormatInt(src.SlackSinceUnix, 10))
			}
			b.WriteString(".")
		}
		b.WriteString("\n")
	}
}

func writeLinkedReposSection(b *strings.Builder, linked []repos.LinkedRepo) {
	if len(linked) == 0 {
		return
	}
	b.WriteString("\n\n## Linked repositories\n")
	b.WriteString("Each linked GitHub repo is exposed via its own MCP server. **Always call `mcp__triagent-git-<alias>__get_repo_architecture_summary` first** when investigating a question that touches the repo — it's a cached digest the launcher generated upfront, free at retrieval, and falls through to the description below when no summary is yet cached. Use them to read controller/SDK code when you need code-level evidence. Discovery tools (`latest_tags`, `commit_summary`, `diff_summary`, `search_log`) are cheap deterministic git plumbing; sub-agent tools spawn a focused sub-Claude in the cloned repo and return a summary so this session's context stays clean: `research_codebase` answers questions about the code as a whole at a ref (exact metric names, CRD conditions, flags, alert rules), `analyze_change` explains one specific commit, `correlate_with_findings` ranks recent commits against symptoms. Don't use `analyze_change` with `ref=HEAD` to ask whole-repo questions — that's `research_codebase`. Tools are addressable as `mcp__triagent-git-<alias>__<tool>`.\n")
	for _, r := range linked {
		b.WriteString("- `")
		b.WriteString(r.EffectiveAlias())
		b.WriteString("` — ")
		b.WriteString(r.Owner)
		b.WriteString("/")
		b.WriteString(r.Name)
		if r.Description != "" {
			b.WriteString(" — ")
			b.WriteString(r.Description)
		}
		b.WriteString("\n")
	}
}

func writeToolCatalogSection(b *strings.Builder, catalog []ToolCatalogEntry) {
	if len(catalog) == 0 {
		return
	}
	b.WriteString("\n\n## Tool catalog (referenceable in `suggested_calls`)\n")
	b.WriteString("Every tool below is callable from a real investigation session. The editor session may not have all of these MCP servers registered (k8s/prom need cluster context the editor lacks), but you can — and should — reference them by `<server>/<tool>` in any playbook node's `suggested_calls`. Required arg names are marked with `*`; everything else is optional.\n")
	var lastServer string
	for _, t := range catalog {
		if t.Server != lastServer {
			b.WriteString("\n### ")
			b.WriteString(t.Server)
			b.WriteString("\n")
			lastServer = t.Server
		}
		b.WriteString("- `")
		b.WriteString(t.Name)
		b.WriteString("(")
		for i, in := range t.Inputs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(in.Name)
			if in.Required {
				b.WriteString("*")
			}
		}
		b.WriteString(")` — ")
		b.WriteString(t.Description)
		b.WriteString("\n")
	}
}
