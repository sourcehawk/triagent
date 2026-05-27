# Triagent

Agentic Incident Investigation, driven from your browser.

Triagent is a localhost web app that pairs the Claude reasoning agent with read-only Kubernetes access, an extensible
MCP catalog (Prometheus, Slack, GitHub, incident.io, your own), a guided playbook walker, and a persistent wiki, all
bound to a single cluster's namespace per session. You run `triagent start`, it opens a browser, you hand it the
symptom, and it drives a focused diagnosis you can paste into a ticket when it's done.

## The problem it solves

Kubernetes workloads fail in shapes you've seen before, but the triage isn't a `kubectl` command, it's a multi-tool
scramble across half a dozen surfaces. A typical incident looks like this:

1. **Alert lands.** Pager, Slack `@`-mention, customer ticket. You were probably already on something else.
2. **Catch up on the channel.** What has the customer / oncall / support already said? What's been ruled out? Who
   else is looking?
3. **Read the cluster state.** Pods, events, logs, the failing workload's owner CR, the storage backing it, the
   gateway service.
4. **Check what changed.** Recent deploys, controller version skews, last week's incident write-up that mentioned
   the same component.
5. **Pull metrics.** Prometheus for saturation, app-specific dashboards for queue depth or stuck work, incident.io
   for the ongoing-incident timeline.
6. **Recall prior art.** Have we seen this exact pattern before, and what fixed it?
7. **Synthesise.** Hold the cross-references in your head, decide which thread to pull next, write up a conclusion
   someone else can act on.

Each step is mechanical for an experienced operator, but the tabs multiply and the synthesis is slow. Worse, the
patterns drift as new operators rotate in, and the artefact at the end is a Slack message that decays the moment the
channel scrolls.

This tool collapses steps 2–6 into one conversation against one audit trail. The walker knows which sources to consult
for which failure shapes; the MCP catalog turns each query into a single typed tool call; the summary in step 7 is a
markdown block with a copy button and a *push to upstream* button. Every tool call stays visible, so you can audit the
chain or interrupt at any point, and the finished session can be shared so the next operator starts from where you
ended, not from the alert.

With watches on the source, step 1 collapses too. The launcher polls the channel or the issue query itself,
pre-classifies new items against recent signals and the wiki, and, when the bar is met, hands you a session that's
already past the catch-up phase. With auto-start on and the operator agent in auto mode, routine ones run end-to-end
before you've read the page. And because every finished investigation can deposit a wiki entry, a playbook, or a codefix
proposal, step 6 (*recall prior art*) gets cheaper for every subsequent operator: today's diagnosis is tomorrow's
single-call wiki recall, or the `dismissed` signal you never had to look at.

## Why this works

Three properties make the system unusually leveraged:

- **The agent reads the procedure, doesn't memorise it.** Domain-specific knowledge lives in playbooks the agent loads
  at runtime, not in a system prompt or a fine-tune. Updating what the system can diagnose is a YAML edit, not a model
  rebuild.
- **The tools are a typed catalog, not a shell.** Every action the agent can take is a curated MCP tool with a
  schema'd input. The agent can't go off-piste, and the catalog is also the documentation: same surface for the agent
  and the operator authoring playbooks.
- **Knowledge accumulates as data.** Every investigation can deposit a playbook (procedural) and a wiki entry
  (factual). The system gets sharper with use: new failure shapes don't require code changes, they require a YAML
  file.

New failure shape on Tuesday → playbook PR on Wednesday → every operator has it on Thursday. No release cycle.

## What's in the box

Four surfaces, each with a dedicated section in these docs.

### [Investigations](/docs/investigations)

The operator-facing surface: what you see when you launch `triagent start`. The home view browses sessions the
team has pushed to a shared sessions repo; **+ new investigation** starts a fresh one. From there: hand the agent the
symptom and whatever context you have (a cluster, a Slack thread, an incident.io link, free-form notes; at least one
is required), watch a guided walker drive a diagnosis, and ship the markdown summary either by paste or by pushing the
session upstream as a PR. The activity panel keeps every tool call visible so you can audit the chain or interrupt at
any point.

#### Auto mode

