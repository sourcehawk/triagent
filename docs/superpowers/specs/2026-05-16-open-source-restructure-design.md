# Open-source restructure — design

**Date:** 2026-05-16
**Status:** Draft

## 1. Context

The `triagent` repo is a fresh `git init` containing a verbatim port of the
Camunda-internal `c1-plugins` monorepo. The goal is to land a single initial
commit on the public remote that is a fully de-Camunda'd, self-contained,
single-module Go project — the open-source investigation agent.

Three things are happening at once:

1. **Repo flatten.** `c1-plugins` was a multi-plugin host repo (one binary
   per plugin, each with its own `plugin.json`, `go.mod`, version). triagent
   is one project: one Go module, two binaries, one version.
2. **De-Camunda.** Remove every Camunda-specific string, default, repo
   reference, prompt, and profile from the code paths that will ship.
3. **Plug-in surface.** The Prometheus MCP today carries Camunda-coded
   PromQL. Removing it forces a small extension to the existing
   `extra_mcps` profile field so external MCPs can be wired in by an
   operator's profile.

Pluggable k8s auth providers and dynamic switch-context for external MCPs
are explicitly out of scope; they get their own specs after this lands.

## 2. Goals and non-goals

**Goals.**

- Single Go module at the repo root: `github.com/sourcehawk/triagent`. One
  `go.mod`, one CI, one version.
- Two binaries: `triagent` (the launcher) and `triagent-mcp` (the MCP
  multiplexer). Each is a plain Cobra program.
- Zero `github.com/camunda/*` dependencies in `go.mod`. No `c1-sdk` host
  model.
- A runnable default profile that boots without configuration on any
  machine with a working `kubectl`.
- An `extra_mcps[]` profile schema that supports both:
  - *Reference mode* — list a Claude-configured MCP as allowed (the
    existing behavior, unchanged in semantics).
  - *Spawn mode* — let the launcher spawn an arbitrary stdio MCP via
    `command`/`args`/`env` and write it into the per-session `.mcp.json`.
- A published initial commit on `main` that contains no Camunda-internal
  history.

**Non-goals (deferred to follow-on specs).**

- Pluggable k8s auth providers beyond `teleport` and a minimal
  `kubeconfig`. (Future: `gcloud`, `aws-iam`, etc.)
- Dynamic `switch_context` for external MCPs (live URL injection or
  launcher control plane). External MCPs are pinned at boot in v1.
- Re-homing the Camunda profile + Prometheus MCP into `c1-plugins`. That
  is a separate change set in a separate repo.

## 3. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| MCP split | Single module, packages under `pkg/mcp/<name>/`. | Lowest maintenance for a one-binary OSS launcher. Module-per-MCP buys nothing without external vendoring needs. |
| `c1-sdk` dependency | Drop entirely. Copy-vendor the slice of `c1-sdk/teleport/{auth,k8s}` we use into `pkg/auth/teleport/`. | The OSS project must be standalone. Second-consumer-pays-extraction rule (CLAUDE.md): no published helper repo until a second project asks. |
| Plugin host model | Replace `c1-sdk/plugin.Run` with plain Cobra `Execute`. Drop every `plugin.json`. | No host CLI exists in the OSS world. |
| Version | `const` in `cmd/triagent/main.go` and `cmd/triagent-mcp/main.go`. | Simpler than `-ldflags -X`. Switch later if needed. |
| Multiplexer subcommand style | Keep `--kind=<name>` flag (vs. promoting to cobra subcommands like `triagent-mcp k8s`). | Less churn in `internal/preflight/mcpconfig.go` and existing `.mcp.json` templates. |
| Default auth provider | `kubeconfig` (reads `$KUBECONFIG`, lists contexts, no SSO). | Works for any operator with a working `kubectl`. Zero config. |
| Camunda profile fate | Deleted from triagent. Lives only in `c1-plugins`. | Stated goal: no Camunda specifics in the OSS repo. |
| Prometheus MCP | Removed from triagent. Launcher prom port-forward + control endpoint also removed. | ~654 lines of Camunda PromQL; not worth a partial extraction. The c1-plugins repo can keep its build and consume it as a spawn-mode external MCP. |
| External MCP plug-in surface | `profile.extra_mcps[]` supports both reference mode (unchanged) and a new spawn mode (`command`/`args`/`env`/`allowed_tools`). | Spawn mode is the minimum needed to wire prom back in from c1-plugins. Reference mode preserves today's behavior for Claude-configured servers like docs MCPs. |
| Env interpolation in extra_mcps | `${env:NAME}` only; resolved from `os.LookupEnv` at config-write time. | Cheapest plug-in mechanism. No callback, no control plane. v2 can add launcher signals. |
| Switch-context for external MCPs | Unsupported in v1. External MCPs are pinned to their boot config. | Avoids designing the launcher control plane on a single-consumer use case. |
| Commit hygiene | 8 staged commits locally for bisect / review; squashed to a single initial commit before pushing to public remote. | Required by user: no Camunda-internal history on the public repo. |

