@AGENTS.md

# triagent — contributor guidelines

Code is the source of truth for *what*; `AGENTS.md` is the source of truth for *where*; architectural decisions live under `docs/adrs/`; this file is the operational *how* and *don't*.

## Feature-development workflow

The feature-development workflow skills ship as the **`feature-dev-workflow`** plugin (enabled in `.claude/settings.json` under `enabledPlugins`), not from this repo. Invoke `feature-dev-workflow:planning-a-feature` at feature conception and let each skill's `**REQUIRED SUB-SKILL:**` markers walk you through the rest (planning → issues → plan → developing → fan-out → review → PR). The end-to-end flow and its diagram are documented in the plugin repo (`github.com/sourcehawk/feature-dev-workflow`).

### Editing the workflow skills

These skills live in the `feature-dev-workflow` plugin (`github.com/sourcehawk/feature-dev-workflow`), not in this repo — edit them there, then bump the enabled version here:

- **Invoke `superpowers:writing-skills` before creating or modifying any skill** and follow its RED → GREEN → REFACTOR loop.
- **Skill `description:` is "Use when..." triggers only, never a workflow summary** — a summary becomes a shortcut agents take instead of reading the body.

## Operational rules

- **TDD is the standard.** Failing test → watch it fail for the right reason → implement. One commit per task.
- **Tests assert with `testify`.** Use `github.com/stretchr/testify/assert` for checks the test should keep running past, and `require` for preconditions a failure must stop at (a non-nil error before a dereference, setup that must succeed). Bare `t.Fatal` / `t.Errorf` is the rare exception, not the default.
- **Before claiming done: `make test` + `make lint`; if `frontend/` touched, also `cd frontend && npm run typecheck`.** CI gates all three; local is the cheapest place to catch failures. Race-clean is non-negotiable.
- **Commit conventions:** `feat(<area>): ...`, `fix(<area>): ...`, `refactor(<area>): ...`, `test(<area>): ...`, `chore(<area>): ...`. Area mirrors the module path.
- **Never `--no-verify`, never `git add -A` / `git add .`.** Stage by name; pre-commit hooks exist for a reason.
- **No GitHub mutation without a fresh confirmation against the specific body about to land.** Paste body inline, name target, wait for an explicit yes.
- **PR titles outlive lifecycle state.** No `wip` / `draft` / `plan` suffixes — GitHub's chip carries lifecycle.

## Code & writing style

- **Lead with the rule, follow with the why.** One idea per paragraph. When refining in response to feedback, re-read cold and cut anything that only makes sense knowing what just changed — refinement context belongs in the commit message, not the file.
- **No residue category in code.** No `_legacy` / `_old` / `_v2` siblings, no `// removed because X` comments, no renamed-but-unused shims. Unused → delete it. Load-bearing → keep under its real name.
- **Complete underdeveloped code paths you're already touching.** Add the missing tests + edge-case handling; explicitly name anything you found but deliberately deferred so it isn't silently inherited. Still scope-disciplined: this is permission to complete what you're in, not licence to refactor adjacent modules.

## Project facts (terse)

- **Single binary, two subcommands.** `triagent` (launcher) + `triagent-mcp --kind=<server>`. New MCP = new `pkg/mcp/<name>/` package + one `case` in `cmd/triagent-mcp/serve.go`. See [ADR-0001](docs/adrs/0001-mcp-binary-architecture.md).
- **Sub-agent tools** route through `pkg/mcp/subagent` with an explicit `AllowedTools` whitelist (empty = error). Default timeout 5 min. See [ADR-0002](docs/adrs/0002-subagent-runner.md).
- **Two URL spaces, two auth modes:** `POST /api/internal/...` (bearer, MCP-to-launcher), `POST /api/...` (cookie, browser-to-launcher). `mcpconfig.go` is the single source of truth for per-session MCP wiring. See [ADR-0003](docs/adrs/0003-mcp-launcher-ipc.md).
- **Walker vocabulary is locked.** Use "walker" / "step suggester" / "guided flow" — never "state machine". Transitions are `delegate_to` (sub-flow, returns) or `handoff` (terminate + jump). See [ADR-0004](docs/adrs/0004-walker-semantics.md).
- **One global `<StreamProvider>` per tab, multiplexed SSE.** Never per-component `EventSource`. DOM events use `triagent:` prefix; localStorage uses `triagent.` prefix. See [ADR-0005](docs/adrs/0005-multiplexed-sse.md).
- **URL is the source of truth for view state.** Next App Router + route groups (`app/(main)/`); `usePathname` / `useSearchParams`. See [ADR-0006](docs/adrs/0006-url-as-view-state.md).
- **`events.jsonl` is the source of truth for cluster/context history.** Long-running work uses a manager-rooted goroutine, not `r.Context()`. Persistent in-progress flags get an orphan-recovery sweep on `Restore()`. See [ADR-0007](docs/adrs/0007-events-jsonl-source-of-truth.md).
- **Deployment-specific defaults load from a profile** (`internal/profile/`), not from Go constants or `//go:embed`. Hot-swappable client state uses `atomic.Pointer[snapshot]`. Every spawned subprocess carries explicit `KUBECONFIG`. See [ADR-0008](docs/adrs/0008-profile-abstraction.md).
- **Auto mode is a second `claude` session** with `triagent-mcp --kind=agent-operator`. Takeover/resume is first-class via phase machine. See [ADR-0009](docs/adrs/0009-auto-mode-architecture.md).

## Conventions & terminology

- **`signal-watches`** is the polling/auto-spawn subsystem — not "alert rules" / "monitor".
- **Wiki frontmatter: `links` (all-optional URLs), `entries/` folder, free-form slug identifier.** No `INC-...` regex.
- **Investigation field is `incidentURL` (camelCase) / `incident_url` (snake_case JSON).** URL form is canonical.
- **Per-repo MCPs are aliased `triagent-git-<alias>`.** Don't introduce a new top-level MCP when adding per-repo tools.
- **Storage:** `~/.config/triagent/...` for launcher state; `<XDG_CACHE_HOME>/triagent-mcp/...` for caches. Git-backed vaults (wiki, sessions, playbooks) live under launcher state.
- **Atomic writes via tmp-file-then-rename** (existing `atomicWrite` helper). Never write metadata in place.
- **Frontmatter / YAML round-trips use `gopkg.in/yaml.v3`,** through a single helper module per artifact.
- **Frontend embed:** `internal/web/dist/.gitkeep` is the tracked anchor that lets `//go:embed all:dist` compile on a fresh checkout. Don't remove it.
- **Read the spec, not the plan.** Specs in `docs/superpowers/specs/` are durable. Plans in `docs/superpowers/plans/` are scratch and get deleted once the plan ships. If a spec says X and code does Y, raise it — don't quietly rewrite either.
- **Prefer extracting a shared helper to copy-pasting a second consumer.**

## Refining files

When refining any file in response to feedback, the result reads as if it had always been that way. Don't leave residue about the change itself: no "we used to do X," "this no longer includes Y," or "do not do Z" tails for things no longer in play. Re-read the changed section cold; cut anything that only makes sense knowing what just changed. Refinement context belongs in commit messages and PR descriptions, not the file.