A flavour of the investigation surface where a second agent, the *operator
agent*, plays the human operator role: answering prompts, choosing
capture paths, signing off. Routine investigations now run end-to-end
without a human in the chat. You can take over at any moment for the
high-stakes ones. See [Investigations → Auto mode](/docs/investigations#auto-mode).

### [Watches](/docs/watches)

Persistent eyes-on a source (a GitHub issues query, a Slack channel) that
poll on a schedule and surface anything new as a *signal*. With auto-start
on, an ingestion agent classifies each batch and (when warranted) spawns a
full investigation, so the launcher reaches you before the pager does. Each
signal carries a back-reference to the watch and items that produced it;
manual start is a click for the ones the agent flagged as `unclear`.

### [MCP servers](/docs/mcp)

A tool catalog the agent reads like a map, and the same map an operator reads when authoring a playbook. Exposed as
curated tools rather than a raw shell, so the agent never gets to run arbitrary commands. The catalog grows as we wire
in new sources (Kubernetes, Prometheus, the playbook walker, linked git repos, the wiki, Slack, incident.io, …); rather
than enumerate it here, browse the live list at [**/mcp**](/mcp). The catalog reflects exactly what the launcher
loaded for this build.

### [Playbooks](/docs/playbooks)

Procedural knowledge as data. Each playbook is a YAML graph that encodes one failure shape's triage path: read step
description, make suggested calls, call `step_complete` with findings and the matching goto. New playbooks compound: adding
one expands what the system can diagnose with **zero impact on context size**, because the agent only loads a playbook
when an investigation enters it.

### [Wiki](/docs/wiki)

Citation infrastructure for facts that survive across sessions. Incident write-ups (one per past investigation worth
remembering) and entity profiles (one per long-lived component, such as a CRD, a controller, a workload, a tenant) live in a
real git repo, indexed for the agent to consult during triage. Link density compounds: the more entries cross-reference
canonical entity names, the better the agent's "have we seen this before?" recall gets. Procedure belongs in playbooks;
facts belong in the wiki.

## Alpha Release

This is alpha. Expect rough edges, breaking config changes between versions, and the occasional walker dead-end. Some
things are stable enough to plan around:

- **Sessions persist locally.** Every investigation lives under `~/.config/triagent/sessions/`:
  transcript, MCP config, metadata. Closing the tab and reopening the URL replays it.
- **Playbooks, wiki entries, and shared sessions are GitOps-managed.** Approved drafts land as PRs against their
  respective repos (`sourcehawk/triagent-playbooks`, `sourcehawk/triagent-wiki`,
  `sourcehawk/triagent-sessions`), with no implicit cloud writes and no shared mutable state.
- **Kubernetes access is read-only.** The k8s MCP exposes a curated list of resources with Secrets blocked and ConfigMap
  values redacted. The tool cannot mutate your clusters.

What's still moving: tool surfaces (we add MCP servers as new patterns surface), playbook content (community-driven,
edited in-app), and the wiki schema (entity types are user-extensible).

## Connecting your sources

A handful of optional integrations broaden the agent's reach beyond Kubernetes and Prometheus. None are required to run
an investigation, but each one unlocks tools you'll see appear in the [**/mcp**](/mcp) catalog once the credential is
on file. Each integration has its own page:

- **[Slack and incident.io](/docs/connections)** — credentials stored in `~/.config/triagent/credentials.json` (mode
  0600), validated against the upstream before saving.
- **[GitHub repositories](/docs/repos)** — linked over SSH for clone, `gh` CLI for the *Push as PR* flows. Defaults
  ship via the profile's `linked_repos`; personal repos persist per-machine.
- **[Profiles](/docs/profiles)** — the deployment-specific config bundle that wires upstream repos for playbooks /
  wiki / sessions, the agent's `architecture.md`, custom k8s `kinds.json`, and extra MCPs. The single
  highest-leverage knob for fitting Triagent to your platform.

## Get started

Run `triagent start` in a terminal. The launcher prints a local URL with a per-launch token, opens it in your
browser, and blocks until you press `Ctrl-C`. The home view lands on the shared sessions browser; click **+ new
investigation** to start your own.
