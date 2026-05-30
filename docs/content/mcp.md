# MCP servers

The agent doesn't talk to Kubernetes, Prometheus, GitHub, or the playbook walker directly. Every interaction goes
through an MCP (Model Context Protocol) server, a small subprocess that exposes a typed tool surface. `triagent-mcp`
is the binary that ships those servers, and the launcher spawns one of each kind per investigation session.

## The catalog is a map

The tool catalog is the map the agent navigates, and it's the same map an operator reads when authoring a playbook.
A tool name like `gateway_error_rate` is a tool *and* a piece of documentation: its existence tells the agent "there's
a sanctioned way to ask this question", and tells the operator "this question has a tested answer". The reflected
JSON-schema is the docs; there's no second artifact to drift.

This makes **tool design agent design**. Shipping a new MCP tool changes what the agent reaches for without touching
the agent, the playbooks, or the prompt. Removing one closes off a question shape; renaming one rephrases the agent's
vocabulary. The catalog isn't plumbing; it's the steerable part of the system.

## What MCP is, briefly

MCP is a protocol for giving an LLM agent a stable, typed tool catalog over stdio. The agent emits a `tool_use` block;
the server runs the tool; the result comes back as `tool_result`. Tools have JSON-schema'd inputs, named outputs, and
short descriptions the agent reads before deciding to call.

The launcher writes a per-session `mcp.json` enumerating which servers to spawn for _this_ investigation. Each server
gets its own env-var scope (cluster, namespace, prom URL, repo path, …). Claude inherits the config from the CLI args.

## The server kinds

`triagent-mcp` ships several server kinds. Each one is invoked as `triagent-mcp serve --kind=<kind>` with
kind-specific env vars. The list below is a tour with rationale; the live tool inventory is in the launcher's
[**MCP catalog**](/mcp), the source of truth for which tools are reflected from the build you're running.

### triagent-k8s

Read-only Kubernetes access. Tools that read namespaced resources take a per-call `namespace` argument; the server
itself is not bound to one. The session's parameter block surfaces `cluster-resource-namespace` as the default to pass.

- `list_resource_kinds`: what kinds (CRDs included) are visible?
- `list_resources`: list resources of a kind, optionally label-filtered.
- `get_resource`: fetch a single resource as YAML or describe-style.
- `get_logs`: pod logs with grep / tail / since filters.
- `list_events`: events filtered by involved object kind / name / reason.
- `list_namespaces`: substring filter over namespace names; use to find the right scope when the cluster-id is unknown.
- `trace_crossplane`: walk a Crossplane composite (XR) and report each child's Ready/Synced state to find which
  managed resource is stuck.

The server speaks against the operator's kubeconfig context. Each tool call carries its own namespace argument so a
single session can investigate cluster-wide. There's no write surface; by construction the agent can't `apply`,
`delete`, `exec`, or `port-forward`.

### triagent-strategies

The investigation walker. Loads playbook YAMLs at startup, opens sessions, tracks findings, suggests next steps.

- `list_playbooks`: discoverable catalog, filterable by type.
- `playbook_types`: canonical type list (`investigation` for symptom-triage decision trees, `general` for meta/workflow
  playbooks).
- `walk_playbook`: open a session for an id; returns the entrypoint step.
- `get_state`: read the walker's current step, findings, and next-step options without advancing.
- `step_complete`: record findings and advance to the next node in one atomic call.
- `summarize`: produce the formal markdown conclusion.
- `get_playbook_raw`, `validate_playbook`, `playbook_schema`: authoring surface used by the AI proposal flow.
- `playbook_proposal_draft`: submit a draft for in-chat operator review.

Plays the role of _contract enforcer_: every investigation has a recorded chain of decisions, every conclusion is a
structured artifact, and the audit trail is the activity panel.

### triagent-git (per linked repo)

One server per linked GitHub repo. Operators add repos from the **linked github projects → manage** panel in the
sidebar. Each repo gets its own `triagent-git-<alias>` MCP process.

**Discovery (cheap, deterministic):**

- `latest_tags`: recent tags, with -SNAPSHOT / prerelease filtering.
- `commit_summary`, `diff_summary`: point lookups.
- `search_log`: `git log --grep` style search.
- `search_issues`: `gh search issues` for duplicate detection before drafting a new issue.

**Sub-agent (focused sub-claude in the clone):**

