# Open-Source Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the triagent repo from a Camunda-internal multi-plugin port into a self-contained, OSS-ready Go project rooted at `github.com/sourcehawk/triagent`, ending with a single squashed initial commit on the public `main`.

**Architecture:** Single Go module at the repo root. Two binaries: `cmd/triagent/` (launcher) and `cmd/triagent-mcp/` (MCP multiplexer with `--kind=<name>`). MCP impls + shared abstraction at `pkg/mcp/<name>/` and `pkg/mcp/{toolspec,subagent,citations,telemetry}/`. Auth providers at `pkg/auth/{cluster.go, kubeconfig/, teleport/}`. Launcher internals at `internal/`. Embedded Next.js frontend at `frontend/`. Profile YAML at `profiles/<name>/`. External MCPs via `profile.extra_mcps[]` in reference mode (allowlist only; server configured in Claude) or spawn mode (launcher writes spawn config into per-session `.mcp.json`).

**Tech Stack:** Go 1.26.1, Cobra, modelcontextprotocol/go-sdk, k8s.io/client-go, Next.js 25 + React, Vitest, golangci-lint.

**Spec:** `docs/superpowers/specs/2026-05-16-open-source-restructure-design.md`

---

## How this plan is organized

The plan mirrors the 8-commit sequence in section 10 of the spec. Each phase corresponds to one local commit that must leave the build green. Tasks within a phase ladder up to that commit. The final phase ("Finalization") squashes everything to a single initial commit and pushes to the public remote.

**Verification suite** (run at the end of every commit phase unless a task is purely a deletion that has already broken nothing):

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm run typecheck && npm run build && npm test -- --run )
```

Don't run the verification suite mid-phase for mechanical refactors that haven't yet finished rewriting imports — wait until the phase is complete.

**Commit message convention** (from CLAUDE.md): `feat(<area>):`, `refactor(<area>):`, `fix(<area>):`, `test(<area>):`, `chore(<area>):`. Areas mirror the module path (`pkg/mcp`, `internal/server`, `frontend`, etc.). All commits include `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` per the user's preferences for AI-generated work.

**Do not run `--no-verify`** at any point. If a pre-commit hook fails, fix the underlying issue.

---

## Phase 0: Setup

### Task 0.1: Confirm baseline state

**Files:** none (status check only)

- [ ] **Step 1: Verify on `main` with only the two spec commits**

```bash
git -C /home/aegir/Documents/personal/triagent log --oneline
```

Expected: two commits — the open-source restructure design and the module-path pin.

- [ ] **Step 2: Verify untracked files are the camunda port**

```bash
git -C /home/aegir/Documents/personal/triagent status --short
```

Expected output: `??` lines for `CLAUDE.md`, `DEVELOPER_GUIDE.md`, `Makefile`, `README.md`, `catalog.json`, `investigate/`, `k8s/`, `mcp/`, `tools/`, `ui/`, `.github/`, `.gitignore`, `.golangci.yml`, `.prettierrc`, `.tool-versions`.

---

## Phase 1: Baseline

Land the camunda-port-verbatim baseline so the rest of the sequence has a reference point. **No code edits in this phase — pure `git add`.**

### Task 1.1: Stage and commit the camunda baseline

**Files:** every untracked file in the repo (excluding build artefacts the `.gitignore` already excludes).

- [ ] **Step 1: Inspect what will be added**

```bash
cd /home/aegir/Documents/personal/triagent
git status --short
```

Confirm no `??` entry corresponds to a build-only artefact (look for `node_modules/`, `.next/`, `dist/`, `bin/`). If any of those appear as untracked despite `.gitignore`, fix the ignore rule first.

- [ ] **Step 2: Stage everything by name (no `git add .` / `git add -A`)**

```bash
git add CLAUDE.md DEVELOPER_GUIDE.md Makefile README.md catalog.json
git add .github .gitignore .golangci.yml .prettierrc .tool-versions
git add investigate k8s mcp tools ui
```

- [ ] **Step 3: Verify the staged set looks right**

```bash
git status --short
```

Expected: every line begins with `A` (added). Nothing left as `??`.

- [ ] **Step 4: Commit**

```bash
git commit -m "$(cat <<'EOF'
chore: import c1-plugins baseline

Pre-restructure snapshot of the Camunda-internal c1-plugins monorepo.
This commit exists as a reference point; the subsequent restructure
commits get squashed against it before pushing to the public remote.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 5: Verify a clean working tree**

```bash
git status --short
```

Expected: empty output.

---

## Phase 2: Reshape to single module

Collapse the four-module monorepo into a single Go module at the repo root. Drop the obsolete plugins (`/k8s`, `/ui`, `/tools`) and the multi-plugin host scaffolding. The MCP impls (`mcp/<name>/`) stay where they are within this phase — phase 3 promotes them to `pkg/mcp/`.

### Task 2.1: Delete `/k8s`, `/ui`, `/tools`

**Files:**
- Delete: `k8s/`, `ui/`, `tools/`

- [ ] **Step 1: Confirm no Go file outside these dirs imports them**

```bash
grep -rIn 'github.com/camunda/c1-plugins/\(k8s\|ui\|tools\)' investigate/ mcp/ || echo "clean"
```

Expected: `clean`.

- [ ] **Step 2: Delete the directories**

```bash
rm -rf k8s ui tools
```

### Task 2.2: Delete multi-plugin host scaffolding

**Files:**
- Delete: `catalog.json`, root `Makefile`, `.github/scripts/sync-catalog.sh`, `DEVELOPER_GUIDE.md`, every `plugin.json`

- [ ] **Step 1: Delete the scaffolding files**

```bash
rm -f catalog.json Makefile DEVELOPER_GUIDE.md
rm -f .github/scripts/sync-catalog.sh
find . -name plugin.json -not -path './node_modules/*' -delete
```

- [ ] **Step 2: Drop the pre-commit hook that enforces plugin.json version bumps**

```bash
rm -rf .github/hooks
```