## 4. Target repo layout

```
triagent/
  go.mod                                  # github.com/sourcehawk/triagent
  go.sum
  Makefile                                # build, lint, test, fmt
  README.md
  CLAUDE.md
  DEVELOPER_GUIDE.md                      # short — how to add MCP/auth/profile
  .golangci.yml
  .tool-versions
  .gitignore
  .github/workflows/                      # single-module CI

  cmd/
    triagent/             main.go         # launcher binary
    triagent-mcp/         main.go         # MCP multiplexer (--kind=<name>)

  pkg/
    mcp/                                  # shared MCP abstraction
      server.go           # Server interface
      toolspec/           # ToolSpec type + JSON schema helpers
      subagent/           # shared sub-agent runner
      citations/          # citation runner + Validators
      telemetry/          # parent-tool-id attach, env scrubbing
      k8s/                # MCP packages (formerly mcp/internal/<name>/)
      git/
      wiki/
      slack/
      incidentio/
      sessions/
      strategies/
      meta/
      agentoperator/
      parallel/
      teleport/
      signalingest/
      entities/           # shared entity types used cross-MCP
    auth/                                 # k8s cluster-access providers
      cluster.go          # Provider interface (was investigate/internal/cluster)
      kubeconfig/         # default provider
      teleport/           # vendored slice of c1-sdk/teleport + adapter

  internal/                               # launcher internals
    server/  profile/  preflight/  k8sx/  editor/  sessions/  wiki/
    claude/  auto/  watches/  repos/  connections/  portforward/  web/

  profiles/
    default/                              # runnable starter (kubeconfig)

  system/                                 # system playbooks
  prompts/                                # prompt templates
  operator-skills/                        # operator-agent skill files

  frontend/                               # embedded Next.js
    app/  components/  lib/  hooks/  public/

  docs/
    superpowers/specs/                    # ADRs
    superpowers/plans/                    # scratch plans
```

