# Developer Guide

This guide covers environment setup, build/test cycles, codebase layout, and contribution conventions for triagent.

## Prerequisites

- **Go 1.26+** (`go version`)
- **Node 20+** and `npm` (for the frontend bundle)
- **claude CLI** on `$PATH`, authenticated (`claude auth status`)
- **kubectl** on `$PATH`
- **golangci-lint** for the lint target (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)
- Optional: `tsh` (Teleport CLI) for cluster-discovery features

## Repository layout

```
triagent/
├── cmd/
│   ├── triagent/       # launcher binary entrypoint
│   └── triagent-mcp/   # MCP multiplexer entrypoint (serve + dump-meta)
├── internal/           # launcher-private packages
│   ├── auto/           # agent-as-operator subsystem
│   ├── claude/         # claude subprocess management
│   ├── connections/    # credential store (Slack token, incident.io key)
│   ├── editor/         # playbook / wiki editor session management
│   ├── preflight/      # session preflight + per-session MCP config writing
│   ├── profile/        # deployment profile abstraction (inputs, prompt keys)
│   ├── repos/          # per-repo architecture summary cache
│   ├── server/         # HTTP API handlers (browser + internal MCP endpoints)
│   ├── sessions/       # investigation session store + system prompt builder
│   ├── watches/        # signal-watches polling subsystem
│   ├── wiki/           # wiki vault management
│   └── web/            # embedded Next.js dist (//go:embed all:dist)
├── pkg/mcp/            # MCP server implementations (one package per kind)
│   ├── agentoperator/  # triagent-agent-operator
│   ├── citations/      # shared citation runner + validators
│   ├── entities/       # shared entity extraction helpers
│   ├── git/            # triagent-git-<alias>
│   ├── incidentio/     # triagent-incidentio
│   ├── k8s/            # triagent-k8s
│   ├── k8sx/           # triagent-k8sx (extended k8s surface)
│   ├── meta/           # triagent-meta
│   ├── parallel/       # triagent-parallel
│   ├── sessions/       # triagent-sessions
│   ├── signalingest/   # triagent-signal-ingest
│   ├── slack/          # triagent-slack
│   ├── strategies/     # triagent-strategies (playbook walker)
│   ├── subagent/       # shared sub-agent runner
│   ├── telemetry/      # telemetry helpers (tool-id context, parent linking)
│   ├── teleport/       # triagent-teleport
│   ├── toolspec/       # ToolSpec type + reflection helpers
│   └── wiki/           # triagent-wiki
├── prompts/            # system prompt construction
├── system/             # bundled system playbooks (*.yaml, //go:embed)
├── frontend/           # Next.js app (embedded into the Go binary at build time)
├── docs/               # additional documentation
├── Makefile
├── go.mod              # module: github.com/sourcehawk/triagent
└── go.sum
```

## Build

```sh
# Build both binaries into ./bin/
make build

# Build only the launcher
make build-launcher

# Build only the MCP multiplexer
make build-mcp

# Build the frontend bundle into internal/web/dist/ (required before go build if changed)
make frontend
```

`make build` does **not** automatically run `make frontend`. Run `make frontend` first if you've changed the UI and
want the Go binary to embed the updated bundle.

## Test

```sh
# Wholesale: Go race suite + frontend vitest. CI runs the same set.
make test

# Per-language escape hatches:
make test-go          # equivalent: go test -race -count=1 ./...
make test-frontend    # equivalent: cd frontend && npm install && npm test -- --run

# Frontend type-check (not in `make test`; CI runs it separately)
cd frontend && npm run typecheck

# Frontend build (catches bundler errors)
cd frontend && npm run build
```

Run `make test` and `make lint` before claiming an implementation done — CI gates on both. The race detector is
non-negotiable — the launcher fans out across goroutines. If you've touched `frontend/`, also run `npm run typecheck`
since it isn't bundled into `make test`.

## Lint

```sh
make lint
# equivalent: golangci-lint run ./...
```

Config lives in `.golangci.yml`. Fix all lint errors before committing; CI will catch them.

## Local development loop

```sh
# Terminal 1: Go server (embedded dist from the last make frontend run)
go run ./cmd/triagent start

# Terminal 2: Next.js dev server (proxies /api/* to the Go server)
cd frontend && npm run dev
```