(Hook contents become moot once `plugin.json` files don't exist.)

- [ ] **Step 3: Inspect `.github/workflows/` and delete any workflow that runs `make catalog` or per-plugin version checks**

```bash
ls .github/workflows/
```

Read each one; delete or edit so no reference to `sync-catalog.sh`, `make catalog`, or `plugin.json` remains. Keep workflows that run `go test`, `go build`, `golangci-lint`, or `npm` — they need their paths updated in a later task.

### Task 2.3: Raise `investigate/` contents to the repo root

**Files:**
- Move: `investigate/main.go` → `cmd/triagent/main.go`
- Move: `investigate/cmd/*.go` → `cmd/triagent/`
- Move: `investigate/internal/` → `internal/`
- Move: `investigate/system/`, `prompts/`, `operator-skills/`, `frontend/` → root `system/`, `prompts/`, `operator-skills/`, `frontend/`
- Move: `investigate/README.md` → root `README.md` (overwriting nothing — root `README.md` was already deleted by 2.2; if not, replace it)
- Move: `investigate/Makefile` → root `Makefile`

- [ ] **Step 1: Make the new `cmd/triagent/` skeleton**

```bash
mkdir -p cmd/triagent
git mv investigate/main.go cmd/triagent/main.go
git mv investigate/cmd/*.go cmd/triagent/
rmdir investigate/cmd
```

- [ ] **Step 2: Move the launcher internals up one level**

```bash
git mv investigate/internal internal
git mv investigate/system system
git mv investigate/prompts prompts
git mv investigate/operator-skills operator-skills
git mv investigate/frontend frontend
git mv investigate/README.md README.md
git mv investigate/Makefile Makefile
```

- [ ] **Step 3: Remove the now-empty `investigate/`**

```bash
rm -f investigate/go.mod investigate/go.sum
rmdir investigate
```

### Task 2.4: Raise `mcp/` contents to support `cmd/triagent-mcp/`

**Files:**
- Move: `mcp/main.go` → `cmd/triagent-mcp/main.go`
- Move: `mcp/cmd/*.go` → `cmd/triagent-mcp/`
- Move: `mcp/` → root `mcp/` (i.e. keep the path but drop the outer `mcp/`'s status as a Go module by deleting `mcp/go.mod`; `mcp/` is now a regular subtree of the single module). Phase 3 moves it to `pkg/mcp/`.

- [ ] **Step 1: Build the multiplexer binary location**

```bash
mkdir -p cmd/triagent-mcp
git mv mcp/main.go cmd/triagent-mcp/main.go
git mv mcp/cmd/*.go cmd/triagent-mcp/
rmdir mcp/cmd
```

- [ ] **Step 2: Drop the per-module Go files at `mcp/`'s old root**

```bash
rm -f mcp/go.mod mcp/go.sum
```

- [ ] **Step 3: Verify `mcp/<name>/` is still intact**

```bash
ls mcp/
```

Expected: `agentoperator`, `citations`, `entities`, `git`, `incidentio`, `k8s`, `k8sx`, `meta`, `parallel`, `prom`, `sessions`, `signalingest`, `slack`, `strategies`, `subagent`, `telemetry`, `teleport`, `toolspec`, `wiki`. (These will move to `pkg/mcp/` in phase 3.)

### Task 2.5: Write the new single root `go.mod`

**Files:**
- Create: `go.mod`, `go.sum` (regenerated)

- [ ] **Step 1: Author a fresh `go.mod`**

```bash
cat > go.mod <<'EOF'
module github.com/sourcehawk/triagent

go 1.26.1
EOF
```

- [ ] **Step 2: Pull required deps from the two old go.sums**

The union of `investigate/go.mod` and `mcp/go.mod` direct requires (look back at the original files in git history of the baseline commit if needed):

```
github.com/camunda/c1-sdk v0.4.1                  // removed in phase 4
github.com/charmbracelet/log v1.0.0
github.com/spf13/cobra v1.10.2
github.com/stretchr/testify v1.11.1
gopkg.in/yaml.v3 v3.0.1
k8s.io/api v0.35.3
k8s.io/apimachinery v0.35.3
k8s.io/client-go v0.35.3
github.com/modelcontextprotocol/go-sdk v1.2.0
github.com/prometheus/client_golang v1.23.2
github.com/prometheus/common v0.67.5
sigs.k8s.io/yaml v1.6.0
```

Add them via:

```bash
go get github.com/camunda/c1-sdk@v0.4.1
go get github.com/charmbracelet/log@v1.0.0
go get github.com/spf13/cobra@v1.10.2
go get github.com/stretchr/testify@v1.11.1
go get gopkg.in/yaml.v3@v3.0.1
go get k8s.io/api@v0.35.3
go get k8s.io/apimachinery@v0.35.3
go get k8s.io/client-go@v0.35.3
go get github.com/modelcontextprotocol/go-sdk@v1.2.0
go get github.com/prometheus/client_golang@v1.23.2
go get github.com/prometheus/common@v0.67.5
go get sigs.k8s.io/yaml@v1.6.0
```

### Task 2.6: Rewrite import paths

**Files:** every `.go` file in the repo.

- [ ] **Step 1: Sed-rewrite the module paths**

```bash
# investigate/internal/<x> → internal/<x>
find . -name '*.go' -not -path './frontend/*' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/camunda/c1-plugins/investigate/internal/|github.com/sourcehawk/triagent/internal/|g' {} +

# investigate/cmd → cmd/triagent (only used by main.go ref)
find . -name '*.go' -not -path './frontend/*' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/camunda/c1-plugins/investigate/cmd|github.com/sourcehawk/triagent/cmd/triagent|g' {} +

# mcp/<x> → mcp/<x> within the new module (path unchanged for now; phase 3 moves to pkg/mcp/<x>)
find . -name '*.go' -not -path './frontend/*' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/camunda/c1-plugins/mcp/|github.com/sourcehawk/triagent/mcp/|g' {} +

# mcp/cmd → cmd/triagent-mcp
find . -name '*.go' -not -path './frontend/*' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/camunda/c1-plugins/mcp/cmd|github.com/sourcehawk/triagent/cmd/triagent-mcp|g' {} +

# Catch-all: anything still pointing at camunda/c1-plugins is a bug — find them
grep -rIln 'github.com/camunda/c1-plugins' . --include='*.go' | head -20
```

The grep should print nothing. Any hits are import paths the rewrite missed; fix manually.

- [ ] **Step 2: Run `go mod tidy` to fill `go.sum` and prune unused deps**

```bash
go mod tidy
```

(Indirects fill in. The `c1-sdk` dep remains direct until phase 4.)

### Task 2.7: Write the new top-level Makefile

**Files:**
- Create: `Makefile` (already deleted in 2.2; this is the new root single-module version)

- [ ] **Step 1: Author the new Makefile**

```makefile
.PHONY: build build-launcher build-mcp test lint fmt frontend frontend-dev clean

# Build both binaries into ./bin/.
build: build-launcher build-mcp

build-launcher:
	go build -o bin/triagent ./cmd/triagent

build-mcp:
	go build -o bin/triagent-mcp ./cmd/triagent-mcp

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

# Build the embedded frontend bundle into internal/web/dist (consumed by //go:embed).
frontend:
	cd frontend && npm install && npm run build

# Local frontend dev server (no embedding).
frontend-dev:
	cd frontend && npm run dev

clean:
	rm -rf bin internal/web/dist/* internal/web/dist/.gitkeep
	mkdir -p internal/web/dist
	touch internal/web/dist/.gitkeep
```

The Makefile preserves the `internal/web/dist/.gitkeep` anchor per CLAUDE.md.

### Task 2.8: Update CI workflows

**Files:**
- Modify: `.github/workflows/*.yml`

- [ ] **Step 1: List remaining workflows**

```bash
ls .github/workflows/
```

- [ ] **Step 2: Rewrite paths in each workflow**

Walk every `.yml` file. Replace any `cd investigate` / `cd mcp` patterns; multi-module `for mod in ...` loops; references to deleted plugins. The single-module CI looks like:

```yaml
- name: Go build
  run: go build ./...
- name: Go test
  run: go test -race -count=1 ./...
- name: Go lint
  run: golangci-lint run ./...
- name: Frontend
  run: |
    cd frontend
    npm install
    npm run typecheck
    npm run build
    npm test -- --run
```

### Task 2.9: Update `.gitignore`

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Inspect current ignores**

```bash
cat .gitignore
```

- [ ] **Step 2: Confirm the `internal/web/dist/` block still works**

The original `.gitignore` (per CLAUDE.md) has a negation rule keeping `internal/web/dist/.gitkeep` tracked. After 2.3 moved `investigate/internal/web/dist/.gitkeep` to `internal/web/dist/.gitkeep`, the rule's path may need updating. Verify with:

```bash
git check-ignore -v internal/web/dist/.gitkeep
```

Expected: NOT ignored (the negation should apply). If it's ignored, edit `.gitignore` so the rule reads:

```
internal/web/dist/*
!internal/web/dist/.gitkeep
```

### Task 2.10: Verify the build and commit the reshape

**Files:** verification only.

- [ ] **Step 1: Build**

```bash
go build ./...
```

Expected: clean build. If a missing import path surfaces, fix and re-run.

- [ ] **Step 2: Test**

```bash
go test -race -count=1 ./...
```

Expected: green. The Go tests may include calls to `c1-sdk/plugin` and `c1-sdk/teleport` — those still work, the dep hasn't been removed yet.

- [ ] **Step 3: Lint**

```bash
golangci-lint run ./...
```

Expected: green.

- [ ] **Step 4: Frontend check**

```bash
cd frontend && npm install && npm run typecheck && npm run build && npm test -- --run
cd ..
```

Expected: green. The frontend hasn't been touched, only its location.

- [ ] **Step 5: Stage and commit**

```bash
git add -A   # NOTE: per CLAUDE.md, prefer named adds. But for a mass move this scale, -A is justifiable; verify with status first.
git status --short | head -40
```

If status shows any unexpected entries (build artefacts), back them out and use named adds for the rest.

```bash
git commit -m "$(cat <<'EOF'
refactor: reshape to single Go module rooted at github.com/sourcehawk/triagent

Collapses the four-module c1-plugins monorepo into one Go module. Drops
/k8s, /ui, /tools and the multi-plugin host scaffolding (catalog.json,
sync-catalog.sh, per-plugin plugin.json, version-bump hooks). Folds
investigate/ contents to the repo root; introduces cmd/triagent and
cmd/triagent-mcp for the two binaries; preserves mcp/<name>/
as a transitional location for the MCP impls (phase 3 promotes them to
pkg/mcp/).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3: Lift MCP abstraction to `pkg/mcp/`

Move every MCP impl from `mcp/<name>/` to `pkg/mcp/<name>/` and promote the shared cross-cutting packages. Define the `pkg/mcp/Server` interface. Delete the `dump-meta` subcommand.

### Task 3.1: Move shared cross-cutting packages

**Files:**
- Move: `mcp/toolspec/` → `pkg/mcp/toolspec/`
- Move: `mcp/subagent/` → `pkg/mcp/subagent/`
- Move: `mcp/citations/` → `pkg/mcp/citations/`
- Move: `mcp/telemetry/` → `pkg/mcp/telemetry/`
- Move: `mcp/entities/` → `pkg/mcp/entities/`
- Move: `mcp/k8sx/` → `pkg/mcp/k8sx/` (k8s-related shared bits used by k8s + git MCPs)

- [ ] **Step 1: Create `pkg/mcp/` and move the shared packages**

```bash
mkdir -p pkg/mcp
git mv mcp/toolspec pkg/mcp/toolspec
git mv mcp/subagent pkg/mcp/subagent
git mv mcp/citations pkg/mcp/citations
git mv mcp/telemetry pkg/mcp/telemetry
git mv mcp/entities pkg/mcp/entities
git mv mcp/k8sx pkg/mcp/k8sx
```

### Task 3.2: Move each MCP impl package

**Files:**
- Move: `mcp/<name>/` → `pkg/mcp/<name>/` for each of: `agentoperator`, `git`, `incidentio`, `k8s`, `meta`, `parallel`, `prom`, `sessions`, `signalingest`, `slack`, `strategies`, `teleport`, `wiki`.

- [ ] **Step 1: Move all impls**

```bash
for pkg in agentoperator git incidentio k8s meta parallel prom sessions signalingest slack strategies teleport wiki; do
  git mv mcp/$pkg pkg/mcp/$pkg
done
```

- [ ] **Step 2: Remove the now-empty `mcp/` dir**

```bash
rmdir mcp
```

### Task 3.3: Rewrite import paths for the moved packages

**Files:** every `.go` file in the repo.

- [ ] **Step 1: Sed-rewrite imports**

```bash
find . -name '*.go' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/sourcehawk/triagent/mcp/|github.com/sourcehawk/triagent/pkg/mcp/|g' {} +
```

- [ ] **Step 2: Verify no stragglers**

```bash
grep -rIln '"github\.com/sourcehawk/triagent/mcp/' --include='*.go' . | head
```

Expected: empty. (Any remaining `triagent/mcp/<x>` import — note: NOT `pkg/mcp/<x>` — is a sed miss.)

### Task 3.4: Define `pkg/mcp/Server` interface

**Files:**
- Create: `pkg/mcp/server.go`

- [ ] **Step 1: Write the interface**

```go
// Package mcp provides the shared abstractions for MCP servers in
// triagent. Each MCP implementation lives in a sibling subpackage
// (pkg/mcp/k8s, pkg/mcp/git, etc.) and exposes a constructor returning
// a value that satisfies Server. The triagent-mcp multiplexer
// (cmd/triagent-mcp/) selects one implementation by --kind=<name>.
package mcp

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/mcp/toolspec"
)

// Server is the contract every MCP implementation satisfies.
// Run blocks until ctx is cancelled or the stdio peer disconnects.
type Server interface {
	Run(ctx context.Context) error
	ToolSpecs() []toolspec.ToolSpec
}
```

- [ ] **Step 2: Optionally have each MCP's `*Server` type adopt the interface**

For now, this is documentation only — each MCP's existing `Run` and `ToolSpecs` methods already satisfy the shape. A future task can change `cmd/triagent-mcp/serve.go` to dispatch via a `map[string]func(Options) (mcp.Server, error)` factory. Not needed in v1.

### Task 3.5: Delete the `dump-meta` subcommand

**Files:**
- Delete: `cmd/triagent-mcp/dump-meta.go` (was `mcp/cmd/dump-meta.go`)
- Modify: `cmd/triagent-mcp/commands.go` to drop the registration

- [ ] **Step 1: Inspect the file**

```bash
cat cmd/triagent-mcp/dump-meta.go
```

- [ ] **Step 2: Delete and unregister**

```bash
rm cmd/triagent-mcp/dump-meta.go
```

Edit `cmd/triagent-mcp/commands.go` and remove the `cmd.AddCommand(dumpMetaCommand())` line (or however it's registered).

### Task 3.6: Verify and commit

- [ ] **Step 1: Run the full verification suite**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm run typecheck && npm run build && npm test -- --run )
```

Expected: all green. The `tools_wire_test.go` in each `pkg/mcp/<name>/` package validates that registered tools match `ToolSpecs()` — these are the load-bearing tests for this phase.

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(pkg/mcp): lift MCP packages to public pkg/mcp/

Promotes every MCP implementation (mcp/<name>/) and the shared
cross-cutting packages (toolspec, subagent, citations, telemetry,
entities, k8sx) to pkg/mcp/. Adds pkg/mcp/server.go defining the
Server interface that every MCP package satisfies. Deletes the
dump-meta subcommand (was a c1 ecosystem catalog feed; no OSS consumer).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4: Drop `c1-sdk`

Replace `c1-sdk/plugin.Run` with plain Cobra. Copy-vendor the slice of `c1-sdk/teleport/{auth,k8s}` we use into `pkg/auth/teleport/`. Move the cluster provider interface from `internal/cluster/` to `pkg/auth/`. Remove the `c1-sdk` dependency from `go.mod`.

### Task 4.1: Move cluster provider interface to `pkg/auth/`

**Files:**
- Move: `internal/cluster/provider.go` → `pkg/auth/cluster.go`
- Move: `internal/cluster/teleport/teleport.go` → `pkg/auth/teleport/teleport.go`

- [ ] **Step 1: Restructure the cluster package**

```bash
mkdir -p pkg/auth/teleport
git mv internal/cluster/provider.go pkg/auth/cluster.go
git mv internal/cluster/teleport/teleport.go pkg/auth/teleport/teleport.go
rmdir internal/cluster/teleport internal/cluster
```

- [ ] **Step 2: Rename the package in `pkg/auth/cluster.go`**

Change the package declaration from `package cluster` to `package auth`. The file currently exposes types like `Cluster`, `Provider`, `LoginResult`, `ErrAuthExpired`, `ReauthAdvisor`, `Authenticator` — all stay as-is at the new package path.

- [ ] **Step 3: Rewrite imports**

```bash
find . -name '*.go' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/sourcehawk/triagent/internal/cluster/teleport|github.com/sourcehawk/triagent/pkg/auth/teleport|g' {} +

find . -name '*.go' -not -path './node_modules/*' \
  -exec sed -i 's|github.com/sourcehawk/triagent/internal/cluster|github.com/sourcehawk/triagent/pkg/auth|g' {} +

# Type references: cluster.Provider → auth.Provider, cluster.Cluster → auth.Cluster, etc.
# Scope this to .go files outside pkg/auth itself.
find . -name '*.go' -not -path './node_modules/*' -not -path './pkg/auth/*' \
  -exec sed -i 's|cluster\.\(Provider\|Cluster\|LoginResult\|ErrAuthExpired\|ReauthAdvisor\|Authenticator\)|auth.\1|g' {} +
```

- [ ] **Step 4: Verify**

```bash
go build ./...
```

Expected: clean build. If a test file used `cluster.X`, the sed above caught it.

### Task 4.2: Copy-vendor `c1-sdk/teleport/{auth,k8s}` into `pkg/auth/teleport/`

**Files:**
- Create: `pkg/auth/teleport/internal/auth.go` (vendored from `c1-sdk/teleport/auth/`)
- Create: `pkg/auth/teleport/internal/k8s.go` (vendored from `c1-sdk/teleport/k8s/`)
- Modify: `pkg/auth/teleport/teleport.go` to point at the local vendored package

The c1-sdk source is at `$(go env GOPATH)/pkg/mod/github.com/camunda/c1-sdk@v0.4.1/`. Inspect:

```bash
ls $(go env GOPATH)/pkg/mod/github.com/camunda/c1-sdk@v0.4.1/teleport/
```

Expected: `auth/`, `k8s/`. Copy only what `pkg/auth/teleport/teleport.go` references — likely:

- `auth.DefaultProxyAddr`, `auth.DefaultAuthConnector` (string consts)
- A login helper that drives the `tsh` browser SSO flow
- `k8s.NewProvider`, `k8s.Provider.ListClusters`, `Login`, `IsAuthenticated`

- [ ] **Step 1: Catalog the surface used today**

```bash
grep -h 'c1-sdk/teleport' pkg/auth/teleport/teleport.go cmd/triagent/main.go pkg/mcp/teleport/*.go 2>/dev/null | sort -u
```

- [ ] **Step 2: Create a small vendored package**

```bash
mkdir -p pkg/auth/teleport/internal/sdk
```

Copy the source files (preserving copyright headers) from `c1-sdk@v0.4.1/teleport/auth/*.go` and `c1-sdk@v0.4.1/teleport/k8s/*.go` into `pkg/auth/teleport/internal/sdk/`. Collapse the two upstream packages into one local package (rename to `package sdk`) — fewer files; if the upstream split is meaningful, preserve it as two subdirs.

- [ ] **Step 3: Rewrite consumer imports**

In `pkg/auth/teleport/teleport.go`, replace:

```go
import (
    "github.com/camunda/c1-sdk/teleport/auth"
    teleportk8s "github.com/camunda/c1-sdk/teleport/k8s"
)
```

With:

```go
import (
    sdk "github.com/sourcehawk/triagent/pkg/auth/teleport/internal/sdk"
)
```

And replace `auth.DefaultProxyAddr` → `sdk.DefaultProxyAddr`, `teleportk8s.NewProvider()` → `sdk.NewProvider()`, etc.

Same rewrite in `pkg/mcp/teleport/*.go` (the MCP-side teleport server) and any test files.

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: clean build. Fix any missed type aliases until clean.

### Task 4.3: Replace `c1-sdk/plugin.Run` with plain Cobra

**Files:**
- Modify: `cmd/triagent/main.go`
- Modify: `cmd/triagent-mcp/main.go`

- [ ] **Step 1: Rewrite `cmd/triagent/main.go`**

The current main does:

```go
plugin.Run(meta, &investigatePlugin{})
```

Where `meta` is `//go:embed plugin.json`. Plugin.json files no longer exist (phase 2 deleted them). Replace with direct Cobra root:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sourcehawk/triagent/cmd/triagent/internal/cmd"  // see below — package move
	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/pkg/auth/teleport"
)