**Absent from the target layout** (vs. today's repo): `/k8s`, `/ui`,
`/tools/generate-plugin`, `catalog.json`, every `plugin.json`, root
multi-module Makefile, `.github/scripts/sync-catalog.sh`, per-plugin
`bump-*` machinery, `profiles/camunda/`, `pkg/mcp/prom/`. (The prom MCP
package is moved into `pkg/mcp/prom/` transiently during step 3 of the
migration, then deleted in step 5; it never appears at HEAD.)

## 5. MCP abstraction (`pkg/mcp/`)

The current `mcp/internal/<name>/` shape carries forward: each MCP exposes
`New(Options) (*Server, error)` and `(*Server).Run(ctx)`, with a sibling
`specs.go` returning `[]toolspec.ToolSpec`. The move to `pkg/mcp/`:

- Promotes the shared cross-cutting packages from `mcp/internal/{toolspec,
  subagent, citations, telemetry}/` to `pkg/mcp/{toolspec,subagent,
  citations,telemetry}/` so external consumers can import them.
- Moves each MCP impl from `mcp/internal/<name>/` to `pkg/mcp/<name>/`.
- The multiplexer (`cmd/triagent-mcp/main.go`) is a thin file that
  imports each `pkg/mcp/<name>` and dispatches on `--kind=<name>`. Same
  shape as today's `mcp/cmd/serve.go`.

`Server` interface (illustrative):

```go
// pkg/mcp/server.go
package mcp

type Server interface {
    Run(ctx context.Context) error
    ToolSpecs() []toolspec.ToolSpec
}
```

The wire-name test (cross-checks handler registration against `ToolSpecs()`)
moves with each MCP package as `tools_wire_test.go`. The `c1-mcp dump-meta`
subcommand is deleted — it existed for the c1 ecosystem catalog, which
doesn't exist for the OSS project.

## 6. Dropping `c1-sdk`

### Replacing `plugin.Run`

`cmd/triagent/main.go` and `cmd/triagent-mcp/main.go` construct their
Cobra root commands directly and call `Execute()`. Version comes from a
`const`. `plugin.json` files are deleted across the repo.

The current early-intercept in `investigate/main.go` for
`c1 investigate clear watches` becomes a normal Cobra subcommand —
the bypass exists to dodge `c1-sdk`'s flag parsing, which is no longer
in the picture.

The "default to `start` when invoked with no args" behavior becomes a
`RunE` on the root command.

### Replacing `c1-sdk/teleport/{auth,k8s}`

Copy-vendored into `pkg/auth/teleport/`. The minimum surface:

- A package-level `auth` constants pair (`DefaultProxyAddr`,
  `DefaultAuthConnector`) → replaced with per-instance `Config{Proxy,
  AuthConnector}` passed to `NewProvider`. The profile already carries
  these fields (`auth.teleport.proxy`, `auth.teleport.auth_connector`).
- The `tsh`-subprocess-based provider (`ListClusters`, `Login`,
  `IsAuthenticated`, `EnsureAuthenticated`) — ~200–300 LOC of subprocess
  wrangling.

The result: `go.mod` has zero `github.com/camunda/*` entries.

## 7. Auth providers and the runnable default profile

### Provider interface

`investigate/internal/cluster/` becomes `pkg/auth/cluster.go`. Interface
unchanged:

- `Provider` — `ListClusters`, `Login`, `IsAuthenticated`.
- `Authenticator` (optional) — `EnsureAuthenticated(ctx)` for auto-login.
- `ReauthAdvisor` (optional) — `ReauthAdvice()` for user-facing re-auth
  copy. Used by `ConnectionsPanel.tsx` to drive the re-auth block instead
  of the hardcoded Camunda help-desk text.

### Provider construction

`cmd/triagent/main.go` selects the provider based on `profile.Auth.Kind`:

```go
func newAuthProvider(profAuth profile.Auth) (cluster.Provider, error) {
    switch profAuth.Kind {
    case "kubeconfig":
        return kubeconfig.NewProvider(), nil
    case "teleport":
        return teleport.NewProvider(teleport.Config{
            Proxy:         profAuth.Teleport.Proxy,
            AuthConnector: profAuth.Teleport.AuthConnector,
        }), nil
    default:
        return nil, fmt.Errorf("unknown auth.kind %q (supported: kubeconfig, teleport)", profAuth.Kind)
    }
}
```

### The `kubeconfig` provider

New package `pkg/auth/kubeconfig/` implementing `cluster.Provider`.

- `ListClusters(ctx)` — enumerates contexts in `$KUBECONFIG` (or
  `~/.kube/config`). Each context maps to a `cluster.Cluster{Name:
  ctx.Name, ID: ctx.Cluster}`.
- `Login(ctx, name)` — writes a per-session sub-kubeconfig that pins the
  named context, returns its path via `LoginResult.ContextName` (the
  launcher passes it as `KUBECONFIG` to all spawned subprocesses, which
  is the existing pattern per CLAUDE.md).
- `IsAuthenticated()` — returns true. Kubeconfig credentials are
  persistent; we never re-auth.
- No `Authenticator`, no `ReauthAdvisor`. The connections panel hides
  the re-auth block when `ReauthAdvice` is empty.

### The default profile

`profiles/default/profile.yaml`:

```yaml
name: default
description: |
  Runnable starter profile. Uses your local kubeconfig for cluster access.
  Copy this directory and override fields to make your own profile.
auth:
  kind: kubeconfig
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  playbooks_repo: ""
  wiki_repo: ""
  sessions_repo: ""
  prometheus:
    service: ""
    namespace: ""
    port: 9090
slack:
  channel_prefix: ""
linked_repos: []
extra_mcps: []
investigation_inputs:
  - id: incident_url
    label: Incident URL
    type: url
    optional: true
    hint: "Optional URL to the incident or ticket page for this investigation."
    prompt_keys:
      - { key: incident-url, value: "{{.value}}", if: '{{ne .value ""}}' }
  - id: slack_channel
    label: Slack channel
    type: slack_channel
    optional: true
    prompt_keys:
      - { key: slack-channel-id, value: "{{.id}}", if: '{{ne .id ""}}' }
      - { key: slack-channel-name, value: "{{.name}}", if: '{{ne .name ""}}' }
      - { key: slack-channel-url, value: "{{.url}}", if: '{{and (eq .id "") (ne .url "")}}' }
  - id: notes
    label: Notes
    type: textarea
    optional: true
    placeholder: "Symptoms, alerts, what you've seen…"
    prompt_keys:
      - { key: operator-notes, value: "{{.value}}", if: '{{ne .value ""}}' }
```

`profiles/camunda/` is deleted from this repo.

## 8. External-MCP plug-in surface

`profile.extra_mcps[]` supports two modes that share an `alias` +
`description`:

### Reference mode (preserved)

```yaml
extra_mcps:
  - alias: my-org-docs
    description: |
      Internal docs MCP. Use it for canonical CR field names and
      version-specific feature flags.
```

The launcher adds `mcp__my-org-docs__*` to the agent's `--allowedTools`
glob and threads `description` into the system prompt. The MCP server
itself must already be configured in the operator's Claude environment
(`~/.claude.json`, `claude mcp add`, or project `.mcp.json`); `--mcp-config`
overlays additively, so reference-mode entries remain reachable. The
launcher does NOT spawn it.

### Spawn mode (new)

```yaml
extra_mcps:
  - alias: prom
    description: |
      Curated Prometheus saturation queries (Zeebe backpressure, ES JVM,
      gateway 5xx, …). Use these for cluster health signals.
    command: c1-mcp
    args: ["serve", "--kind=prom"]
    env:
      C1_MCP_PROMETHEUS_URL: "${env:PROM_URL}"
      C1_MCP_PROMETHEUS_TOKEN: "${env:PROM_TOKEN}"
    allowed_tools: ["mcp__prom__*"]   # optional
```

The launcher writes `command`, `args`, `env` verbatim into the
per-session `.mcp.json` under server key `<alias>`. Claude Code spawns
the binary. The agent calls its tools as `mcp__prom__<tool>`.

Note on the example: `c1-mcp` and `C1_MCP_PROMETHEUS_*` here are the
*c1-plugins* binary's own names (its repo retains the multi-kind
binary that triagent is replacing with `triagent-mcp`). A triagent
operator using c1-plugins' prom MCP sets those env vars in their shell
and `command: c1-mcp` points at the binary that c1-plugins ships.
None of those names are triagent-side identifiers.

