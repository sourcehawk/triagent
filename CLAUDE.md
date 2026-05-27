@AGENTS.md

# triagent — contributor guidelines

Durable rules, conventions, and rationale. Code is the source of truth for "what"; `AGENTS.md` is the source of truth for "where"; this file is the source of truth for "why" and "don't". Lead with the rule, follow with rationale where non-obvious.

## MCP plugins

- **MCPs live in `pkg/mcp/<kind>/`, exposed via `cmd/triagent-mcp/serve.go`.** One `triagent-mcp` binary with `--kind=<server>` subcommands (`git`, `wiki`, `slack`, `incidentio`, `k8s`, `sessions`, `strategies`, `meta`, `agent-operator`, `parallel`, `teleport`, `signal-ingest`, etc.). Adding a new MCP = new `pkg/mcp/<name>/` package + one `case` in `cmd/triagent-mcp/serve.go`.
- **Each MCP package exposes `New(Options) (*Server, error)` + `(*Server).Run(ctx)` and a sibling `specs.go` with `ToolSpecs() []toolspec.ToolSpec`.** The catalog feeds `triagent-mcp dump-meta`; keep it in sync with handler registrations or the wire test fails.
- **MCP server descriptions and tool prose stay product-neutral.** Never bake "incident-response" / "investigator" framing into an MCP-side tool description — that's the consumer's framing. Use description passthrough at the consumer side instead.
- **Prefer small, configurable tool surfaces over per-type tool families.** When tempted to ship `do_thing_for_X`, `do_thing_for_Y`, ask whether one parameterised tool would do. Exception: for vast/custom telemetry namespaces (Prom, k8s) with a few bounded high-value signals, **curated tools beat raw query** — complementary, not contradictory.
- **Sub-agent tools run via the shared `pkg/mcp/subagent` runner.** Pass an explicit `AllowedTools` whitelist; empty is an error (no implicit defaults). The runner strips telemetry env vars and writes an empty MCP config to the sub-agent so it can't reach back to parent MCPs.
- **Default sub-agent timeout is 5 min.** Per-call overrides for legitimately longer work (`draft_pr` runs test suites). 90s was historical and too tight once analyze_change / cold clones / draft_pr arrived.
- **Sub-agent telemetry attaches to the parent tool id** via `telemetry.CurrentToolID(ctx)` / `ParentToolID` — events render as nested children in the activity panel.
- **Citation contract for sub-agent prose tools is shared.** Use `pkg/mcp/citations`: tools emit a delimited tail block, the runner parses / cross-checks `[N]` markers / shape-checks / does one corrective retry. Each kind has its own `Validator`. Don't reinvent.

## MCP ↔ launcher IPC

- **Two URL spaces, two auth modes:**
  - `POST /api/internal/...` — MCP-to-launcher, bearer auth via `telemetryToken` from `TRIAGENT_MCP_TELEMETRY_TOKEN`.
  - `POST /api/...` — browser-to-launcher, cookie auth.
  Never collapse them; cookies don't make sense MCP-side and bearer leaks would defeat browser CSRF protections.
- **Launcher-bound MCPs read `TRIAGENT_MCP_TELEMETRY_URL`, `TRIAGENT_MCP_TELEMETRY_TOKEN`, `TRIAGENT_MCP_TRACE_ID`** and POST to a loopback endpoint scoped by trace id. Same pattern for `meta`, `agent-operator`, and any future "agent talks back to launcher" surface.
- **Per-investigation MCP config is written once at preflight** to a session-specific path; `mcpconfig.go` is the only file that decides which MCPs attach to a session. Conditional attachment (e.g. `triagent-slack` only if Sources says so) lives there, not at the handler level.

## Walker / strategies (system playbooks)

- **Use "walker" / "step suggester" / "guided flow" — never "state machine"** when describing the playbook engine. Same semantics, but the vocabulary is locked.
- **System playbooks live in `system/*.yaml`** and are picked up by `embed.go`'s `*.yaml` glob — no manual registration. Adding a playbook = drop the YAML, run the embed test.
- **`handoff` terminates the current walk and hands off to another playbook; `delegate_to` walks a sub-flow and returns.** Mutually exclusive with `terminal_advice` / `handoff` / `suggested_calls`; requires `next`. A delegate must end in non-handoff terminals — runtime rejects otherwise. Findings from the delegate land on the parent's findings map (single audit trail).
- **Cross-playbook id resolution is soft** (typos surface at runtime, not in single-doc validate). Don't try to make it hard — playbooks are loaded as a set, and validation across the set happens at load time.