const version = "0.1.0"

func main() {
	// Intercept `triagent clear watches [flags...]` before cobra dispatch
	// so the subcommand can parse its own flag set.
	if len(os.Args) >= 3 && os.Args[1] == "clear" && os.Args[2] == "watches" {
		os.Exit(cmd.ClearWatches(os.Args[3:]))
	}

	// A bare `triagent` defaults to `triagent start`.
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "start")
	}

	// Provider wiring stays hard-coded to teleport in this phase.
	// Phase 7 makes it profile-driven and adds the kubeconfig provider.
	provider := teleport.NewProvider(teleport.Config{})
	cmd.SetProvider(provider)

	root := &cobra.Command{
		Use:     "triagent",
		Short:   "AI-assisted investigation agent.",
		Version: version,
	}
	for _, c := range cmd.Commands() {
		root.AddCommand(c)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

(The `cmd` package import above — `cmd/triagent/internal/cmd` — is wrong as written; the existing `cmd/triagent/commands.go`, `clean.go`, `clear_watches.go`, `start.go` files were moved here in phase 2. They're already in package `main`'s sibling directory at `cmd/triagent/`. Use `package cmd` for those files and an `internal/` indirection, OR keep them in `package main` and inline the subcommand registration. The simpler path: keep them in `package main` in `cmd/triagent/`; reference the `Commands()`, `ClearWatches()`, `SetProvider()` exported names directly.)

Choose: inline (no internal package), and let `cmd/triagent/main.go` reach the other files directly. Update them to all share `package main`.

- [ ] **Step 2: Rewrite `cmd/triagent-mcp/main.go`**

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const version = "0.1.0"

func main() {
	root := &cobra.Command{
		Use:     "triagent-mcp",
		Short:   "MCP server multiplexer for triagent.",
		Version: version,
	}
	for _, c := range Commands() {
		root.AddCommand(c)
	}
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

The `Commands()` function comes from `commands.go` (formerly `mcp/cmd/commands.go`), now in the same `package main`.

- [ ] **Step 3: Delete `//go:embed plugin.json` lines from both `main.go` files**

The `var meta []byte` blocks become dead. Remove them. Remove the `_ "embed"` import if no longer needed.

### Task 4.4: Remove `c1-sdk` from `go.mod`

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Drop the direct require**

```bash
go mod edit -droprequire=github.com/camunda/c1-sdk
go mod tidy
```

- [ ] **Step 2: Confirm zero `camunda` deps remain**

```bash
grep -E 'github\.com/camunda' go.mod go.sum
```

Expected: empty. (Note: if any indirect dep happens to share the prefix, leave it.)

### Task 4.5: Verify and commit

- [ ] **Step 1: Run the verification suite**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm run typecheck && npm run build && npm test -- --run )
```

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(cmd,pkg/auth): drop c1-sdk dependency

Replaces c1-sdk/plugin.Run with plain Cobra Execute in both binaries.
Vendors the used slice of c1-sdk/teleport/{auth,k8s} into
pkg/auth/teleport/internal/sdk/. Moves the cluster Provider interface
to pkg/auth/. Removes c1-sdk from go.mod; zero github.com/camunda/*
dependencies remain.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5: Evict the prom MCP

Delete `pkg/mcp/prom/`, the launcher's prom port-forward goroutine, and the prom control endpoint. Remove all prom-specific consts from `internal/preflight/mcpconfig.go`. Drop the prom entry from the allowed-tools builders.

### Task 5.1: Delete the `pkg/mcp/prom/` package

**Files:**
- Delete: `pkg/mcp/prom/`

- [ ] **Step 1: Confirm only the launcher imports the prom package**

```bash
grep -rIln 'pkg/mcp/prom' --include='*.go' . | head
```

Expected hits: `cmd/triagent-mcp/serve.go` (or `commands.go`) and possibly some tests in `internal/preflight/`.

- [ ] **Step 2: Remove the multiplexer case**

In `cmd/triagent-mcp/serve.go`, find the `case "prom":` block and delete it. Drop the `prom` import.

- [ ] **Step 3: Delete the directory**

```bash
git rm -r pkg/mcp/prom
```

### Task 5.2: Delete the launcher's prom port-forward

**Files:**
- Modify or delete: `internal/portforward/` (whatever subset handles prom — read the file before deleting)

- [ ] **Step 1: Inspect portforward**

```bash
ls internal/portforward/
grep -rIln 'prom' internal/portforward/
```

If the package is entirely prom-specific, delete it: `git rm -r internal/portforward/`. If it has non-prom uses, surgical edit instead.

### Task 5.3: Delete the prom control endpoint

**Files:**
- Delete: `internal/server/handlers_prom_internal.go`, `internal/server/handlers_prom_internal_test.go`

- [ ] **Step 1: Confirm the handler is unused elsewhere**

```bash
grep -rIln 'handlers_prom_internal\|PromInternal\|prom_internal' internal/ | head
```

- [ ] **Step 2: Delete**

```bash
git rm internal/server/handlers_prom_internal.go internal/server/handlers_prom_internal_test.go
```

- [ ] **Step 3: Find handler registrations referring to prom routes**

```bash
grep -rIn 'api/internal/prom\|prom/control\|prom_internal' internal/server/ | head
```

Remove any matching handler registrations in `internal/server/handlers.go` or wherever routes are wired.

### Task 5.4: Remove prom-specific consts from `internal/preflight/mcpconfig.go`

**Files:**
- Modify: `internal/preflight/mcpconfig.go`
- Modify: `internal/preflight/mcpconfig_test.go`
- Modify: `internal/preflight/preflight.go` (if it references `PromControlPort`, prom URL, etc.)

- [ ] **Step 1: List consts to remove**

```bash
grep -n 'Prom' internal/preflight/mcpconfig.go
```

Targets: `MCPAliasProm`, `EnvPromURL`, `EnvPromToken`, `EnvPromControlPort`.

- [ ] **Step 2: Remove the consts and the server entry they back**

Edit `internal/preflight/mcpconfig.go`. Find the block that emits the `c1-prom` server entry in `writeMCPConfig` and delete it. Remove the consts. Remove `PromURL`, `PromToken`, `PromControlPort` from the `mcpConfigInputs` struct.

- [ ] **Step 3: Cascade to `preflight.go`**

Edit `internal/preflight/preflight.go`. Find any code that allocates the prom control port, port-forwards prom, or sets the prom URL on `mcpConfigInputs`. Delete those lines and any unused locals they leave behind.

- [ ] **Step 4: Update tests**

`internal/preflight/mcpconfig_test.go` likely has cases asserting the prom server is registered. Delete those. Don't leave stale fixture data referencing `c1-prom`.

### Task 5.5: Drop prom from allowed-tools builders

**Files:**
- Modify: `internal/sessions/session.go` (function `allowedTools`)
- Modify: `internal/editor/editor.go` (the equivalent builder ~line 350)

- [ ] **Step 1: Edit `internal/sessions/session.go`**

Find the line `"mcp__" + preflight.MCPAliasProm + "__*",` in `allowedTools` and delete it.

- [ ] **Step 2: Edit `internal/editor/editor.go`**

Same delete in the editor's allowed-tools function.

- [ ] **Step 3: Update tests**

`internal/sessions/session_test.go` and any editor tests asserting the prom glob is present need their expectation updated.

### Task 5.6: Verify and commit

- [ ] **Step 1: Run the verification suite**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm run typecheck && npm run build && npm test -- --run )
```

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor: evict the prom MCP and its launcher coupling

Removes pkg/mcp/prom/ (~654 LOC of Camunda-coded PromQL), the
launcher's prom port-forward goroutine, the prom control endpoint,
and every prom-specific const/env-var in internal/preflight/mcpconfig.
Drops the prom entry from the allowed-tools builders in
internal/sessions and internal/editor. The Camunda prom MCP will be
re-homed in c1-plugins and consumed via extra_mcps spawn mode (phase 6).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 6: External-MCP plug-in surface

Extend `profile.ExtraMCP` to support spawn mode alongside the existing reference mode. Walk both modes in `mcpconfig.go::writeMCPConfig` and the allowed-tools builders. Add `${env:NAME}` interpolation. Delete `profiles/camunda/` in this phase (its `extra_mcps[]` uses the old `name:` field).

### Task 6.1: Write failing tests for the schema rename

**Files:**
- Modify: `internal/profile/profile_test.go`

- [ ] **Step 1: Edit the existing test fixture to use the new schema**

The current `internal/profile/profile_test.go::TestParse` fixture has:

```yaml
extra_mcps:
  - name: org-docs
    description: docs MCP
```

Change to:

```yaml
extra_mcps:
  - alias: org-docs
    description: docs MCP
  - alias: prom-spawn
    description: spawn-mode example
    command: triagent-mcp
    args: ["serve", "--kind=prom"]
    env:
      FOO_URL: "${env:FOO_URL}"
    allowed_tools: ["mcp__prom-spawn__cpu_pressure"]
```

Update the assertion to check both entries:

```go
if len(p.ExtraMCPs) != 2 {
    t.Fatalf("expected 2 ExtraMCPs, got %d", len(p.ExtraMCPs))
}
if p.ExtraMCPs[0].Alias != "org-docs" {
    t.Errorf("reference mode alias wrong: %+v", p.ExtraMCPs[0])
}
if p.ExtraMCPs[1].Alias != "prom-spawn" || p.ExtraMCPs[1].Command != "triagent-mcp" {
    t.Errorf("spawn mode parsing wrong: %+v", p.ExtraMCPs[1])
}
if len(p.ExtraMCPs[1].Args) != 2 || p.ExtraMCPs[1].Args[1] != "--kind=prom" {
    t.Errorf("spawn mode args wrong: %+v", p.ExtraMCPs[1])
}
if p.ExtraMCPs[1].Env["FOO_URL"] != "${env:FOO_URL}" {
    t.Errorf("spawn mode env wrong: %+v", p.ExtraMCPs[1].Env)
}
if len(p.ExtraMCPs[1].AllowedTools) != 1 {
    t.Errorf("allowed_tools wrong: %+v", p.ExtraMCPs[1].AllowedTools)
}
```

- [ ] **Step 2: Run the test; confirm it fails for the right reason**

```bash
go test -run TestParse ./internal/profile -v
```

Expected: compile error (`Alias`, `Command`, `Args`, `Env`, `AllowedTools` not defined) or runtime parse error.

### Task 6.2: Extend `profile.ExtraMCP` struct

**Files:**
- Modify: `internal/profile/profile.go`

- [ ] **Step 1: Change the struct**

```go
type ExtraMCP struct {
	Alias        string            `yaml:"alias"`
	Description  string            `yaml:"description"`
	Command      string            `yaml:"command,omitempty"`
	Args         []string          `yaml:"args,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	AllowedTools []string          `yaml:"allowed_tools,omitempty"`
}
```

- [ ] **Step 2: Run the parse test; confirm it now passes**

```bash
go test -run TestParse ./internal/profile -v
```

Expected: PASS.

### Task 6.3: Update consumers that read `ExtraMCP.Name`

**Files:** every site referencing `m.Name` on an `ExtraMCP`.

- [ ] **Step 1: Find consumers**

```bash
grep -rIn 'ExtraMCPs\[' --include='*.go' . | head
grep -rIn '\.Name' internal/sessions/session.go internal/editor/editor.go internal/server/handlers_profile*.go 2>/dev/null
```

- [ ] **Step 2: Rewrite `.Name` → `.Alias`**

In `internal/sessions/session.go::allowedTools`:

```go
for _, m := range prof.ExtraMCPs {
    if len(m.AllowedTools) > 0 {
        out = append(out, m.AllowedTools...)
    } else {
        out = append(out, "mcp__"+m.Alias+"__*")
    }
}
```

Same pattern in `internal/editor/editor.go`. Update any test fixture that constructs `profile.ExtraMCP{Name: ...}` to use `{Alias: ...}`.

- [ ] **Step 3: Run the test that asserts ExtraMCPs in allowed-tools**

```bash
go test -run TestAllowedTools_IncludesExtraMCPs ./internal/sessions -v
```

Update the test's fixture to use `Alias` if it doesn't already. Confirm it passes.

### Task 6.4: Write the failing test for `mcpconfig.go` spawn-mode passthrough

**Files:**
- Modify: `internal/preflight/mcpconfig_test.go`

- [ ] **Step 1: Add the test**

```go
func TestWriteMCPConfig_ExtraMCPs_SpawnMode_EmitsServerEntry(t *testing.T) {
    t.Setenv("FOO_URL", "http://localhost:9090")
    prof := &profile.Profile{
        ExtraMCPs: []profile.ExtraMCP{
            {Alias: "ref-mode", Description: "claude-configured"},
            {
                Alias: "prom-spawn",
                Description: "spawn-mode prom",
                Command: "triagent-mcp",
                Args: []string{"serve", "--kind=prom"},
                Env: map[string]string{"FOO_URL": "${env:FOO_URL}"},
            },
        },
    }
    path, err := writeMCPConfig(mcpConfigInputs{
        Dir:     t.TempDir(),
        MCPBin:  "/bin/triagent-mcp",
        Profile: prof,
    })
    require.NoError(t, err)
    servers := readMCPConfig(t, path)
    assert.NotContains(t, servers, "ref-mode",
        "reference-mode entry should NOT spawn a server")
    require.Contains(t, servers, "prom-spawn",
        "spawn-mode entry should appear as a server")
    spawn := servers["prom-spawn"]
    assert.Equal(t, "triagent-mcp", spawn["command"])
    args := spawn["args"].([]any)
    assert.Equal(t, []any{"serve", "--kind=prom"}, args)
    env := spawn["env"].(map[string]any)
    assert.Equal(t, "http://localhost:9090", env["FOO_URL"],
        "${env:FOO_URL} should be interpolated at write time")
}

