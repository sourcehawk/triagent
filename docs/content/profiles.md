# Profiles

A **profile** is the deployment-specific config bundle that tells the launcher what your platform looks like: how
to reach a cluster, which upstream repos back playbooks / wiki / sessions, what the agent should know about your
stack before triaging, which extra MCPs to attach, what the preflight form asks for, and where launcher state lives
on disk. The embedded `default` profile is runnable as-is on any Kubernetes cluster reachable through your
kubeconfig. Forking it is how you teach Triagent about *your* platform.

## Anatomy of a profile

A profile is a directory with a `profile.yaml` at the root, plus optional sibling assets (`prompts/*.md`,
`k8s/kinds.json`) so prompts and the kinds list stay readable instead of getting jammed inline as heredoc strings.
The yaml below is a realistic overlay against the embedded `default` — read it top to bottom; every block has a
job, and the comments explain when you'd touch it.

```yaml
# my-team/profile.yaml — an overlay that fits Triagent to your platform.
# Inherits everything from `default`, then overrides only the fields below.

name: my-team
base: default

# Upstream git repos that back the playbooks / wiki / sessions surfaces.
# Empty (or unset) = local-only mode — everything still works, but
# sync-from-upstream and push-as-PR stay disabled. owner/name on GitHub.
defaults:
  playbooks_repo: my-org/triagent-playbooks
  wiki_repo:      my-org/triagent-wiki
  sessions_repo:  my-org/triagent-sessions

# The single highest-leverage override. Site knowledge the agent reads
# before every triage: what your platform does, how its components compose,
# naming conventions, recurring failure modes. Path is relative to this
# profile.yaml. See "Writing a good architecture.md" below.
prompt_files:
  architecture.md: architecture.md

# Kubernetes resource kinds the triagent-k8s MCP exposes to the agent.
# Fork pkg/mcp/k8s/default_kinds.json from the triagent repo as a starting
# point, add your CRDs, and point at it here. Missing-file is a hard error
# so typos surface at load time instead of silently falling back.
kinds_file: kinds.json

# Repos to spin up per-repo triagent-git MCPs for. Each entry becomes a
# search_issues / draft_pr / commit_summary tool surface the agent can
# consult during triage. Operators add personal repos on top from the
# sidebar; profile-supplied entries appear locked (🔒) in the UI.
linked_repos:
  - owner: my-org
    name: widget-broker
    alias: widget-broker
    description: >
      Customer-facing widget API. Reach for it when an investigation
      mentions widgets, request rate, or HTTP 5xx on the gateway.

# Additional MCPs to attach to every investigation. Two modes:
#   reference — MCP runs externally (Claude Code plugin, hosted tooling);
#               the agent learns about it from the description only.
#   spawn     — launcher spawns the process. Add command/args/env.
extra_mcps:
  - alias: org-docs
    description: Org-internal docs MCP, hosted via Claude Code.

# Authentication for cluster access. Two kinds:
#   kubeconfig — reads $KUBECONFIG / ~/.kube/config. Zero setup.
#   teleport   — SSO via `tsh login`. Requires the teleport block below.
auth:
  kind: kubeconfig
  # teleport:
  #   proxy: example.teleport.sh
  #   auth_connector: okta

# Everything not declared here falls back to the embedded `default`:
# the playbooks entrypoint, the paths layout, the slack channel prefix,
# the preflight form fields, Prometheus coordinates, the `offline`
# air-gap switch. See the full inline-commented schema:
#   https://github.com/sourcehawk/triagent/blob/main/internal/profile/profiles/default/profile.yaml
```