### Mode discrimination

`command` set → spawn mode. `command` absent → reference mode. A single
profile can mix both.

### `${env:NAME}` interpolation

Resolved from `os.LookupEnv` at config-write time. The only interpolation
form. If a referenced env var is missing, preflight returns a clear
"missing required env var $NAME for extra_mcp alias=<x>" error that
surfaces in the start panel. The operator sets the var in their shell or
a wrapper script.

### `allowed_tools`

Optional in both modes. When absent, the allowed-tools glob is
`mcp__<alias>__*`. When present, it narrows to specific tool names.

### Switch-context behavior

External MCPs are pinned to their boot config. If the operator runs
`switch_context` to a different cluster, the URL/token threaded in via
`env:` is not updated; the external MCP stays attached to its original
target. This is documented in `frontend/public/docs/connections.md` (new).
The v2 design (out of scope) introduces a documented launcher control
plane for external MCPs that want context-following.

### Code changes (step 6 in section 10)

- `profile.ExtraMCP` struct: rename `Name` → `Alias`; add `Command`,
  `Args`, `Env`, `AllowedTools`.
- `internal/preflight/mcpconfig.go::writeMCPConfig`: walk
  `prof.ExtraMCPs`; for each spawn-mode entry, emit a server entry into
  the output `.mcp.json`. Resolve `${env:NAME}` in `Env` values.