func TestWriteMCPConfig_ExtraMCPs_MissingEnv_Errors(t *testing.T) {
    prof := &profile.Profile{
        ExtraMCPs: []profile.ExtraMCP{{
            Alias: "x",
            Command: "x",
            Env: map[string]string{"VAR": "${env:UNDEFINED_TRIAGENT_VAR}"},
        }},
    }
    _, err := writeMCPConfig(mcpConfigInputs{
        Dir:     t.TempDir(),
        MCPBin:  "/bin/triagent-mcp",
        Profile: prof,
    })
    require.Error(t, err)
    assert.Contains(t, err.Error(), "UNDEFINED_TRIAGENT_VAR")
}
```

- [ ] **Step 2: Run and confirm both tests fail**

```bash
go test -run TestWriteMCPConfig_ExtraMCPs ./internal/preflight -v
```

Expected: FAIL — `prom-spawn` not present in output.

### Task 6.5: Implement spawn-mode passthrough in `writeMCPConfig`

**Files:**
- Modify: `internal/preflight/mcpconfig.go`

- [ ] **Step 1: Add the helper for `${env:NAME}` interpolation**

Add to `mcpconfig.go`:

```go
import "regexp"

var envRefRe = regexp.MustCompile(`^\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}$`)

// interpolateEnv replaces a value of the form ${env:NAME} with the value
// of NAME from the process env. Returns an error if NAME is unset.
// Non-matching values pass through verbatim.
func interpolateEnv(alias, key, value string) (string, error) {
    m := envRefRe.FindStringSubmatch(value)
    if m == nil {
        return value, nil
    }
    v, ok := os.LookupEnv(m[1])
    if !ok {
        return "", fmt.Errorf("extra_mcp alias=%q env %q: missing required env var %s", alias, key, m[1])
    }
    return v, nil
}
```

- [ ] **Step 2: Walk spawn-mode entries in `writeMCPConfig`**

In the function body, after the built-in MCP server entries are emitted and before the closing `json.Marshal`, add:

```go
if in.Profile != nil {
    for _, m := range in.Profile.ExtraMCPs {
        if m.Command == "" {
            continue // reference mode
        }
        env := map[string]string{}
        for k, v := range m.Env {
            resolved, err := interpolateEnv(m.Alias, k, v)
            if err != nil {
                return "", err
            }
            env[k] = resolved
        }
        server := map[string]any{
            "command": m.Command,
            "args":    m.Args,
        }
        if len(env) > 0 {
            server["env"] = env
        }
        servers[m.Alias] = server
    }
}
```

(`servers` is the existing `map[string]any` that's marshalled at the bottom of the function — verify the exact variable name in the file.)

- [ ] **Step 3: Run the tests; confirm they pass**

```bash
go test -run TestWriteMCPConfig_ExtraMCPs ./internal/preflight -v
```

Expected: PASS for both.

### Task 6.6: Walk both modes in the allowed-tools builders

**Files:**
- Modify: `internal/sessions/session.go::allowedTools`
- Modify: `internal/editor/editor.go`

Both files already iterate `prof.ExtraMCPs` (after the `Name` → `Alias` rename from 6.3). Honor `AllowedTools` overrides:

- [ ] **Step 1: Edit both builders**

```go
for _, m := range prof.ExtraMCPs {
    if len(m.AllowedTools) > 0 {
        out = append(out, m.AllowedTools...)
    } else {
        out = append(out, "mcp__"+m.Alias+"__*")
    }
}
```

- [ ] **Step 2: Add a test case for `AllowedTools`**

In `internal/sessions/session_test.go::TestAllowedTools_IncludesExtraMCPs`:

```go
{
    Alias: "prom-spawn",
    AllowedTools: []string{"mcp__prom-spawn__cpu_pressure"},
},
```

Assert the narrow tool is present and the wildcard `mcp__prom-spawn__*` is NOT.

- [ ] **Step 3: Run**

```bash
go test ./internal/sessions ./internal/editor -v
```

### Task 6.7: Delete `profiles/camunda/`

**Files:**
- Delete: `internal/profile/profiles/camunda/`

- [ ] **Step 1: Confirm no Go code embeds or names `camunda` as a default profile**

```bash
grep -rIn 'camunda' internal/profile/ pkg/auth/ cmd/triagent/ 2>/dev/null | head
```

The only legitimate hits should be in tests that use `camunda-docs` as an example external MCP. If `LoadEmbedded("camunda")` is called anywhere in tests or cmd code, fix to use `default` or a t.TempDir() profile.

- [ ] **Step 2: Delete**

```bash
git rm -r internal/profile/profiles/camunda
```

- [ ] **Step 3: Update `internal/profile/embed_test.go` (or equivalent)**

If a test embeds-and-loads `camunda`, change it to load `default` or build an inline profile.

### Task 6.8: Verify and commit

- [ ] **Step 1: Run the verification suite**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm run typecheck && npm run build && npm test -- --run )
```

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(profile): extra_mcps spawn mode + ${env:NAME} interpolation