The blocks above cover the 90% case. A few more knobs the default already sets sensibly but you can override:
**`paths`** (per-machine filesystem layout, templated with `${XDG_CONFIG_HOME}` / `${PROFILE_NAME}` so switching
profiles never mixes state), **`slack.channel_prefix`** (filter applied to the Slack channel picker — e.g. `inc-`
to hide non-incident channels), **`defaults.prometheus`** (in-cluster Prometheus coordinates; an empty `service`
keeps the prom MCP detached), and **`defaults.auto`** (default new investigations to operator-agent mode).
[Investigation inputs](#investigation-inputs), [namespace derivation](#namespace-derivation), and
[model selection](#model-selection) get their own sections below. The canonical reference for every field is the
default profile's
[`profile.yaml`](https://github.com/sourcehawk/triagent/blob/main/internal/profile/profiles/default/profile.yaml)
with a comment on every one.

## What a fork is for

The default profile gives you a working agent, but a generic one. It walks the same playbooks and reads the
cluster through the same MCPs every operator does, with platform-neutral prompts that don't know what your team
actually builds, ships, or operates. The fork's job is to teach it that *site knowledge* once, so every future
investigation starts informed instead of rediscovering your stack from scratch.

In practice that's principally [`architecture.md`](#writing-a-good-architecturemd), the prompt the agent reads
before every triage, with [`kinds.json`](#kindsjson-override) close behind for stacks with custom Kubernetes
resources. Everything else (extra MCPs, linked repos, prompt-tone overrides) is incremental.

## Loading a profile

The launcher takes a path to a profile directory (or directly to a `profile.yaml`):

```sh
triagent start                                    # embedded default
triagent start --profile ./my-team                # profile directory
triagent start --profile ./my-team/profile.yaml   # yaml file directly
```

`TRIAGENT_PROFILE` is the env-var equivalent of `--profile`. With no argument the launcher runs the embedded
`default` profile in local-only mode: everything works, but sync-from-upstream and push-as-PR stay disabled until a
fork wires the three upstream repos.

There are two ways to ship a fork. Pick by how much of the default you want to own.

### Write an overlay

Recommended starting point for most teams. Create a directory with a `profile.yaml` that inherits from `default`
and overrides only the fields you care about, plus any sibling assets (`architecture.md`, `kinds.json`) those
overrides point at:

```sh
mkdir -p ~/.config/triagent/profile
$EDITOR ~/.config/triagent/profile/profile.yaml      # paste the Anatomy yaml, trim to what you override
$EDITOR ~/.config/triagent/profile/architecture.md   # your platform's site knowledge
triagent start --profile ~/.config/triagent/profile
```

Everything you leave out of the yaml is inherited from `default`. The overlay is easy to keep in sync with upstream
Triagent because you only own the diff.

### Fork the default

For heavier customisation (multiple prompt overrides, a `kinds.json` you'll iterate on, non-trivial
`investigation_inputs` changes), materialise a byte-for-byte copy of the default you can edit in place:

```sh
triagent create-profile my-team
# → creates ./my-team/profile.yaml + ./my-team/prompts/*.md
# edit ./my-team/profile.yaml: set defaults.*_repo, customise prompts, etc.
triagent start --profile ./my-team
```

`create-profile` refuses to clobber an existing directory and rewrites the top-level `name:` field to match the
argument. Comments and field ordering are preserved on copy, which is the whole point of forking over overlaying:
the inline-commented schema stays at editing distance.

You can always graduate from overlay to fork later by running `create-profile` and merging your overlay fields
into the materialised copy.

## How `base:` merging works

When a profile declares `base: <embedded-name>`, the loader loads the embedded base first, then layers the override
on top. The merge is **replace-on-presence** at the top-level field granularity: if a field is non-zero in the
override, it wins; otherwise the base's value fills in.

A few specifics worth knowing:

- **Strings**: `""` in the override = treated as "field absent", fall back to base. Use the `/` sentinel for
  `playbooks_path` / `wiki_path` / `sessions_path` when you actually want to clear them to repo root; the loader
  normalises `/` → `""` after the merge.
- **Slices** (`linked_repos`, `extra_mcps`, `investigation_inputs`): `nil` = take base; an empty-but-present slice
  (`[]`) counts as a deliberate clear. Slices replace wholesale — you can't merge two repo lists by inheritance.
- **Prompts**: merged **per filename**, not wholesale. Override `architecture.md` and the other four prompts still
  come from the base.
- **`paths` block**: each subfield merges independently, so you can override one dir and inherit the others.

There's no transitive inheritance — `base:` accepts only embedded profile names (`default`, today). A child profile
can't chain off another on-disk profile.

## Switching profiles

Every dir under `paths.*` in the default bakes in `${PROFILE_NAME}`, so switching profiles never mixes state:
upstream clones, session transcripts, user-edited playbooks, repo caches, and the per-machine linked-repos list are
isolated per profile. Switch with `--profile <name>` or `TRIAGENT_PROFILE=<name>`; the only shared state is the
credentials file at `~/.config/triagent/credentials.json`.

Drop `${PROFILE_NAME}` from a `paths.*` value in your fork if you deliberately want shared state across profiles —
but be aware preflight clones don't validate remotes, so two profiles sharing an `upstream_playbooks_dir` will
silently see whichever was cloned first.

## Prompt overrides

Five prompts ship with the default profile. Each has a job; the table below sorts them by how often you'll touch
them.

| File              | What it frames                                                                                | Override frequency |
| ----------------- | --------------------------------------------------------------------------------------------- | ------------------ |
| `architecture.md` | Site knowledge: what your platform does, how it composes, recurring failure modes.            | **Always**         |
| `system.md`       | Top-of-prompt persona for the investigation agent.                                            | Rare               |
| `strategies.md`   | How the agent should use the strategies MCP (the walker, suggested steps, evidence-led flow). | Rare               |
| `editor.md`       | System prompt for the playbook editor assistant.                                              | Rare               |
| `wiki_editor.md`  | System prompt for the wiki authoring assistant.                                               | Rare               |

The originals live at
[`internal/profile/profiles/default/prompts/`](https://github.com/sourcehawk/triagent/tree/main/internal/profile/profiles/default/prompts).
Fork the file you want to override into your profile dir and either drop it under `prompts/<name>.md` (conventional
layout) or point at it from `prompt_files`:

```yaml
prompt_files:
  architecture.md: architecture.md   # sibling file
  system.md:       prompts/system.md # under a prompts/ subdir
```

The `prompts/` directory and `prompt_files` map can coexist; `prompt_files` wins on name collision. Unspecified
prompts fall back to the base profile's content, so an overlay can override one file and leave the rest alone.

### Writing a good `architecture.md`

The default `architecture.md` is intentionally generic. It says things like "read the workload's `status` before
fetching logs" — triage hygiene that holds anywhere. What it can't say is *what your platform actually does*. The
fork's job is to describe your stack from the agent's perspective, in whatever shape it actually takes:

- **What you build, ship, or host.** Two short paragraphs the agent can ground every later observation against.
  "We process invoice OCR — ingest accepts uploads, classification tags them, storage persists them, and a public
  API serves them back." A new operator should be able to read this and orient themselves.
- **How the components compose.** Whether your stack is CRDs reconciling other CRDs, services calling services,
  worker tiers consuming queues, or any mix, name the edges. The agent picks the right thread when one tier
  reports trouble if it knows the topology.
- **Naming conventions.** Where customer data lives vs control-plane, the namespace / cluster / repo conventions
  your team uses, the prefixes that mean something. Lets the agent skip places that can't be the answer.
- **Version-pivot gotchas.** "v1.4 changed the condition string from `Stuck` to `Reconciling`; older wiki entries
  may still mention the old name." The recurring traps you'd brief a new oncall on.
- **Common failure modes worth pre-loading.** Three to five recurring patterns and where they show up. Saves the
  agent from rediscovering them per session.

Aim for a page a new human operator would also benefit from reading. If it works for them, it works for the agent.

## `kinds.json` override

The `triagent-k8s` MCP serves a curated set of Kubernetes resource kinds through `list_resource_kinds` /
`list_resources` / `get_resource`. Each entry is a GVK with a description the agent reads to decide when to fetch
it; some carry a `redact: true` flag for kinds whose values look like credentials.

The default ships
[`pkg/mcp/k8s/default_kinds.json`](https://github.com/sourcehawk/triagent/blob/main/pkg/mcp/k8s/default_kinds.json) —
Pod, Service, Deployment, StatefulSet, Ingress, the common stuff. Your platform's CRDs almost certainly aren't in
there.

Point `kinds_file` at your fork:

```yaml
kinds_file: kinds.json   # path relative to profile.yaml
```

Missing-file is a hard error so typos surface at load time. The conventional `k8s/kinds.json` location works too;
`kinds_file` overrides it when both are present. The file shape:

```json
{
  "kinds": [
    {
      "group": "platform.example.com",
      "version": "v1",
      "kind": "Foo",
      "description": "Top-level customer workload. One per tenant; reconciled by foo-operator.",
      "redact": false
    }
  ]
}
```

Fork the default file as a starting point and add entries for the CRDs the agent should be able to fetch by name.

## Investigation inputs

`investigation_inputs` is the preflight form schema. Each entry renders a UI control on the new-investigation
page and declares how the captured value flows into the prompt parameter block the agent reads at session start.
Order in YAML matches order in the form.

```yaml
investigation_inputs:
  - id: notes                       # stable key; surfaces in error messages and as the values-map key.
    label: Notes                    # form label.
    type: textarea                  # text | url | textarea | cluster_id | slack_channel
    optional: true                  # required fields fail preflight when empty.
    placeholder: "Symptoms, alerts, what you've seen…"
    hint: "Plain text; gets injected verbatim."
    prompt_keys:
      - { key: operator-notes, value: "{{.value}}", if: '{{ne .value ""}}' }
```

### Input types

| Type            | Renders as                                          | Template ctx vars                  |
| --------------- | --------------------------------------------------- | ---------------------------------- |
| `text`          | single-line input                                   | `{{.value}}`                       |
| `url`           | single-line input, light URL validation             | `{{.value}}`                       |
| `textarea`      | multi-line textarea                                 | `{{.value}}`                       |
| `cluster_id`    | cluster picker bound to detected kube contexts      | `{{.value}}`                       |
| `slack_channel` | channel picker (filtered by `slack.channel_prefix`) | `{{.id}}`, `{{.name}}`, `{{.url}}` |

Required (`optional: false`) inputs must be non-empty at preflight or the investigation refuses to start. For
`slack_channel`, "non-empty" means at least one of `id` or `url` is set.

### `prompt_keys`

Each input projects into zero-or-more key/value lines in the agent's prompt parameter block via `prompt_keys`.
The templates are Go `text/template`; the `.` context fields available depend on the input type (see table above).

- `key` — emitted name in the prompt parameter block. Free-form; playbooks read these by name.
- `value` — template producing the emitted value. Plain `{{.value}}` is the common case.
- `if` — optional guard. The line is skipped when the rendered guard is empty or the literal string `false`
  (Go's template renderer for boolean expressions). Use it to drop empty optional inputs from the prompt entirely
  rather than emitting `key: ""`.

The default's `slack_channel` shows the pattern for multi-output inputs:

```yaml
- id: slack_channel
  type: slack_channel
  optional: true
  prompt_keys:
    - { key: slack-channel-id,   value: "{{.id}}",   if: '{{ne .id ""}}' }
    - { key: slack-channel-name, value: "{{.name}}", if: '{{ne .name ""}}' }
    - { key: slack-channel-url,  value: "{{.url}}",  if: '{{and (eq .id "") (ne .url "")}}' }
```

The `id`/`name` pair is emitted whenever the channel resolved through Slack's API; the `url` line is the fallback
for when the operator pasted a URL that didn't resolve to a channel id.

### Overriding the form

`investigation_inputs` merges wholesale: an override either inherits the entire default form (omit the key, or
set it to `nil`) or replaces it (any non-nil list, including the empty `[]` to clear). There is no per-id merge.
To keep the defaults and add a field, copy the default block in full and append your additions.

## Namespace derivation

`namespace_derivation`, when set, lets the launcher pre-compute namespace hint(s) from the alert payload and
inject them into the session's system prompt at preflight, sparing the agent a `list_namespaces` call in the
common case. Two shapes, mutually exclusive — `rules` wins when both are set.

**Template form** — single string with `${field}` placeholders against the alert payload's flat string map:

```yaml
namespace_derivation:
  template: "tenant-${tenant_id}"
```

Every placeholder must resolve to a non-empty value, or the hint is dropped entirely (a half-resolved hint is
worse than no hint).

**Rules form** — top-to-bottom; the first rule whose `when` predicate matches renders its `template`:

```yaml
namespace_derivation:
  rules:
    - when: "${kind} == 'tenant'"
      template: "tenant-${tenant_id}"
    - when: "${cluster_role} != ''"
      template: "control-plane-${cluster_role}"
```

Supported `when` shapes are deliberately small: `${field} == 'literal'` and `${field} != ''`. Anything else
silently doesn't match; compose multiple rules instead of inventing richer logic.

## Model selection

`models` picks the LLM model for the main investigation session and for sub-agent dispatches. Per-call overrides
for specific sub-agent tools (e.g. `draft_pr`'s Sonnet-grade work) remain available; this block sets the defaults.

```yaml
models:
  investigation: claude-sonnet-4-6         # main agent
  subagent:      claude-haiku-4-5-20251001 # sub-agent dispatches
```

Both fields are optional; omitted fields fall back to the baked-in defaults shown above.

## Air-gapped mode

`defaults.offline: true` skips boot-time `git clone` of the upstream repos. The launcher still loads existing
checkouts at the conventional locations under `paths.*` — useful when the team mirrors `triagent-playbooks` /
`triagent-wiki` / `triagent-sessions` into an internal git host and pre-seeds the dirs out-of-band. Missing-or-empty
upstream dirs fail fast with a clear error so the operator can pre-seed them manually rather than the launcher
silently running in local-only mode.

## See also

- [Connections](/docs/connections). Slack and incident.io credential handling. Credentials live outside the profile,
  in `~/.config/triagent/credentials.json`.
- [Repos](/docs/repos). What `linked_repos` enables per repo, including the architecture-summary cache and codefix.
- [MCP](/docs/mcp). The tool catalog `extra_mcps` extends.
- [`profile.yaml`](https://github.com/sourcehawk/triagent/blob/main/internal/profile/profiles/default/profile.yaml).
  The canonical, inline-commented schema reference.