`next.config.mjs` proxies `/api/*` to `localhost:8080` in dev. Set `GO_API_PORT=<port>` if your Go server uses a
different port.

## Adding a new MCP server kind

1. Create `pkg/mcp/<kind>/` with:
   - `server.go` — `New(Options) (*Server, error)` + `(*Server).Run(ctx context.Context) error`
   - `specs.go` — `ToolSpecs() []toolspec.ToolSpec` (must stay in sync with handler registrations)
2. Add a `case "<kind>":` in `cmd/triagent-mcp/serve.go`.
3. Add the kind to the `ToolSpecs()` aggregator in `cmd/triagent-mcp/dump-meta.go`.
4. Add a preflight spawn entry in `internal/preflight/mcpconfig.go` (conditional attachment lives there).
5. Run `make test` — the wire test in `cmd/triagent-mcp/` will catch catalog drift.

## Adding a new system playbook

Drop a `<id>.yaml` into `system/`. The `//go:embed *.yaml` directive in `system/embed.go` picks it up automatically
— no manual registration needed. Run the embed test (`go test -run TestEmbed ./system/...`) to verify.

Playbook schema reference: `triagent-mcp playbook_schema` (or the **MCP catalog** view in the running UI).

## Sub-agent tools

Use the shared runner in `pkg/mcp/subagent/`. Always pass an explicit `AllowedTools` whitelist — empty slice is an
error. The runner:

- Strips telemetry env vars from the sub-agent environment.
- Writes an empty MCP config so the sub-agent cannot reach back to parent MCPs.
- Attaches telemetry to the parent tool ID via `telemetry.CurrentToolID(ctx)`.
- Default timeout: 5 minutes (override per-tool for legitimately longer work like `draft_pr`).

## Telemetry env vars

MCP subprocesses receive these env vars from the launcher:

| Var | Purpose |
| --- | ------- |
| `TRIAGENT_MCP_TELEMETRY_URL` | Loopback URL for posting telemetry events back to the launcher |
| `TRIAGENT_MCP_TELEMETRY_TOKEN` | Bearer token for the internal `/api/internal/...` endpoint |
| `TRIAGENT_MCP_TRACE_ID` | Per-session trace ID for correlating events |

Read them via `pkg/mcp/telemetry` helpers; never hard-code the env var names elsewhere.

## Storage paths

| Location | Contents |
| -------- | -------- |
| `~/.config/triagent/` | Launcher state: sessions, playbooks, wiki vault, credentials |
| `$XDG_CACHE_HOME/triagent-mcp/` (default `~/.cache/triagent-mcp/`) | MCP caches: git clones, Slack channel sidecars |

Use the helpers in `internal/sessions` and `pkg/mcp/git` for path construction — don't inline path strings.

## Credential storage

Slack tokens and incident.io API keys are stored in `~/.config/triagent/credentials.json` and validated against the
upstream service before saving. See `internal/connections/` for the store implementation.

## Profile abstraction

Deployment-specific config (default repo lists, namespace derivation, prompt content, investigation input fields)
loads at runtime from a profile (`internal/profile/`). Profiles support a `base:` key for merging. The neutral base
profile ships embedded; a deployment-specific profile is loaded by name or path at startup.

Do **not** bake deployment-specific defaults into Go constants — put them in a profile instead.

## Commit conventions

```
feat(<area>): short description
fix(<area>): short description
refactor(<area>): short description
test(<area>): short description
chore(<area>): short description
```

Areas mirror the package path: `mcp/git`, `mcp/k8s`, `server`, `frontend`, `sessions`, `watches`, etc.

One commit per logical change. Keep the build green between commits — migrations land alongside their callers.

## Hard rules (don't break these)

- **No `--no-verify`** on commits. Fix the hook failure instead.
- **No `git add -A` or `git add .`** — stage files by name to avoid accidentally committing secrets or binaries.
- **TDD is the standard.** Write a failing test, watch it fail for the right reason, then implement.
- **`go test -race -count=1 ./...` must be clean** before every commit.
- **Atomic writes** for any file-system metadata: use the `atomicWrite` helper (tmp-file-then-rename); never write in
  place.
- **Long-running work (push-PR, sub-agent generation) must survive client disconnect.** Derive the work context from
  the manager's parent context, not from `r.Context()`.