Extends profile.ExtraMCP with Command/Args/Env/AllowedTools so the
launcher can spawn third-party MCPs by reading their config from the
profile. Renames the existing Name field to Alias. The existing
reference mode (no Command) is preserved unchanged. ${env:NAME} values
in Env are interpolated from os.LookupEnv at config-write time;
missing vars surface a preflight error. Deletes the camunda profile,
which still used the old Name field.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 7: Pluggable auth + runnable default profile

Add `pkg/auth/kubeconfig/`. Replace the hard-wired `teleport.NewProvider()` in `cmd/triagent/main.go` with a profile-driven switch. Rewrite `profiles/default/profile.yaml` to use `auth.kind: kubeconfig` with neutral inputs.

### Task 7.1: Write failing tests for the kubeconfig provider

**Files:**
- Create: `pkg/auth/kubeconfig/provider_test.go`

- [ ] **Step 1: Author the test**

```go
package kubeconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- name: cluster-a
  cluster: { server: https://a.example.com }
- name: cluster-b
  cluster: { server: https://b.example.com }
contexts:
- name: ctx-a
  context: { cluster: cluster-a, user: alice }
- name: ctx-b
  context: { cluster: cluster-b, user: bob }
current-context: ctx-a
users:
- name: alice
  user: { token: xxx }
- name: bob
  user: { token: yyy }
`

func writeTempKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(path, []byte(fakeKubeconfig), 0600))
	return path
}

func TestListClusters_EnumeratesContexts(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t))
	p := NewProvider()
	clusters, err := p.ListClusters(context.Background())
	require.NoError(t, err)
	assert.Len(t, clusters, 2)
	names := []string{clusters[0].Name, clusters[1].Name}
	assert.ElementsMatch(t, []string{"ctx-a", "ctx-b"}, names)
}