- `internal/sessions/session.go::allowedTools` and the equivalent in
  `internal/editor/editor.go`: walk both modes and append the
  per-entry glob (`AllowedTools` if set, else `mcp__<alias>__*`).
- New test: one reference + one spawn entry in profile, both correctly
  reflected in the allowed-tools list and the `.mcp.json`.

## 9. Frontend and docs rebrand

### Frontend strings

- `app/layout.tsx`, `app/(main)/investigations/new/page.tsx`:
  `"c1 investigate"` → `"triagent"`.
- DOM event names: `c1-investigate:*` → `triagent:*` (~6 call sites).
- localStorage keys: `c1-investigate.*` → `triagent.*`.
- `frontend/package.json` `name`: `c1-investigate-frontend` →
  `triagent-frontend`.
- Tool-prefix examples and test fixtures: `c1-k8s`, `mcp__c1-*` →
  `triagent-k8s`, `mcp__triagent-*`. Repo defaults in tests (`camunda/c1`,
  `camunda/zeebe`) → neutral (`example-org/example-repo`).

### ConnectionsPanel

`ConnectionsPanel.tsx:155, 181`: replace the two "File a *General Help
Request* on the Camunda IT help-desk" blocks with provider-neutral
instructions. The Slack block teaches how to create a Slack app, scope
the OAuth scopes (`channels:history`, `channels:read`, `groups:history`,
`groups:read`, `users:read`, plus whatever the slack MCP currently
needs), install to the workspace, and paste the bot token. The
incident.io block links to incident.io's API token settings and explains
the minimum scopes.

The re-auth block at the top of the panel is driven by
`cluster.Provider.ReauthAdvisor.ReauthAdvice()` (already implemented).
Empty string → block hidden. Kubeconfig returns empty.

### MCP wire names

| Today | After |
|---|---|
| `c1-k8s` | `triagent-k8s` |
| `c1-prom` | (gone, see section 10 step 5) |
| `c1-mcp` (binary alias key in `.mcp.json`) | `triagent-mcp` |
| `c1-meta` | `triagent-meta` |
| `c1-strategies` | `triagent-strategies` |
| `c1-teleport` | `triagent-teleport` |
| `c1-wiki` | `triagent-wiki` |
| `c1-slack` | `triagent-slack` |
| `c1-incidentio` | `triagent-incidentio` |
| `c1-parallel` | `triagent-parallel` |
| `c1-git-<alias>` | `triagent-git-<alias>` |
| `c1-agent-operator` | `triagent-agent-operator` |
| `c1-signal-ingest` | `triagent-signal-ingest` |
| `c1-sessions` | `triagent-sessions` |

`MCPAlias*` consts in `internal/preflight/mcpconfig.go`, all test
fixtures referencing the old aliases, and the frontend's tool-prefix
heuristics all update together.

### Telemetry env vars