- `analyze_change`: broad question over a single change; returns a short summary so the parent session's context stays clean.
- `correlate_with_findings`: given the running session's findings, ranks recent changes by likelihood of correlation.
- `draft_pr`: opens a draft PR for a linked GH issue. Runs in a fresh `git worktree` with TDD + verification skills;
  the host owns push and `gh pr create` (denied to the sub-agent). Long-running (up to 30 min). See the [Codefix section](/docs/repos#codefix) for the operator flow.

**Deterministic (no sub-agent):**

- `create_github_issue`: files an issue on this repo from the agent-authored title + body markdown. Injects the
  `triagent-proposal` label so the launcher's PR-state poller can identify investigation-driven artefacts.

Repos are cloned once (lazily) into a cache dir; subsequent calls reuse the clone. Sub-agent tools spawn an isolated
claude inside the clone; they're heavyweight and the parent agent should reach for the discovery tools first.

See [/docs/repos](/docs/repos/) for how the launcher caches per-repo architecture summaries and the lifecycle around
refreshing them.

### triagent-wiki

The persistent knowledge surface (see the **Wiki** section). Loads incident files + entity files from the launcher's
wiki clone, exposes search/list/get tools, and provides a sub-agent that drafts new wiki entries from a finished
investigation.

### triagent-slack (optional)

Slack reader. The launcher spawns one instance per session whenever a Slack token is linked, with no channel scoping at
boot. The agent passes a `channel_id` argument on every channel-aware call (`slack_channel_overview`,
`slack_search_messages`, `summarize_thread`, `analyze_channel`); when it only has a channel name it calls
`slack_get_channel_id` first to resolve to a `C…` id. The session's pinned channel (when the operator picked one in the
modal) is surfaced in the system prompt as the suggested default, not as an MCP boot arg, so the agent can also
investigate other channels the token can access.

Disabled when no Slack token is on file. Connect under the **connections** strip in the sidebar.

### triagent-incidentio (optional)

incident.io reader. One instance per session whenever an incident.io API key is linked, with no incident scoping at boot.
The agent passes `incident_id` (numeric reference like `5466` or UUID) on every call. The session's pinned incident
(when the operator pasted a URL or the wiki id encoded one) flows through the system prompt as the default; the agent
can call other incidents the key can access by passing a different `incident_id`.

Disabled when no incident.io API key is on file. Connect under the **connections** strip in the sidebar.

## Why MCP, not a generic kube/prom client

Three reasons:

1. **Typed tool surface beats a free-form shell.** A bash-able `exec` would let the agent do anything, including delete
   resources, exfiltrate secrets, or run arbitrary code. Every tool on triagent-k8s is read-only by construction.
2. **Curation forces good questions.** Named tools call tested queries; a generic `prometheus_query` would let the agent
   invent one. The bounded surface is also the documentation: the tool list tells the agent (and the operator) exactly
   what the system can answer.
3. **Replayability.** Every tool call is logged with its input and result. An investigation transcript is reproducible:
   re-running the same calls in the same order yields the same evidence (modulo cluster state drift).

## Catalog and architecture

The launcher's **MCP** view (top nav) is a live tool catalog: every tool from every server, with its inputs and a short
description. Use it as a reference when authoring playbooks ("what arg does `get_resource` take?") or when debugging a
failed call.

Behind the scenes:

- Tool inputs are reflected from the Go input struct via `jsonschema:` tags. Editing a tool's struct in triagent-mcp
  instantly updates the catalog the launcher renders, with no hand-curated docs to drift.
- The catalog is built once at launcher startup by aggregating each MCP's `ToolSpecs()` in-process via
  `internal/server/meta.go` (`loadMeta` / `toolCatalog`) and cached. Re-launch to pick up new tools after a
  triagent-mcp upgrade.

## Using MCP tools as an operator

Operators don't call MCP tools directly; the agent does. But two places put the catalog in your hands:

1. **The MCP view.** Browse what's available, expand a tool to see its inputs.
2. **The playbook editor's "suggested calls" picker.** When authoring a playbook node, picking a tool from the dropdown
   autocompletes the args based on the reflected schema.

If you find yourself wanting to invoke a tool by hand, you're probably authoring a playbook; encode the call there so
the next operator gets the help too.

## Adding a new MCP server

Out of scope for this doc, but the shape: implement the new kind in `pkg/mcp/<kind>/`, register it in
`cmd/triagent-mcp/serve.go`, add the kind's `ToolSpecs()` to the in-process aggregator in
`internal/server/meta.go::toolCatalog()`, and extend the launcher's preflight to spawn it. The catalog view picks it up
automatically once the aggregated catalog reflects the new tools.