func TestIsAuthenticated_AlwaysTrue(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t))
	p := NewProvider()
	assert.True(t, p.IsAuthenticated())
}

func TestLogin_WritesSubKubeconfigPinningContext(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t))
	p := NewProvider()
	res, err := p.Login(context.Background(), "ctx-b")
	require.NoError(t, err)
	// Login returns a path; load it and check current-context == ctx-b.
	data, err := os.ReadFile(res.ContextName)
	require.NoError(t, err)
	assert.Contains(t, string(data), "current-context: ctx-b")
}
```

- [ ] **Step 2: Run and confirm it fails**

```bash
go test ./pkg/auth/kubeconfig -v
```

Expected: compile error — package doesn't exist yet.

### Task 7.2: Implement the kubeconfig provider

**Files:**
- Create: `pkg/auth/kubeconfig/provider.go`

- [ ] **Step 1: Author the provider**

```go
// Package kubeconfig is the default auth.Provider implementation:
// it reads $KUBECONFIG (or ~/.kube/config) and exposes each context as
// a cluster. Login writes a per-session sub-kubeconfig pinning the
// chosen context; the launcher passes that path as KUBECONFIG to all
// spawned subprocesses (per CLAUDE.md, subprocesses get explicit
// KUBECONFIG, never inherit ambient operator shell state).
package kubeconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sourcehawk/triagent/pkg/auth"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type provider struct{}

func NewProvider() auth.Provider { return &provider{} }

func (p *provider) ListClusters(_ context.Context) ([]auth.Cluster, error) {
	cfg, err := loadKubeconfig()
	if err != nil {
		return nil, err
	}
	out := make([]auth.Cluster, 0, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		out = append(out, auth.Cluster{
			Name: name,
			ID:   ctx.Cluster,
		})
	}
	return out, nil
}

func (p *provider) Login(_ context.Context, name string) (*auth.LoginResult, error) {
	cfg, err := loadKubeconfig()
	if err != nil {
		return nil, err
	}
	if _, ok := cfg.Contexts[name]; !ok {
		return nil, fmt.Errorf("context %q not found in kubeconfig", name)
	}

	// Write a per-session kubeconfig that pins the chosen context.
	dir, err := os.MkdirTemp("", "triagent-kubeconfig-*")
	if err != nil {
		return nil, fmt.Errorf("create per-session dir: %w", err)
	}
	subPath := filepath.Join(dir, "config")
	sub := cfg.DeepCopy()
	sub.CurrentContext = name
	if err := clientcmd.WriteToFile(*sub, subPath); err != nil {
		return nil, fmt.Errorf("write sub-kubeconfig: %w", err)
	}
	return &auth.LoginResult{ClusterName: name, ContextName: subPath}, nil
}

func (p *provider) IsAuthenticated() bool { return true }

func loadKubeconfig() (*clientcmdapi.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if env := os.Getenv("KUBECONFIG"); env != "" {
		rules.ExplicitPath = env
	}
	cfg, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}
```

- [ ] **Step 2: Run the tests; confirm green**

```bash
go test ./pkg/auth/kubeconfig -v
```

Expected: PASS.

### Task 7.3: Make provider construction profile-driven

**Files:**
- Modify: `cmd/triagent/main.go`

- [ ] **Step 1: Replace the hard-wired provider call**

Current `cmd/triagent/main.go` calls `teleport.NewProvider(teleport.Config{})` unconditionally and ignores the profile. Change to construct based on `profile.Auth.Kind`. The profile is loaded in `cmd/triagent/start.go`'s start command, not in `main.go`. Reorganize:

Option A — defer provider construction to `start.go` (where the profile is loaded):

In `cmd/triagent/start.go`, after the profile is loaded:

```go
provider, err := newAuthProvider(prof.Auth)
if err != nil {
    return err
}
mgr.SetProvider(provider)
```

And add the helper to `cmd/triagent/main.go` (or a new `cmd/triagent/auth.go`):

```go
import (
	"fmt"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/pkg/auth/kubeconfig"
	"github.com/sourcehawk/triagent/pkg/auth/teleport"
)

