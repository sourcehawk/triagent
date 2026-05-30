# AGENTS.md: triagent

## Purpose

Localhost web app that pairs a Claude reasoning loop with a typed MCP tool catalog, a guided playbook walker, and a
persistent team wiki to drive Kubernetes incident triage end-to-end. Ships as two binaries (`triagent` launcher +
`triagent-mcp` multiplexer); the Next.js frontend is embedded in the launcher.

## Top-level structure

```
cmd/
  triagent/             launcher binary (HTTP server, session manager, browser entry point)
  triagent-mcp/         MCP multiplexer binary; one process per --kind=<server> subcommand
pkg/
  mcp/<kind>/           individual MCP servers (git, wiki, slack, incidentio, k8s, sessions,
                        strategies, meta, agentoperator, parallel, teleport, signalingest, …)
                        each: New(Options), Run(ctx), specs.go::ToolSpecs(); registered in
                        cmd/triagent-mcp/serve.go
  mcp/subagent/         shared sub-agent runner with explicit AllowedTools whitelist + telemetry
                        forwarding
  mcp/citations/        shared citation validator for sub-agent prose tools
  mcp/toolspec/         tool catalog types aggregated in-process by internal/server/meta.go
  auth/                 launcher auth helpers (per-launch token, cookie/bearer split)
internal/
  server/               HTTP layer (browser routes + /api/internal MCP loopback)
  sessions/             investigation lifecycle, events.jsonl, transcripts
  editor/               playbook + wiki editor sessions (polymorphic Subject)
  watches/              signal-watches polling + auto-spawn subsystem
  profile/              runtime profile loader (embedded + on-disk, supports base: merging)
  preflight/            session preflight (namespace reachability, sources sanity)
  k8sx/                 k8s client extensions, hot-swappable via atomic.Pointer
  claude/               Claude CLI process management
  connections/          launcher-side cluster context tracking
  repos/                git-backed vault helpers (wiki, sessions, playbooks)
  web/                  embedded frontend bundle (//go:embed all:dist)
  auto/                 auto-mode (agent-as-operator) orchestration
  wiki/                 wiki frontmatter + entry helpers
frontend/               Next.js app (App Router, route groups under app/(main)/)
                        embedded into internal/web/dist/ at build time
system/                 system playbooks (*.yaml, picked up by embed.go's glob)
docs/
  superpowers/specs/    durable ADRs (referenced from code; never delete)
  superpowers/plans/    scratch plans (deleted once shipped)
  content/, site/       public docs site (Next.js static export)
operator-skills/        skill-style instructions consumed by the operator agent
prompts/                prompt construction (Go) consumed at session start
test-profile/           on-disk profile used by tests
.tool-versions          Go + Node versions
Makefile                build / test / frontend / docs / release targets
.goreleaser.yaml        cross-platform release build (cask + archives)
```

## Entrypoints

| Goal                                         | Start here                                                                   |
| -------------------------------------------- | ---------------------------------------------------------------------------- |
| Run the launcher locally                     | `make build && ./bin/triagent start` (or `go run ./cmd/triagent start`)      |
| Add an MCP server                            | New `pkg/mcp/<name>/` package + one `case` in `cmd/triagent-mcp/serve.go`    |
| Add a system playbook                        | Drop a YAML into `system/`; the embed glob picks it up                       |
| Add a tool to an existing MCP                | `pkg/mcp/<kind>/specs.go::ToolSpecs()` + handler registration in same pkg    |
| Change embedded frontend                     | Edit under `frontend/`; `make frontend` syncs into `internal/web/dist/`      |
| Look up architectural rationale              | `docs/superpowers/specs/*.md` (durable ADRs)                                 |
| Look up Claude-specific operating rules      | `CLAUDE.md`                                                                  |

## Tests and verification

- `make test` → Go race tests + frontend vitest (wholesale). Use this before claiming an implementation done; CI runs the same set.
- `make test-go` → `go test -race -count=1 ./...` only (Go escape hatch; race-clean is non-negotiable).
- `make test-frontend` → frontend vitest only (`cd frontend && npm install && npm test -- --run`).
- `make lint` → `golangci-lint run ./...`.
- `make build` → frontend bundle (`make frontend`) + both Go binaries. Run for any layout / routing change so the
  embedded bundle stays fresh.
- `make frontend-dev` → local Next.js dev server (proxies `/api/*` to `:8080`); pair with `go run . start` in another
  terminal for the UI dev loop.
- Frontend typecheck isn't bundled into `make test` (it's not a test); run `cd frontend && npm run typecheck` whenever
  you've touched `frontend/`. CI gates on it.
- `make release-check` validates `.goreleaser.yaml`; `make release-snapshot-quick` builds the host platform without
  publishing (use during release-config iteration).

## Conventions

Durable conventions and anti-patterns live in [`CLAUDE.md`](CLAUDE.md). The short version:

- One Go module, two binaries, embedded frontend; no separate frontend deploy.
- MCP servers expose `New(Options)` + `Run(ctx)` + sibling `specs.go::ToolSpecs()`; the launcher aggregates each
  MCP's `ToolSpecs()` in-process at startup via `internal/server/meta.go` (`loadMeta` / `toolCatalog`), and the
  per-package `tools_wire_test.go` fails if registration drifts.
- Sub-agent tools route through `pkg/mcp/subagent` with an explicit `AllowedTools` whitelist; never reinvent.
- Single multiplexed SSE per browser tab via `<StreamProvider>`; URL is the source of truth for view state.
- TDD on the Go side; race-clean is non-negotiable.
- Commit shape: `feat(<area>): …`, `refactor(<area>): …`, `fix(<area>): …`, etc., with areas mirroring the module path.
- Specs in `docs/superpowers/specs/` are durable ADRs (referenced from code, never deleted); plans in
  `docs/superpowers/plans/` are scratch and get deleted once the plan ships.