`C1_MCP_TELEMETRY_URL`, `C1_MCP_TELEMETRY_TOKEN`, `C1_MCP_TRACE_ID`,
`C1_MCP_TELEMETRY_TOOL_PREFIX` → `TRIAGENT_MCP_*`. Both ends
(`cmd/triagent-mcp/`, `internal/server/`) change atomically.

### Storage paths

`~/.config/c1/plugins/investigate/...` → `~/.config/triagent/...`.
`<XDG_CACHE_HOME>/c1-mcp/...` → `<XDG_CACHE_HOME>/triagent-mcp/...`.
Existing local state is abandoned — no migration shim, no OSS users yet.

### In-app docs

`frontend/public/docs/*.md` are rewritten to be project-agnostic:

- `overview.md`, `investigations.md` — drop Camunda framing; use generic
  examples ("a workload cluster", "your incidents wiki vault").
- `mcp.md` — Slack/incidentio sections gain a short "getting your own
  token" blurb. The Prometheus section is removed (and replaced or
  merged with a new "writing your own MCP" section that uses prom as
  the worked example for spawn-mode `extra_mcps`).
- `repos.md`, `wiki.md`, `playbooks.md` — strip Camunda-as-the-example
  framing.
- New page `connections.md` walking through Slack-app creation and
  incident.io token setup. Linked from `ConnectionsPanel.tsx`.

### CLAUDE.md