func newAuthProvider(profAuth profile.Auth) (auth.Provider, error) {
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

- [ ] **Step 2: Delete the old `provider := teleport.NewProvider(...)` block from `main.go`**

The cmd.SetProvider(provider) call moves to start.go after profile load.

### Task 7.4: Rewrite `profiles/default/profile.yaml`

**Files:**
- Modify: `internal/profile/profiles/default/profile.yaml`

- [ ] **Step 1: Author the new default profile**

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

- [ ] **Step 2: Update `profile.Auth.Kind` validation**

If `internal/profile/validate.go` rejects `auth.kind` values other than a known list, ensure `kubeconfig` is in that list.

```bash
grep -n 'Kind' internal/profile/validate.go
```

Make sure the switch includes both `kubeconfig` and `teleport`.

- [ ] **Step 3: Update embed tests**

If a test iterates `LoadEmbedded("default")` and asserts specific contents (e.g. that auth.kind was `unset`), update those expectations.

### Task 7.5: Verify and commit

- [ ] **Step 1: Run the verification suite**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm run typecheck && npm run build && npm test -- --run )
```

- [ ] **Step 2: Smoke-test the binary**

If you have a working `kubectl`:

```bash
go run ./cmd/triagent --version
go run ./cmd/triagent start --profile default
```

Expected: launcher boots and prints a localhost URL. (Don't follow through with an investigation; just verify it boots without auth.kind=unset errors.)

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(pkg/auth,profile): kubeconfig provider + runnable default profile

Adds pkg/auth/kubeconfig/ that lists $KUBECONFIG contexts as clusters,
writes a per-session sub-kubeconfig on Login, and never re-auths.
Drives cmd/triagent/start.go's provider construction off
profile.Auth.Kind so kubeconfig and teleport are interchangeable.
Rewrites the default profile to auth.kind: kubeconfig with neutral
investigation inputs; it boots zero-config on any machine with a
working kubectl.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 8: Rebrand

The final rewrite of every operator-visible Camunda string. Tasks here are mostly mechanical edits and sed passes; bundle related changes. After this phase the OSS audit ("does any file mention Camunda?") should turn up nothing except in commit messages (which we squash away in finalization).

### Task 8.1: Rename MCP wire aliases `c1-*` → `triagent-*`

**Files:**
- Modify: `internal/preflight/mcpconfig.go` (every `MCPAlias*` const)
- Modify: every test fixture and `.go` file referencing the old aliases
- Modify: frontend test fixtures (`ActivityPanel.test.tsx`, etc.) referencing `mcp__c1-*`

- [ ] **Step 1: Edit the consts**

```go
const (
    MCPAliasK8s          = "triagent-k8s"
    MCPAliasMeta         = "triagent-meta"
    MCPAliasStrategies   = "triagent-strategies"
    MCPAliasTeleport     = "triagent-teleport"
    MCPAliasWiki         = "triagent-wiki"
    MCPAliasSlack        = "triagent-slack"
    MCPAliasIncidentio   = "triagent-incidentio"
    MCPAliasParallel     = "triagent-parallel"
    MCPAliasSessions     = "triagent-sessions"
    MCPAliasAgentOp      = "triagent-agent-operator"
    MCPAliasSignalIngest = "triagent-signal-ingest"
    MCPAliasGitPrefix    = "triagent-git-"
)
```

(`MCPAliasProm` was deleted in phase 5.)

- [ ] **Step 2: Sed-rewrite any string literals that hard-coded the old aliases**

```bash
grep -rIln '"c1-k8s"\|"c1-meta"\|"c1-strategies"\|"c1-teleport"\|"c1-wiki"\|"c1-slack"\|"c1-incidentio"\|"c1-parallel"\|"c1-sessions"\|"c1-agent-operator"\|"c1-signal-ingest"\|"c1-git-' --include='*.go' . | head -20
```

Anywhere these hard-coded literals exist (in tests, in playbook YAML), update them. Prefer referencing the `MCPAlias*` const directly.

- [ ] **Step 3: Update playbook YAML files**

```bash
grep -rIln 'c1-k8s\|c1-meta\|c1-strategies\|c1-teleport\|c1-wiki\|c1-slack\|c1-incidentio\|c1-parallel\|c1-sessions\|c1-agent-operator\|c1-signal-ingest\|c1-git-' system/ internal/profile/profiles/default/ | head
```

If `system/*.yaml` or default-profile prompt files reference the old aliases (e.g. `tool: c1-k8s/list_resources`), sed-rewrite to `triagent-k8s/list_resources`.

- [ ] **Step 4: Update frontend test fixtures**

```bash
grep -rIln 'mcp__c1-' frontend/ | head
```

Fix each reference (`mcp__c1-agent-operator__send_message` → `mcp__triagent-agent-operator__send_message`).

### Task 8.2: Rename telemetry env vars `C1_MCP_*` → `TRIAGENT_MCP_*`

**Files:**
- Modify: `internal/preflight/mcpconfig.go` (every `EnvC1_*` const)
- Modify: `cmd/triagent-mcp/serve.go` and any MCP that reads them
- Modify: `pkg/mcp/telemetry/*.go`

- [ ] **Step 1: Sed-rewrite env-var string literals**

```bash
find . -name '*.go' -not -path './node_modules/*' \
  -exec sed -i 's|C1_MCP_|TRIAGENT_MCP_|g' {} +
```

- [ ] **Step 2: Rename the Go const names** (`EnvCRDsFile`, `EnvCrossplaneGroups`, etc., they're fine — the strings are what changed)

The Go-side const names like `EnvTelemetryURL` already use generic names; the values were `"C1_MCP_TELEMETRY_URL"`. The sed in step 1 updated the values. Verify:

```bash
grep -n '= "TRIAGENT_MCP_' internal/preflight/mcpconfig.go
```

- [ ] **Step 3: Check there's no remaining `C1_MCP_` token in Go code**

```bash
grep -rIn 'C1_MCP_' --include='*.go' . | head
```

Expected: empty. If a comment mentions the old name, fine to leave or update for clarity.

### Task 8.3: Rename launcher storage paths

**Files:**
- Modify: every Go file that computes paths under `~/.config/c1/...` or `<XDG_CACHE_HOME>/c1-mcp/...`

- [ ] **Step 1: Find storage path strings**

```bash
grep -rIn '"c1/plugins/investigate"\|"c1-mcp"' --include='*.go' . | head
```

Common locations: `internal/server/persist.go`, `internal/profile/`, MCP cache dirs.

- [ ] **Step 2: Sed-rewrite**

```bash
find . -name '*.go' -not -path './node_modules/*' \
  -exec sed -i 's|"c1/plugins/investigate"|"triagent"|g; s|"c1-mcp"|"triagent-mcp"|g' {} +
```

Be careful not to over-match — `c1-mcp` as a binary name in tests should stay where it's deliberately testing the launcher's spawn-command logic (now `triagent-mcp`).

### Task 8.4: Rebrand frontend strings (titles, DOM events, localStorage)

**Files:**
- Modify: `frontend/app/layout.tsx`
- Modify: `frontend/app/(main)/investigations/new/page.tsx`
- Modify: `frontend/app/(main)/layout.tsx`
- Modify: `frontend/app/(main)/investigations/client.tsx`
- Modify: `frontend/components/SessionWorkspace.tsx`
- Modify: `frontend/components/LinkedReposPanel.tsx`
- Modify: `frontend/components/EditorChatDrawer.tsx`
- Modify: `frontend/components/sessions/ArchiveButton.tsx`
- Modify: `frontend/components/DocsView.tsx`
- Modify: `frontend/components/PushPRModal.tsx`
- Modify: `frontend/components/ToolPicker.tsx`
- Modify: `frontend/components/WatchesList.test.tsx`, `CodefixProposalCard.test.tsx`, `ActivityPanel.test.tsx`
- Modify: `frontend/package.json`

- [ ] **Step 1: Sed-rewrite DOM event names**

```bash
find frontend -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.js' -o -name '*.jsx' \) -not -path '*/node_modules/*' -not -path '*/.next/*' -not -path '*/out/*' \
  -exec sed -i 's|c1-investigate:|triagent:|g' {} +
```

- [ ] **Step 2: Sed-rewrite localStorage keys**

```bash
find frontend -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.js' -o -name '*.jsx' \) -not -path '*/node_modules/*' -not -path '*/.next/*' -not -path '*/out/*' \
  -exec sed -i 's|"c1-investigate\.|"triagent.|g' {} +
```

- [ ] **Step 3: Rebrand the app title**

In `frontend/app/layout.tsx`:

```tsx
export const metadata: Metadata = {
  title: "triagent",
};
```

In `frontend/app/(main)/investigations/new/page.tsx`, change the `<h1>` text:

```tsx
<h1 className="text-2xl font-semibold tracking-tight">triagent</h1>
```

- [ ] **Step 4: Update `frontend/package.json`**

```json
{
  "name": "triagent-frontend",
  ...
}
```

- [ ] **Step 5: Update neutral repo fixtures in tests**

In `frontend/components/WatchesList.test.tsx`, `CodefixProposalCard.test.tsx`, `PushPRModal.tsx`:

- `camunda/c1` → `example-org/example-repo`
- `camunda/zeebe` → `example-org/example-repo`
- `c1-proposal/` branch prefix → `triagent-proposal/`
- "c1 triage" example watch name → "example triage"
- `camunda/c1-plugins` in `PushPRModal.tsx:118` → drop the hardcoded display; render from `defaults.playbooks_repo` only.

### Task 8.5: Rewrite ConnectionsPanel re-auth + OAuth instructions

**Files:**
- Modify: `frontend/components/ConnectionsPanel.tsx`

- [ ] **Step 1: Drive re-auth advice from the API**

The current panel hardcodes "File a *General Help Request* on the Camunda IT [help-desk]". Replace both occurrences (lines ~155 and ~181). Expose `auth.Provider`'s `ReauthAdvice()` (when the provider implements `auth.ReauthAdvisor`) via an existing or new launcher endpoint — look for an existing `/api/connections/*` or `/api/capabilities` handler — and render the advice string in the panel. Hide the re-auth block when the advice is empty (e.g. for the kubeconfig provider, which doesn't implement `ReauthAdvisor`).

- [ ] **Step 2: Replace the Slack OAuth instructions**

Today's copy points the user to a Camunda internal Slack app. Replace with generic guidance:

```tsx
<p>
  To connect Slack, create a Slack app in your workspace and grab a bot
  user token. Required scopes:{" "}
  <code>channels:history</code>, <code>channels:read</code>,{" "}
  <code>groups:history</code>, <code>groups:read</code>,{" "}
  <code>users:read</code>. See{" "}
  <a
    href="https://api.slack.com/quickstart"
    target="_blank"
    rel="noreferrer"
  >
    Slack's quickstart
  </a>{" "}
  for app creation and installation.
</p>
```

(Verify the actual scopes used by the slack MCP; cross-check `pkg/mcp/slack/`.)

- [ ] **Step 3: Replace the incident.io instructions**

```tsx
<p>
  To connect incident.io, generate an API key in your incident.io
  account (Settings → API keys). The key needs read access to incidents,
  timelines, and updates. See{" "}
  <a
    href="https://api-docs.incident.io"
    target="_blank"
    rel="noreferrer"
  >
    incident.io's API docs
  </a>.
</p>
```

### Task 8.6: Rebrand in-app docs (`frontend/public/docs/*.md`)

**Files:**
- Modify: `frontend/public/docs/overview.md`, `investigations.md`, `mcp.md`, `repos.md`, `wiki.md`, `playbooks.md`
- Create: `frontend/public/docs/connections.md`

Each of these files has Camunda framing throughout. The rewrite is editorial — no mechanical sed will catch the intent. For each:

- [ ] **Step 1: `overview.md`** — drop Camunda mentions; restate the launcher's purpose for a general SRE/infra audience.

- [ ] **Step 2: `investigations.md`** — replace "Camunda cluster triage" framing with generic "Kubernetes workload triage" or "cluster incident triage". Replace `camunda/c1-investigation-sessions` with a generic reference to "the sessions vault repo configured in your profile".

- [ ] **Step 3: `mcp.md`** — drop the Prometheus section entirely (the MCP was evicted in phase 5). Replace with a new "Writing or wiring your own MCP" section that uses spawn-mode `extra_mcps` as the worked example. Rewrite the Slack section to point at `connections.md` for token setup. Same for incidentio.

- [ ] **Step 4: `repos.md`, `wiki.md`, `playbooks.md`** — strip Camunda-as-the-default framing. The `camunda/c1-investigation-wiki` example becomes `<your-org>/<your-vault>` or a generic placeholder.

- [ ] **Step 5: Create `connections.md`** — short page covering: how to create a Slack app, scopes needed, where to paste the token in the connections panel; same for incident.io. Linked from the `ConnectionsPanel.tsx` instructions.

### Task 8.7: Rewrite CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Read the current CLAUDE.md, section by section**

```bash
wc -l CLAUDE.md
head -50 CLAUDE.md
```

- [ ] **Step 2: Remove Camunda-specific sections / lines**

Drop or rewrite:
- "The c1 ecosystem spans three repos" — drop, no longer relevant.
- "All Camunda-specific defaults … load at runtime from a profile" — generalize to "All organization-specific defaults load at runtime from a profile (`internal/profile/`)."
- "`mcp/k8s/default_kinds.json` is platform-neutral. Camunda CRDs live in the camunda profile" — keep first half, drop second.
- "Per-repo MCPs are aliased `c1-git-<alias>`" → "Per-repo MCPs are aliased `triagent-git-<alias>`".
- "The operator agent is a second `claude` session" — keep but neutralize any incident URL examples.
- "Frontmatter / YAML round-trips use `gopkg.in/yaml.v3`" — keep.
- Naming & terminology section — replace `c1-*` examples with `triagent-*`.
- The whole "Profile abstraction (Camunda-specific vs neutral)" section — replace with a shorter "Profile abstraction" section that explains the mechanism without naming Camunda.

- [ ] **Step 3: Keep the engineering-discipline rules verbatim**

Anti-patterns rejected, sub-agent runner contract, citation runner contract, commit conventions, testing hygiene, "single in-progress flag orphan recovery", "atomic.Pointer for hot-swappable client state", "events.jsonl is source of truth" — all stay.

### Task 8.8: Rewrite README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Author a new OSS-audience README**

Sections to include:

```markdown
# triagent

AI-assisted investigation agent for Kubernetes workloads. Spawns a
local Claude session with a guided playbook walker, curated MCP tools,
and a per-investigation transcript surfaced over a localhost web UI.

## Status

Pre-1.0. Interfaces are not stable.

## Install

```bash
go install github.com/sourcehawk/triagent/cmd/triagent@latest
go install github.com/sourcehawk/triagent/cmd/triagent-mcp@latest
```

Or build from source:

```bash
git clone https://github.com/sourcehawk/triagent
cd triagent
make build
./bin/triagent start
```

## Quickstart

triagent ships with a runnable default profile that uses your local
`$KUBECONFIG` for cluster access. With a working `kubectl`:

```bash
triagent start
```

A localhost URL prints in the terminal; open it. Pick a context, hand
the agent a symptom, and the guided playbook walker takes over.

## Profiles

A profile is a YAML directory that customizes auth, default repos,
prompts, and external MCP wiring for your org. Copy `internal/profile/profiles/default/`
as a starting point and override the fields you care about.

```bash
triagent start --profile path/to/your/profile/
```

See `docs/superpowers/specs/2026-05-16-open-source-restructure-design.md`
for the profile schema reference.

## External MCPs

triagent's `extra_mcps[]` profile field accepts either reference-mode
or spawn-mode entries. See `frontend/public/docs/mcp.md` (open it in
the app) for the worked example.

## License

[choose one — MIT, Apache 2.0, …]
```

- [ ] **Step 2: Pick a license**

Decide which license. Add `LICENSE` file at the repo root. (This is an open question that wasn't decided in the spec; the user picks now.)

### Task 8.9: New short DEVELOPER_GUIDE.md

**Files:**
- Create: `DEVELOPER_GUIDE.md` (was deleted in phase 2; this is a new, shorter version)

- [ ] **Step 1: Author the guide**

```markdown
# Developer guide

## Adding an MCP

1. Create `pkg/mcp/<name>/`.
2. Implement `New(Options) (*Server, error)` and `(*Server).Run(ctx)` plus a sibling `specs.go` with `ToolSpecs() []toolspec.ToolSpec`.
3. Register the kind in `cmd/triagent-mcp/serve.go`.
4. Add a `tools_wire_test.go` that cross-checks handler registration against `ToolSpecs()`.

## Adding an auth provider

1. Create `pkg/auth/<name>/`.
2. Implement `auth.Provider` (and optionally `auth.Authenticator`, `auth.ReauthAdvisor`).
3. Add a case in `cmd/triagent/auth.go::newAuthProvider`.
4. Add tests covering `ListClusters`, `Login`, `IsAuthenticated`.

## Adding a profile

Either copy `internal/profile/profiles/default/` to a sibling directory
(embedded) or place it anywhere on disk and pass `--profile <path>`.
Profile schema lives in `internal/profile/profile.go`.

## Wiring an external MCP

Edit your profile's `extra_mcps[]`. See the spawn-mode example in the
spec.

## Tests

```bash
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm test -- --run )
```

## Commit conventions

`feat(<area>):`, `refactor(<area>):`, `fix(<area>):`, `test(<area>):`,
`chore(<area>):`. Areas mirror the module path.
```

### Task 8.10: Final verification and rebrand commit

- [ ] **Step 1: Run the full verification suite**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm install && npm run typecheck && npm run build && npm test -- --run )
```

- [ ] **Step 2: Audit for residual Camunda references**

```bash
grep -rIn -e 'camunda' -e 'Camunda' -e 'c1-plugins' -e 'c1-investigate' . \
  --include='*.go' --include='*.tsx' --include='*.ts' --include='*.md' --include='*.yaml' --include='*.yml' --include='*.json' \
  | grep -v 'docs/superpowers/specs/' \
  | grep -v 'docs/superpowers/plans/' \
  | head -30
```

Spec and plan files retain Camunda references for historical context — those are excluded above. Anywhere else: fix.

- [ ] **Step 3: Audit for residual `c1-mcp` / `c1-k8s` etc.**

```bash
grep -rIn 'c1-mcp\|c1-k8s\|c1-prom\|c1-meta\|c1-strategies\|c1-teleport\|c1-wiki\|c1-slack\|c1-incidentio\|c1-parallel\|c1-sessions\|c1-agent-operator\|c1-signal-ingest\|c1-investigate' . \
  --include='*.go' --include='*.tsx' --include='*.ts' --include='*.md' --include='*.yaml' --include='*.yml' --include='*.json' \
  | grep -v 'docs/superpowers/' \
  | head
```

Expected: empty.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat: rebrand to triagent and rewrite operator-facing copy for OSS

Renames every operator-visible Camunda identifier:
  - MCP wire aliases c1-* → triagent-*
  - Telemetry env vars C1_MCP_* → TRIAGENT_MCP_*
  - Storage paths ~/.config/c1/... → ~/.config/triagent/...
  - Frontend DOM events c1-investigate:* → triagent:*
  - Frontend localStorage c1-investigate.* → triagent.*
  - App title "c1 investigate" → "triagent"
  - frontend/package.json name → triagent-frontend

Rewrites ConnectionsPanel re-auth and OAuth instructions for an OSS
audience (no more Camunda IT help-desk references); adds a new
connections.md page documenting Slack-app creation and incident.io
token setup. Rewrites in-app docs (overview, investigations, mcp,
repos, wiki, playbooks) to be project-agnostic. Rewrites CLAUDE.md to
drop Camunda-specific guidance while keeping engineering-discipline
rules. New README.md + short DEVELOPER_GUIDE.md.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 9: Finalization

Squash the 8-commit local sequence into a single initial commit and push to the public remote.

### Task 9.1: Final verification at HEAD

- [ ] **Step 1: Run the verification suite one more time**

```bash
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
( cd frontend && npm install && npm run typecheck && npm run build && npm test -- --run )
```

If anything fails, fix in a new commit before squashing.

### Task 9.2: Squash to a single initial commit

**Files:** none directly — git history rewrite only.

- [ ] **Step 1: Inspect the commit graph**

```bash
git log --oneline
```

You should see (most recent first):
- Phase 8 rebrand
- Phase 7 kubeconfig + default profile
- Phase 6 extra_mcps spawn mode
- Phase 5 evict prom
- Phase 4 drop c1-sdk
- Phase 3 lift pkg/mcp
- Phase 2 reshape to single module
- Phase 1 baseline
- (the two earlier spec commits)

- [ ] **Step 2: Soft-reset to the root commit (the original spec commit)**

```bash
git reset --soft $(git rev-list --max-parents=0 HEAD)
```

This moves HEAD to the root commit while keeping the entire working tree's
contents (everything from phases 1–8) staged. The root commit remains in
the graph; we'll fold the staged changes into it with `--amend` in step 4.

- [ ] **Step 3: Verify staging**

```bash
git status --short | head
git log --oneline
```

Expected: every file shows as `A` (staged add). `git log` shows a single
commit — the original root spec commit.

- [ ] **Step 4: Amend the root commit so it becomes the one-and-only initial commit**

```bash
git commit --amend -m "$(cat <<'EOF'
chore: initial commit

triagent — AI-assisted investigation agent for Kubernetes workloads.

A localhost web UI driving a Claude session with curated MCP tools and
a guided playbook walker. Single Go module at the repo root; two
binaries (triagent launcher + triagent-mcp multiplexer); MCP impls
publicly importable under pkg/mcp/<name>/; auth providers under
pkg/auth/{kubeconfig,teleport}/. Profile-driven configuration.

See README.md to get started.
EOF
)"
```

(No Co-Authored-By trailer in the public initial commit — the user's
preference. Adjust if they want one.)

- [ ] **Step 5: Verify history is one commit**

```bash
git log --oneline
git log --stat | head -40
```

Expected: a single `chore: initial commit` line that touches every
file in the repo (including the spec and plan).

### Task 9.3: Push to the public remote

- [ ] **Step 1: Add the remote**

```bash
git remote add origin https://github.com/sourcehawk/triagent.git
```

- [ ] **Step 2: Push `main`**

```bash
git push -u origin main
```

If GitHub requires force-push (because the remote already has commits — e.g. an auto-created README), confirm first with the user before `--force` is used. Prefer to delete the remote default branch via the GitHub UI and push fresh.

### Task 9.4: Final sanity check on the public repo

- [ ] **Step 1: Browse the public repo**

Open `https://github.com/sourcehawk/triagent` in a browser. Confirm:

- One commit visible.
- README.md renders.
- No `camunda` references in the GitHub-rendered tree, code search.
- CI runs (if .github/workflows/ has them) — wait for green.

---

## Risks captured during execution

- **`go.mod` indirect bloat after phase 2.** `go mod tidy` may pull in indirects from the c1-sdk graph that no longer apply once phase 4 lands. Re-run `go mod tidy` after phase 4 to prune.
- **The MCP wire test in phase 3.** `tools_wire_test.go` files in each `pkg/mcp/<name>/` cross-check tool names. They run as part of phase 3's verification — any drift between handler registration and `ToolSpecs()` surfaces here. Don't `--no-verify` past failures.
- **Frontend `package.json` rename + `node_modules` cache.** If `npm install` was previously run with the old name, delete `frontend/node_modules` and re-install in phase 8 before verifying.
- **Cobra version check helper.** When replacing `c1-sdk/plugin.Run`, the auto-`--version` and auto-`--help` behavior comes from setting `root.Version`. Ensure both binaries set it (the const) so the CLI surface looks complete.
- **Frontend embed `dist/.gitkeep` survival.** The `.gitkeep` anchor must persist through phases 2 (move) and 8 (rebrand). Verify with `git check-ignore -v internal/web/dist/.gitkeep` after each phase that touches frontend or `.gitignore`.

## What's deferred

- **Pluggable k8s auth providers beyond kubeconfig/teleport.** Separate spec + plan after this lands.
- **Dynamic switch-context for external MCPs.** Separate spec + plan after this lands.
- **Re-homing the Camunda profile + prom MCP in c1-plugins.** A separate change set in a separate repo, owned by the user.
- **Choosing an OSS license.** Decided in Task 8.8 step 2.
- **Choosing a binary version source (`const` vs `-ldflags -X`).** Pick during phase 4; default to `const`.