## Editor / proposal flows

- **Proposals coexist per session.** The resolution-ledger + proposal-files semantics support N concurrent proposals (per playbook id, latest wins). When in doubt, project transcript → `Map` keyed by `playbook_id` (helper: `proposal-projection.ts`).
- **Two surfaces share the diff body** (chat-side `ProposalCard` and editor's `ProposalPreview`) via `ProposalBodyTabs`. The pure diff helper (`playbook-diff.ts`) decides _what_ changed; `PlaybookGraph` decides _how_ to draw it. Don't fold diff logic into the React tree.
- **Editor session continuity uses query-param routing** (`/playbooks?playbook=<id>`), not path segments — accepting a proposal from `__new` would otherwise unmount the chat. Rebinding the editor session's `Subject` happens server-side via `POST /api/editor-sessions/{id}/rebind` under the manager lock.
- **`editor.Session` carries a polymorphic `Subject` (kind + key + opaque per-kind data) and a `Sources` block.** Same Session/Manager/drawer/SSE machinery for playbook editing, wiki editing, and future kinds — branch on `Subject.Kind` in the prompt builder.

## Frontend (Next.js / React)

- **One global `<StreamProvider>` owns a single multiplexed `EventSource` per tab.** Per-component code subscribes via filter API. **Never open per-scope SSE connections** — Chrome's HTTP/1.1 per-origin connection pool (6) blows up on rapid sidebar nav. Backlog is delivered via REST `/transcript` endpoints + a seq-dedup'd live stream.
- **The URL is the source of truth for view state.** Routing uses Next App Router + route groups (`app/(main)/`). Never reimplement URL state — `usePathname` / `useSearchParams` are it. The legacy `readURLState` / `writeURLState` / `View` discriminator is dead; don't reintroduce.
- **Shared chrome (TopNav + Sidebar) lives once in a route-group layout** (`app/(main)/layout.tsx`). Wiki has its own layout under `app/wiki/layout.tsx` but uses the same `<TopNav>` / `<AppShell>` building blocks.
- **Frontend tests use Vitest in node mode** for `lib/` pure-logic helpers. Components are verified by inspection / smoke; only reach for jsdom via `/* @vitest-environment jsdom */` pragma per-file when actually required.
- **DOM custom events use the `triagent:` prefix**; localStorage keys use the `triagent.` prefix. Don't introduce any other namespace prefix for new events or storage keys.

## Backend (Go / launcher)

- **Each subprocess we spawn carries explicit `KUBECONFIG` (and any other context env) — never inherit ambient operator shell state.** Operator's mid-session `kubectx` switch must not leak into running investigations.
- **`atomic.Pointer[snapshot]` for hot-swappable client state** (k8s ToolKit, prom URL). Lets `switch_context` / lazy-prom-attach work without locking and without restarting the MCP.
- **Persistent in-progress flags get an orphan-recovery sweep on launcher restart.** Any `PushInProgress: true` etc. that survives a restart belongs to a goroutine that no longer exists — convert it to an explicit error state on `Restore()`, don't pretend it's still running.
- **Long-running work (push-PR, sub-agent generation) survives client disconnect** by running on a goroutine with a context derived from the manager's parent, not from `r.Context()`. State transitions emit SSE so any open tab picks them up. Don't tie work lifetime to a request.
- **Single-flight by `<owner>/<name>` (or equivalent) for any work the user can re-trigger** (repo architecture summary, etc.). The worker keeps an in-process registry keyed by the natural id; a concurrent kick is a no-op, not a double-spend.
- **`events.jsonl` is the source of truth for cluster/context history** — don't persist `ContextName` / `ClusterID` on `Investigation`. Sessions can touch multiple clusters; the event log carries the full record.
- **Wire callback seams for cross-package events** (e.g. `Forward func(Event)` on `editor.Session`) rather than per-package subscriber pools. The launcher's Manager owns the single fan-out point.
- **Frontmatter / YAML round-trips use `gopkg.in/yaml.v3`.** Frontmatter parse+write should round-trip through a single helper module per artifact (wiki, repo-summary, etc.) — don't inline yaml parsing in handlers.
- **Storage: `~/.config/triagent/...` for launcher state; `<XDG_CACHE_HOME>/triagent-mcp/...` for caches.** Git-backed vaults (wiki, sessions, playbooks) live under the launcher state dir and are `git init`'d on first launcher run.
- **Atomic writes via tmp-file-then-rename** (existing `atomicWrite` helper). Never write metadata in place.

## Testing & build hygiene

- **TDD is the standard.** Write a failing test, run it, watch it fail for the right reason, then implement. Never bundle two unrelated changes in one commit.
- **Go test fixtures use `t.TempDir()`** for filesystem, and disable git GPG signing via `GIT_CONFIG_NOSYSTEM=1` + `commit.gpgsign=false` (see `pkg/mcp/git/tools_test.go::initFixtureRepo`). Configure `user.name` / `user.email` on init or commits hang on CI.
- **`go test -race -count=1 ./...` is the canonical backend test.** Race-clean is non-negotiable — the launcher fans out across goroutines.
- **Frontend: `npm test -- --run`** plus `npm run typecheck` and `npm run build`. Build is fast; run it for any layout / routing change.
- **Commit conventions:** `feat(<area>): ...`, `refactor(<area>): ...`, `fix(<area>): ...`, `test(<area>): ...`, `chore(<area>): ...`. Areas mirror the module path (`pkg/mcp/git`, `internal/server`, `frontend`, `watches`).
- **Never `--no-verify`, never `git add -A` / `git add .`.** Pre-commit hooks exist for a reason; staging by name keeps secrets and stray files out.
- **Long pipelines don't stop on transient failures.** Autonomous runs continue past a commit/build hiccup and surface it in the end-of-run summary — don't bail on the first non-blocking error.
- **Frontend embed:** `internal/web/dist/.gitkeep` is the tracked anchor that lets `//go:embed all:dist` compile on a fresh checkout before `make build` populates the directory. Don't remove it; the `.gitignore` negation rule depends on it.

## Completing underdeveloped code paths

- **When you touch a code path that is clearly underdeveloped — repeatedly buggy, missing tests, edge cases unhandled — finish it rather than punt.** "Wasn't covered before so it's not my problem" leaves the gap for the next person and the cycle repeats. If you're already reading and modifying the code, you're the cheapest person to add the missing test, handle the nil case, fix the off-by-one you noticed. The general "don't expand scope" rule yields when the surface area you're already in is demonstrably immature.
- **Signals the surface is underdeveloped:** the same area has been patched repeatedly, a branch you're modifying has no test coverage, empty / nil / error / concurrency inputs are handled inconsistently or not at all, `TODO` / `FIXME` markers are still in place, behaviour is described in a comment but never asserted in a test.
- **"Finish it" means: add the missing tests including the edge cases, fix the bugs the tests surface, and explicitly name anything you found but deliberately aren't fixing in this change** so it isn't silently inherited. Don't leave a known-broken edge case behind because the original ticket didn't enumerate it.
- **Still scope-disciplined.** This is permission to complete what you're already touching, not licence to refactor adjacent modules. If the gap is two files over from your change, surface it in the PR description; don't pull it in.

## Naming & terminology

- **Use `signal-watches`** for the polling/auto-spawn subsystem — not "alert rules" / "state machine" / "monitor".
- **Wiki frontmatter: `links` (all-optional URLs), not `sources`.** `entries/` folder, not `incidents/`. Identifier is a free-form slug, not `INC-...` regex.
- **Investigation field is `incidentURL` (camelCase) / `incident_url` (snake_case JSON)** — not `incidentId`. URL form (`https://app.incident.io/.../incidents/<n>`) is canonical; the `INC-...` shape is gone.
- **Per-repo MCPs are aliased `triagent-git-<alias>`** — tools register on each one (e.g. `search_issues`, `create_github_issue`, `draft_pr` for codefix). Don't introduce a new top-level MCP when adding per-repo tools.
- **Sub-flow vs handoff:** "the playbook _delegates_ to / hands off to". Don't say "calls" or "invokes" — those imply something the walker isn't doing.

## Profile abstraction

- **All deployment-specific defaults, repo lists, namespace derivation, prompt content, investigation input fields, and external-MCP references load at runtime from a profile** (`internal/profile/`), not from baked-in Go constants or `//go:embed` strings.
- **`pkg/mcp/k8s/default_kinds.json` is platform-neutral.** Custom CRDs belong in a deployment-specific profile, not in the MCP binary.
- **Profile loads by name (embedded) or by path on disk.** Profiles support a `base:` key for merging.
- **`investigation.yaml` and other system playbooks use semantic-role keys** (e.g. two-bucket scope) rather than deployment-specific terminology. The profile supplies the deployment-specific terms.

## Auto mode (agent-as-operator)

- **The operator agent is a second `claude` session per investigation,** with its own MCP (`triagent-mcp --kind=agent-operator`) exposing `send_message` / `finish` / `request_takeover`. It watches the multiplex stream and resumes on each `end` envelope with a diff prompt.
- **Take-over / resume is first-class.** The watcher gates on a phase machine; a take-over flips it, a resume hands control back with a raw-event catch-up briefing.

## Anti-patterns rejected (don't reintroduce)

- **Per-version file storage (`<id>.vN.yaml`).** Replaced by git history of `<id>.yaml`. `version:` field is dead. Approve-only branch on proposals is dead — approve = write + activate; decline is the only alternative.
- **Wiping persistent caches on launcher boot.** Slack MCP cache is loaded from JSON sidecars on startup; `triagent clean` is the manual escape hatch.
- **Per-component `EventSource`** — see Frontend section.
- **Server-level namespace binding for k8s MCP.** Namespace is per-call; `list_namespaces` discovers them. `clusterId` is optional; preflight enforces "at least one of (cluster, incident URL, slack channel, notes)".
- **Pinning a kube context at session start.** Replaced by `triagent-teleport` MCP for discovery/login + `switch_context` on `triagent-k8s` MCP. Prom port-forward is launcher-managed and reactive to `switch_context` telemetry.
- **Approve-via-side-channel proposal flows.** Eligibility is derived from the transcript client-side; the approve/decline path is the single source of truth.
- **`view` state as a single discriminator in `app/page.tsx`.** See Frontend.
- **Synthetic first-user messages for agent flow control.** Use system-prompt augmentation (closing block) instead — no race, no new endpoint, agent acts on its very first turn.

## GitHub & external mutations

- **No GitHub mutation without a fresh confirmation against the specific body about to land.** Generic earlier intent ("yes please open an issue") ≠ standing consent for the body now. Paste the full proposed body inline in chat, name the target (`<org>/<repo>` or `#<num>`), and wait for an explicit "yes" before `gh issue create|edit` / `gh pr create|edit`. Absence of objection is not a yes — wait for the affirmative.
- **PR titles outlive the state that named them.** Don't suffix with `wip`, `draft`, `plan`, `scaffolding`, etc. — GitHub's draft/ready chip carries the lifecycle state. A single title from open through merge avoids renames and stale wording in the merged record.

## Writing style

- **When refining a file in response to feedback, the result reads as if it had always been that way.** No "we used to do X," "this no longer includes Y," or "do not do Z" tails for things no longer in play. Re-read the changed section cold; cut anything that only makes sense knowing what just changed. Refinement context belongs in the commit message, not the file.
- **Lead with the rule, follow with the _why_** in CLAUDE.md and any contributor-facing prose. Don't pad with examples that restate the rule; one idea per paragraph.
- **Don't introduce a residue category in code either.** No `_legacy` / `_old` / `_v2` sibling files left dangling, no `// removed because X` comments where the deletion would suffice, no renamed-but-unused `_var` shims. If a thing is unused, delete it; if it's load-bearing, keep it under its real name.

## Editing local skills

- **No `.claude/skills/` directory exists today.** If/when one is added, invoke `superpowers:writing-skills` before creating or modifying any skill in it, and follow its RED → GREEN → REFACTOR loop. The methodology (test-driven, anti-patterns named, rationalizations closed in prose) is what makes a skill survive pressure; skipping it produces skills that look fine in review and fail on the next pushback turn.
- **Skill `description:` is "Use when..." triggers only, never a workflow summary.** Agents read the description to decide whether to load the skill; a workflow summary becomes a shortcut they take instead of reading the body.
- **Name rationalizations explicitly in discipline content.** Pressure-induced shortcuts (gate skipping, premature completion claims, "I already confirmed earlier") belong in an anti-patterns or red-flag table with the correct counter-action. For schema content (template fields, section lists), the inverse: list only what's included and let absence speak — "no X field" tails are noise in a schema, not safety.

## When in doubt

- **Read the spec, not the plan.** Specs in `docs/superpowers/specs/` are durable ADRs, referenced from code, and never deleted or rewritten history-style. Plans in `docs/superpowers/plans/` are scratch and get deleted once the plan ships. If a spec says X and code does Y, raise it — don't quietly rewrite either.
- **Prefer extracting a shared helper to copy-pasting a second consumer.** The sub-agent runner, the citation runner, the FilterableList, the diff helper, the proposal-projection helper — all live in shared modules because the second consumer paid the extraction cost.
- **One commit per task; keep the build green between commits.** Migrations land alongside their callers (or behind a flag); never check in a `_legacy` file and a `_new` file in the same commit unless the second consumer is migrated in the same commit.