The Camunda-specific guidance (linked-repos lists, incident URL formats,
`c1-plugins` repo references in "naming & terminology", `c1-k8s`
examples, the "Profile abstraction (Camunda-specific vs neutral)"
section's framing) is rewritten as project-agnostic. The engineering
rules survive: anti-patterns, sub-agent runner / citation runner /
profile abstraction principles, commit conventions, testing hygiene.

### README.md and DEVELOPER_GUIDE.md

`README.md` is rewritten for the OSS audience: project pitch, install,
run, link to docs. Replaces the existing "plugin repository for the c1
CLI" framing.

`DEVELOPER_GUIDE.md` is rewritten or replaced with a short guide on how
to add: an MCP (under `pkg/mcp/`), an auth provider (under `pkg/auth/`),
a profile (under `profiles/` or via `--profile <path>`), an external MCP
(via `extra_mcps[]`).

## 10. Migration sequencing

### The 8-commit local sequence

Each commit must leave green:

```
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
cd frontend && npm run typecheck && npm run build && npm test -- --run
```

| # | Commit | Net effect |
|--|--|--|
| 1 | **Baseline.** `git add` the camunda port verbatim. | Reference point for the rest of the sequence. |
| 2 | **Reshape to single module.** Delete `/k8s`, `/ui`, `/tools`, `catalog.json`, root multi-module Makefile, `.github/scripts/sync-catalog.sh`, every `plugin.json`. Rename module path → `github.com/sourcehawk/triagent`. Fold `investigate/` + `mcp/` into root: `cmd/triagent/`, `cmd/triagent-mcp/`, `internal/`, `frontend/`, `system/`, `prompts/`, `operator-skills/`. One `go.mod` at root. New top-level `Makefile`. | Final Go module surface. Imports rewritten. Frontend embed path updated. |
| 3 | **Lift MCP abstraction.** Move `mcp/internal/<name>/` → `pkg/mcp/<name>/`. Promote `toolspec`, `subagent`, `citations`, `telemetry` to `pkg/mcp/{...}/`. Update imports. Drop the `dump-meta` catalog subcommand. | MCPs publicly importable. Shared abstraction at `pkg/mcp/`. |
| 4 | **Drop c1-sdk.** Replace `plugin.Run` with plain Cobra in both binaries. Move `investigate/internal/cluster/` → `pkg/auth/cluster.go`. Copy-vendor needed slice of `c1-sdk/teleport/{auth,k8s}` into `pkg/auth/teleport/`. Remove `c1-sdk` from `go.mod` / `go.sum`. Version becomes a `const`. | Zero `github.com/camunda/*` deps. Standalone binaries. |
| 5 | **Evict prom MCP.** Delete `pkg/mcp/prom/`. Delete launcher prom port-forward goroutine and prom control endpoint (`internal/server/handlers_prom_internal.go`). Remove `EnvProm*`, `MCPAliasProm` from `mcpconfig.go`. Drop prom from `allowedTools` in `internal/sessions/session.go` + `internal/editor/editor.go`. | Prom is purely an external concern. Smaller launcher. |
| 6 | **External-MCP plug-in surface.** Extend `profile.ExtraMCP`: rename `Name` → `Alias`; add `Command`, `Args`, `Env`, `AllowedTools`. Walk spawn-mode entries in `mcpconfig.go::writeMCPConfig`. Add `${env:NAME}` interpolation with missing-env preflight error. Walk both modes in allowed-tools builders. Tests for reference + spawn. Delete `profiles/camunda/` here (its `extra_mcps:` uses the old `name:` field, so it must go with the rename to keep the build green). | Third-party MCPs wirable via profile only. |
| 7 | **Pluggable auth + runnable default profile.** Add `pkg/auth/kubeconfig/`. Replace hard-wired `newAuthProvider("teleport")` with profile-driven switch. Rewrite `profiles/default/profile.yaml` to `auth.kind: kubeconfig` with neutral inputs. | Default profile boots without configuration. |
| 8 | **Rebrand.** Frontend strings (title, DOM events, localStorage, ConnectionsPanel copy). MCP wire renames `c1-*` → `triagent-*`. Telemetry env vars `C1_MCP_*` → `TRIAGENT_MCP_*`. Storage paths. Frontend `public/docs/*.md` rewritten + new `connections.md`. CLAUDE.md neutralized. README.md and DEVELOPER_GUIDE.md rewritten. | No Camunda strings remain. |

### Finalization (after commit 8)

1. Run the verification suite at HEAD one last time.
2. `git reset --soft <baseline-parent>` and create a single squashed
   commit. No Camunda-internal history is published.
3. Push to the public remote.

## 11. Out of scope (follow-on specs)

These each get a separate short spec after the restructure lands.

- **Pluggable k8s auth providers.** Add `gcloud`, `aws-iam`, or other
  providers beyond `kubeconfig` and `teleport`. The interface is already
  in place; this is mostly about wiring + writing a new provider.
- **Dynamic context for external MCPs.** Either restart-on-`switch_context`
  or a documented launcher control plane (HTTP endpoint or unix socket)
  so external MCPs can follow cluster swaps. Driven by a real second
  consumer; not designed speculatively.

## 12. Risks and watch-outs

- **Module rename touch fan-out.** Step 2 rewrites every `.go` import
  line. Easy to miss test files. Mitigation: a single sed pass across
  `*.go`, plus a clean `go build ./...` before committing.
- **Frontend embed anchor.** `internal/web/dist/.gitkeep` is the tracked
  anchor that lets `//go:embed all:dist` compile before `make build`
  populates the directory (per CLAUDE.md). Preserve it through the
  layout reshape; ensure the `.gitignore` negation rule moves too.
- **Telemetry env-var rename atomicity.** Both ends (the MCP binary that
  reads them, the launcher that writes them) must change in the same
  commit (step 8) — verify with `tools_wire_test.go` in each MCP.
- **Existing browser localStorage.** Operator's local prefs under
  `c1-investigate.*` keys reset on first run after step 8. Acceptable —
  no OSS user base yet, and the dev install is the only consumer.
- **`extra_mcps` field rename `name` → `alias`.** A breaking change to
  the profile schema. No deprecation shim because no OSS install base
  exists. The `c1-plugins` Camunda profile (in its own repo) updates at
  the same time as it migrates to triagent.

## 13. Open questions

- **Binary version source.** `const` is the recommendation. If a
  build-time injection (`-ldflags "-X main.version=…"`) is preferred,
  swap during step 4.
