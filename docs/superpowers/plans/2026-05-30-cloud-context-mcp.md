# Read-only cloud-context MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only cloud-context MCP (`pkg/mcp/cloud/`) that gives the operator agent GCP and AWS investigation context through a thin typed surface plus a bypass-resistant gated CLI, with a deployment-pinned read-only identity the agent cannot select or escalate.

**Architecture:** One package bound at launch by `--provider`, aliased `triagent-cloud-<alias>` (the git-MCP pattern). Provider behaviour sits behind an injectable `cloud.Provider` interface (the teleport pattern), with `gcp` and `aws` implementations in subpackages wired by `cmd/triagent-mcp/serve.go`. All cloud access shells the provider CLI through one exec core; no cloud SDK dependency. The launcher pins a read-only identity via harness-controlled env, validates it with a shared whoami probe surfaced in the connections panel and `preflight.Run()`, and degrades the cloud source visibly rather than blocking the session.

**Tech Stack:** Go (`os/exec`, `encoding/json`, `embed`), the `modelcontextprotocol/go-sdk/mcp` server, the existing `toolspec`, `auth.Provider`, `connections.Manager`, `preflight`, and `profile` packages; Next.js for the connections panel pill.

**Spec:** `docs/superpowers/specs/2026-05-30-cloud-context-mcp-design.md`

---

## PR breakdown

The feature lands via the feature-branch model on `feature/cloud-context-mcp`. Four sub-PRs, each its own sub-issue under epic #44:

| PR | Issue | Scope | Depends on |
| -- | ----- | ----- | ---------- |
| **A — scaffold + harness** | #45 | `pkg/mcp/cloud/`: `Provider` interface, command allowlist + deny floor, `run_cli` harness, `list_allowed_commands`, typed `list_inventory` + `session_status` against a fake provider, the shared identity probe, `serve.go` `--kind=cloud --provider=` wiring, wire test. | — |
| **B — GCP provider** | #43 | `pkg/mcp/cloud/providers/gcp`: implements `Provider` over `gcloud`; default allowlist + deny-floor additions; impersonation env contract. | A (interface) |
| **C — AWS provider** | #46 | `pkg/mcp/cloud/providers/aws`: implements `Provider` over `aws`; default allowlist + deny-floor additions; assume-role profile contract. | A (interface) |
| **D — launcher integration** | #47 | shared provider factory `pkg/mcp/cloud/providers`; profile `cloud:` block; `mcpconfig.go` aliasing + env injection; `preflight` cloud probe + visible degrade; `connections` cloud array + `GET /api/connections`; frontend read-only pill. | A (probe), **B + C (provider construction)** |

B and C run in parallel once A's contracts are realized. D runs **after both B and C merge**: its preflight + connections probe constructs `cloud.Provider` values to call `cloud.Probe`, so it imports the provider packages via a shared factory and cannot compile until both land. Each PR is independently reviewable and leaves `make test` green.

## File structure

`pkg/mcp/cloud/` (PR A):

- `provider.go` — the `Provider` interface and the projection structs every tool returns (`Inventory`, `IdentityStatus`, `CLIResult`).
- `allowlist.go` — `Command`, `CommandAllowlist`, `LoadCommandAllowlist(path)`, and the hardcoded `denyFloor` (subcommands, flags, arg-prefixes). Mirrors `pkg/mcp/k8s/allowlist.go`.
- `harness.go` — `execCLI(ctx, binPath, argv, env, limit)`: the no-shell argv exec core with validation hooks and output truncation.
- `validate.go` — `validateArgv(argv, allow *CommandAllowlist, scope ScopeAllowlist)`: normalizes the subcommand path, checks allowlist, rejects deny-floor tokens, validates scope.
- `probe.go` — `Probe(ctx, p Provider) (IdentityStatus, error)`: the shared whoami the launcher and `session_status` both call.
- `server.go` — `Options`, `New`, `registerOn`, `Run`. `Options.Provider` is a `Provider` value (DI, teleport pattern).
- `specs.go` — `ToolSpecs()`.
- `tools_inventory.go` — `list_inventory` handler.
- `tools_status.go` — `session_status` handler.
- `tools_cli.go` — `run_cli` and `list_allowed_commands` handlers.
- `fake_test.go` — `fakeProvider` implementing `Provider` for package tests.
- `tools_wire_test.go` — asserts `ToolSpecs()` matches registered handlers (the existing wire-test convention).
- `harness_security_test.go` — the bypass-resistance assertions (no `sh -c`, metacharacters inert, deny floor, scope).

`pkg/mcp/cloud/providers/gcp/` (PR B) and `pkg/mcp/cloud/providers/aws/` (PR C):

- `provider.go` — the `Provider` implementation (binary name, default allowlist, deny-floor additions, env builder, projection parsers).
- `default_commands.json` — embedded default allowlist for this provider.
- `provider_test.go` — table tests over CLI-output fixtures → projections.

Launcher (PR D):

- `internal/profile/profile.go` — add the `Cloud` config block.
- `internal/preflight/mcpconfig.go` — `MCPAliasCloudPrefix`, cloud env constants, the cloud server entry.
- `internal/preflight/preflight.go` — cloud identity probe + degrade marking.
- `internal/connections/connections.go` — cloud status entries in the response shape.
- `internal/server/handlers_connections.go` — cloud array in `GET /api/connections`.
- `frontend/` — read-only cloud pill in the connections panel.

## Contracts

| Name | Producer (PR/issue) | Consumer | Shape | Realization |
| ---- | ------------------- | -------- | ----- | ----------- |
| `cloud-provider-interface` | A / #45 | B/#43, C/#46 | `cloud.Provider` Go interface (see Task A2) | stub-on-producer-branch: A's `provider.go` lands the interface + a `fakeProvider`; B/C branch from A's merged state |
| `cloud-identity-probe` | A / #45 | D / NEW | `cloud.Probe(ctx, Provider) (IdentityStatus, error)` and `IdentityStatus{Provider, AssumedIdentity, Valid, Hint string}` | stub-on-producer-branch: A exports `Probe` + `IdentityStatus`; D imports them |
| `cloud-serve-cli` | A / #45 | D / NEW | `triagent-mcp serve --kind=cloud --provider=<gcp\|aws>` | data-only (CLI string) |
| `cloud-env-contract` | A+B+C | D / NEW | env var names the subprocess reads: `TRIAGENT_CLOUD_PROVIDER`, `TRIAGENT_CLOUD_ALLOWLIST_PATH`, `TRIAGENT_CLOUD_SCOPE`, plus provider impersonation env (`CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT` for gcp, `AWS_PROFILE` for aws) | data-only (exported consts in `cloud` + provider packages; D references the const names) |

`IdentityStatus` is the single struct the probe returns; the connections array, the `session_status` tool, and the preflight gate all render from it, so they cannot disagree.

## Conventions

Every sub-PR inherits these (the dimensions from `feature-dev-workflow:maintaining-architectural-coherence`):

- **Layout.** Provider implementations live in `pkg/mcp/cloud/providers/<name>/`, never in the parent package. The parent owns the interface, harness, allowlist, probe, tools; subpackages own only CLI specifics.
- **CLI-only access.** Every cloud read shells the provider binary through `cloud.execCLI`. No `cloud.google.com/go` or `aws-sdk-go` dependency in v1 — keeps auth and impersonation uniform (the CLI consumes the harness env).
- **Naming.** Server name `triagent-mcp-cloud`; session alias `triagent-cloud-<alias>`; tools `list_inventory`, `session_status`, `run_cli`, `list_allowed_commands`. The investigative groupings (inventory, reachability, permissions, cluster, logs, audit) are **axes** — used in prose and the allowlist's `Description` fields, never as Go identifiers, file names, or marker strings (the naming firewall).
- **Allowlist shape.** Provider default allowlists are `default_commands.json` embedded via `//go:embed`, loaded by the shared `LoadCommandAllowlist`, with the provider contributing deny-floor additions in code. The floor is never expressed in JSON (config can't re-enable it), mirroring how `LoadAllowlist` always filters `Secret`.
- **Output shaping.** Tools return projection structs, never raw API/CLI JSON. Redaction reuses the spirit of `pkg/mcp/k8s/redact.go`: secret-looking values are dropped, not surfaced.
- **Env discipline.** The agent supplies argv only. All credentials, impersonation, allowlist path, and scope reach the subprocess through `cmd.Env`, set by the launcher in `mcpconfig.go`. Identity-selecting flags are deny-floored in argv.
- **Tests.** Go race tests per the repo standard; CLI interaction is tested against captured-output fixtures (no live cloud). The wire test fails if `ToolSpecs()` drifts from registration.

---

## PR A — scaffold + harness (#45)

### Task A1: Package skeleton and server

**Files:**
- Create: `pkg/mcp/cloud/server.go`
- Create: `pkg/mcp/cloud/provider.go`
- Test: `pkg/mcp/cloud/server_test.go`, `pkg/mcp/cloud/fake_test.go`

- [ ] **Step 1: Write the failing test** — a `New` with a fake provider returns a server, and `New` with a nil provider errors.

```go
// server_test.go
func TestNewRequiresProvider(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error when Provider is nil")
	}
	if _, err := New(Options{Provider: &fakeProvider{}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Define the interface and fake** in `provider.go` and `fake_test.go`.

```go
// provider.go
type Provider interface {
	Name() string                       // "gcp" | "aws"
	Binary() string                     // resolved absolute path to gcloud/aws
	DefaultAllowlist() *CommandAllowlist // embedded default for this provider
	DenyFloorAdditions() DenyFloor       // provider-specific subcommands/flags
	Inventory(ctx context.Context, run RunFunc) (Inventory, error)
	Identity(ctx context.Context, run RunFunc) (IdentityStatus, error)
}

// RunFunc is the harness exec core, injected so providers never exec directly.
type RunFunc func(ctx context.Context, argv []string) (CLIResult, error)

type Inventory struct {
	Scopes []Scope `json:"scopes"` // projects (gcp) / accounts (aws)
}
type Scope struct {
	ID, Name string
}
type IdentityStatus struct {
	Provider        string `json:"provider"`
	AssumedIdentity string `json:"assumed_identity"`
	Valid           bool   `json:"valid"`
	Hint            string `json:"hint,omitempty"`
}
type CLIResult struct {
	Stdout    string `json:"stdout"`
	Truncated bool   `json:"truncated"`
	ExitCode  int    `json:"exit_code"`
}
```

```go
// fake_test.go
type fakeProvider struct{ identity IdentityStatus }
func (f *fakeProvider) Name() string                       { return "fake" }
func (f *fakeProvider) Binary() string                     { return "/bin/true" }
func (f *fakeProvider) DefaultAllowlist() *CommandAllowlist { return &CommandAllowlist{} }
func (f *fakeProvider) DenyFloorAdditions() DenyFloor       { return DenyFloor{} }
func (f *fakeProvider) Inventory(context.Context, RunFunc) (Inventory, error) { return Inventory{}, nil }
func (f *fakeProvider) Identity(context.Context, RunFunc) (IdentityStatus, error) { return f.identity, nil }
```

- [ ] **Step 3: Implement `server.go`** following the teleport pattern (`Options{Provider}`, `New`, `registerOn`, `Run`, server name `triagent-mcp-cloud`).
- [ ] **Step 4: Run** `go test ./pkg/mcp/cloud/ -run TestNewRequiresProvider -v` → PASS.
- [ ] **Step 5: Commit** `feat(cloud): provider interface and server skeleton (#45)`.

### Task A2: Command allowlist and deny floor

**Files:**
- Create: `pkg/mcp/cloud/allowlist.go`
- Test: `pkg/mcp/cloud/allowlist_test.go`

- [ ] **Step 1: Write failing tests** covering: an override path replaces the embedded default; a command on the deny floor is dropped even if the override lists it; provider deny-floor additions merge in.

```go
func TestLoadCommandAllowlistDropsDenyFloor(t *testing.T) {
	// JSON that tries to allow a deny-floored subcommand
	path := writeTemp(t, `{"commands":[{"path":"projects list"},{"path":"secrets versions access"}]}`)
	al, err := LoadCommandAllowlist(path, DenyFloor{})
	if err != nil { t.Fatal(err) }
	if al.Allows([]string{"secrets","versions","access"}) {
		t.Fatal("deny floor must drop secrets access regardless of config")
	}
	if !al.Allows([]string{"projects","list"}) {
		t.Fatal("projects list should be allowed")
	}
}
```

- [ ] **Step 2: Implement** `Command{Path, Description string, Redact bool}`, `CommandAllowlist{Commands []Command}`, `LoadCommandAllowlist(path string, extra DenyFloor)` mirroring `k8s.LoadAllowlist` (embedded default when path empty, else read file), then filter through the base `denyFloor` plus `extra`. `Allows(path []string)` normalizes and matches.

```go
// the always-on floor; config can never re-enable these (the Secret pattern)
var denyFloor = DenyFloor{
	Subcommands: []string{"secrets", "ssh", "scp", "cp", "sync", "auth", "config"},
	Flags:       []string{"--impersonate-service-account", "--account", "--profile",
		"--endpoint-url", "--cli-input-json", "--cli-input-yaml", "--configuration"},
	ArgPrefixes: []string{"file://", "fileb://", "@", "http://", "https://"},
}
```

- [ ] **Step 3: Run** `go test ./pkg/mcp/cloud/ -run TestLoadCommandAllowlist -v` → PASS.
- [ ] **Step 4: Commit** `feat(cloud): command allowlist with hardcoded deny floor (#45)`.

### Task A3: Argv validation

**Files:**
- Create: `pkg/mcp/cloud/validate.go`
- Test: `pkg/mcp/cloud/validate_test.go`

- [ ] **Step 1: Write failing tests** — table over: allowed verb passes; un-allowlisted verb rejected; each deny-floor flag rejected; each arg-prefix rejected; `--project` outside scope rejected; shell metacharacter tokens (`;`, `|`, `$(x)`) rejected by allowlist (not interpreted).

```go
func TestValidateArgvRejectsDenyFloorAndScope(t *testing.T) {
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances list"}}}
	scope := ScopeAllowlist{Projects: []string{"prod"}}
	cases := []struct{ name string; argv []string; ok bool }{
		{"allowed", []string{"compute","instances","list","--project","prod"}, true},
		{"bad-scope", []string{"compute","instances","list","--project","other"}, false},
		{"impersonate", []string{"compute","instances","list","--impersonate-service-account","x"}, false},
		{"file-prefix", []string{"compute","instances","list","--filter","@/etc/passwd"}, false},
		{"metachar", []string{"compute","instances","list",";","rm","-rf","/"}, false},
		{"not-allowed", []string{"iam","service-accounts","create"}, false},
	}
	// assert validateArgv(argv, al, scope) error-ness matches !ok
}
```

- [ ] **Step 2: Implement** `validateArgv`: split flags from positionals, normalize the leading subcommand path, `al.Allows`, reject any token matching a deny-floor flag / arg-prefix, validate `--project`/`--account`/region against `ScopeAllowlist`.
- [ ] **Step 3: Run** `go test ./pkg/mcp/cloud/ -run TestValidateArgv -v` → PASS.
- [ ] **Step 4: Commit** `feat(cloud): argv validation against allowlist, deny floor, and scope (#45)`.

### Task A4: No-shell exec core and truncation

**Files:**
- Create: `pkg/mcp/cloud/harness.go`
- Test: `pkg/mcp/cloud/harness_test.go`, `pkg/mcp/cloud/harness_security_test.go`

- [ ] **Step 1: Write failing security tests** — (a) source-level: the package contains no `"sh"`/`"bash"` `-c` exec construction; (b) behavioural: `execCLI` with argv `["-c","echo pwned"]` against `/bin/echo` prints the literal tokens, never spawning a second process; (c) output beyond `limit` sets `Truncated`.

```go
func TestExecCLINeverUsesShell(t *testing.T) {
	src, _ := os.ReadFile("harness.go")
	if bytes.Contains(src, []byte(`"-c"`)) || bytes.Contains(src, []byte("sh -c")) {
		t.Fatal("harness must never construct a shell command")
	}
}
func TestExecCLITruncates(t *testing.T) {
	r, err := execCLI(context.Background(), "/bin/echo", []string{strings.Repeat("x", 100)}, nil, 10)
	if err != nil { t.Fatal(err) }
	if !r.Truncated || len(r.Stdout) > 10 { t.Fatalf("expected truncation, got %+v", r) }
}
```

- [ ] **Step 2: Implement** `execCLI` with `exec.CommandContext(ctx, binPath, argv...)`, explicit minimal `cmd.Env`, `cmd.Stdin = nil`, captured stdout with a hard byte cap (`limit`), returning `CLIResult`. No shell, ever.
- [ ] **Step 3: Run** `go test ./pkg/mcp/cloud/ -run TestExecCLI -v -race` → PASS.
- [ ] **Step 4: Commit** `feat(cloud): no-shell argv exec core with output truncation (#45)`.

### Task A5: Identity probe

**Files:**
- Create: `pkg/mcp/cloud/probe.go`
- Test: `pkg/mcp/cloud/probe_test.go`

- [ ] **Step 1: Write failing test** — `Probe` delegates to `Provider.Identity` and returns its `IdentityStatus`; a provider error yields `Valid:false` with the error surfaced as `Hint`.
- [ ] **Step 2: Implement** `Probe(ctx, p Provider) (IdentityStatus, error)` calling `p.Identity` with a `RunFunc` bound to `execCLI` + the provider binary, validating the resolved identity is non-empty.
- [ ] **Step 3: Run** `go test ./pkg/mcp/cloud/ -run TestProbe -v` → PASS.
- [ ] **Step 4: Commit** `feat(cloud): shared identity probe (#45)`.

### Task A6: Tools and specs

**Files:**
- Create: `pkg/mcp/cloud/tools_inventory.go`, `tools_status.go`, `tools_cli.go`, `specs.go`
- Test: `pkg/mcp/cloud/tools_test.go`, `pkg/mcp/cloud/tools_wire_test.go`

- [ ] **Step 1: Write failing tests** (driven by `fakeProvider`): `list_inventory` returns the fake's scopes; `session_status` returns the fake's identity; `list_allowed_commands` returns the loaded allowlist; `run_cli` rejects a deny-floored argv before exec and shapes a `CLIResult` on success; the wire test asserts `ToolSpecs()` names match registered handlers.
- [ ] **Step 2: Implement** the four handlers and `ToolSpecs()` (server `triagent-cloud`, `toolspec.FromStruct` inputs). `run_cli` calls `validateArgv` then `execCLI`; `list_allowed_commands` reads the same `CommandAllowlist`.
- [ ] **Step 3: Run** `go test ./pkg/mcp/cloud/ -v -race` → PASS.
- [ ] **Step 4: Commit** `feat(cloud): list_inventory, session_status, run_cli, list_allowed_commands (#45)`.

### Task A7: serve.go wiring

**Files:**
- Modify: `cmd/triagent-mcp/serve.go` (add `case "cloud"`, `--provider` flag, `runCloud`)
- Test: `cmd/triagent-mcp/serve_test.go`

- [ ] **Step 1: Write failing test** — `--kind=cloud` with no/unknown `--provider` errors with a clear message; a known provider constructs a server.
- [ ] **Step 2: Implement** `runCloud(ctx, f)`: parse `--provider`, construct the gcp/aws impl (imported from the provider subpackages — stubbed to return an error "provider not built yet" until PRs B/C land, so A compiles and tests pass), call `cloud.New(cloud.Options{Provider: impl})`. Add `cloud` to the `--kind` usage strings.
- [ ] **Step 3: Run** `go test ./cmd/triagent-mcp/ -run TestServeCloud -v` → PASS; `make lint` clean.
- [ ] **Step 4: Commit** `feat(cloud): register --kind=cloud --provider in serve.go (#45)`.

---

## PR B — GCP provider (#43)

Branches from A's merged state (interface + harness available). Implements `cloud.Provider` over `gcloud`.

### Task B1: Provider skeleton and binary resolution

**Files:**
- Create: `pkg/mcp/cloud/providers/gcp/provider.go`, `default_commands.json`
- Test: `pkg/mcp/cloud/providers/gcp/provider_test.go`

- [ ] **Step 1:** Failing test — `New()` resolves the `gcloud` binary (via `exec.LookPath`, overridable for tests) and `Name()` returns `"gcp"`, `DefaultAllowlist()` loads the embedded JSON.
- [ ] **Step 2:** Implement the struct, `//go:embed default_commands.json`, and `DenyFloorAdditions()` (gcp-specific: e.g. `compute ssh`, `compute scp`, `functions call`).
- [ ] **Step 3:** Run `go test ./pkg/mcp/cloud/providers/gcp/ -v` → PASS.
- [ ] **Step 4:** Commit `feat(cloud/gcp): provider skeleton, default allowlist, deny-floor additions (#43)`.

### Task B2: Identity (whoami over impersonation)

- [ ] **Step 1:** Failing test over a captured `gcloud auth list --format=json` fixture → `IdentityStatus{AssumedIdentity, Valid}`, reading the active account and the `CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT` target.
- [ ] **Step 2:** Implement `Identity(ctx, run)` parsing the fixture shape; `Valid` requires the resolved identity to equal the impersonation target.
- [ ] **Step 3:** Run the test → PASS.
- [ ] **Step 4:** Commit `feat(cloud/gcp): identity probe over impersonation (#43)`.

### Task B3: Inventory (`gcloud projects list`)

- [ ] **Step 1:** Failing test over a `gcloud projects list --format=json` fixture → `Inventory{Scopes}`.
- [ ] **Step 2:** Implement `Inventory(ctx, run)` projecting id + name.
- [ ] **Step 3:** Run → PASS.
- [ ] **Step 4:** Commit `feat(cloud/gcp): inventory projection (#43)`.

### Task B4: Wire the provider into serve.go

- [ ] **Step 1:** Replace the A7 stub so `--provider=gcp` constructs `gcp.New()`.
- [ ] **Step 2:** Run `go test ./... -race` and `make lint` → PASS.
- [ ] **Step 3:** Commit `feat(cloud): wire gcp provider into serve.go (#43)`.

## PR C — AWS provider (#46)

Mirror of PR B over the `aws` CLI. Branches from A's merged state; independent of B.

### Task C1: Provider skeleton

- [ ] Binary `aws`; `Name()` `"aws"`; embedded `default_commands.json`; `DenyFloorAdditions()` (aws-specific: e.g. `ec2 get-password-data`, anything that returns credentials material).
- [ ] Commit `feat(cloud/aws): provider skeleton, default allowlist, deny-floor additions (#46)`.

### Task C2: Identity (`aws sts get-caller-identity`)

- [ ] Failing test over a `sts get-caller-identity` fixture → `IdentityStatus`; `Valid` requires the resolved ARN to match the pinned role (the `AWS_PROFILE` assume-role target).
- [ ] Commit `feat(cloud/aws): identity probe over assumed role (#46)`.

### Task C3: Inventory (`aws organizations list-accounts`, fallback `sts get-caller-identity` account)

- [ ] Failing test over a `list-accounts` fixture → `Inventory{Scopes}`; on `AccessDenied` (no orgs access) fall back to the single caller account.
- [ ] Commit `feat(cloud/aws): inventory projection with single-account fallback (#46)`.

### Task C4: Wire into serve.go

- [ ] `--provider=aws` constructs `aws.New()`; `go test ./... -race` + `make lint` → PASS.
- [ ] Commit `feat(cloud): wire aws provider into serve.go (#46)`.

## PR D — launcher integration (#47)

Branches from the feature branch **after both B and C have merged** (needs `cloud.Probe`, `IdentityStatus`, the env-const names, and a constructed `cloud.Provider` per source). It depends on the provider packages at compile time: D3/D4 call `cloud.Probe(ctx, cloud.Provider)`, and the only way to obtain a `cloud.Provider` is to construct a concrete gcp/aws value. D therefore introduces a shared factory `pkg/mcp/cloud/providers.New(name) (cloud.Provider, error)` (importing gcp + aws), refactors `cmd/triagent-mcp/serve.go`'s `newCloudProvider` to delegate to it, and uses it in `preflight` and `connections` — mirroring how the launcher already builds `auth.Provider` from `pkg/auth/teleport` / `pkg/auth/kubeconfig`.

### Task D0: Shared provider factory

**Files:**
- Create: `pkg/mcp/cloud/providers/registry.go`
- Modify: `cmd/triagent-mcp/serve.go` (delegate `newCloudProvider` to the factory)
- Test: `pkg/mcp/cloud/providers/registry_test.go`

- [ ] **Step 1:** Failing test — `New("gcp")` returns a non-nil `cloud.Provider` whose `Name()` is `"gcp"`; `New("aws")` likewise; an unknown name errors.
- [ ] **Step 2:** Implement `New(name)` switching to `gcp.New()` / `aws.New()`; refactor `serve.go`'s `newCloudProvider` to call it (removing the per-arm construction the providers added — the factory is now the single construction site).
- [ ] **Step 3:** Run `go test ./pkg/mcp/cloud/providers/ ./cmd/triagent-mcp/ -race` → PASS.
- [ ] **Step 4:** Commit `feat(cloud): shared provider factory; serve.go delegates construction (#47)`.

### Task D1: Profile `cloud:` block

**Files:**
- Modify: `internal/profile/profile.go`, `internal/profile/embed.go` (base merge)
- Test: `internal/profile/profile_test.go`

- [ ] **Step 1:** Failing test — a profile YAML with a `cloud:` block loads into `Profile.Cloud`, and `base:` merge inherits it when the override omits it.
- [ ] **Step 2:** Add `Cloud []CloudSource` with `{Alias, Provider, AssumedIdentity, Scope, CommandAllowlistPath string}`; extend `applyBase`.
- [ ] **Step 3:** Run `go test ./internal/profile/ -race` → PASS.
- [ ] **Step 4:** Commit `feat(profile): cloud source config block (#NEW)`.

### Task D2: mcpconfig aliasing and env injection

**Files:**
- Modify: `internal/preflight/mcpconfig.go`
- Test: `internal/preflight/mcpconfig_test.go`

- [ ] **Step 1:** Failing test — given a `CloudSource`, `writeMCPConfig` emits a `triagent-cloud-<alias>` server with `args ["serve","--kind=cloud","--provider=<p>"]` and env carrying `TRIAGENT_CLOUD_PROVIDER`, `TRIAGENT_CLOUD_ALLOWLIST_PATH`, `TRIAGENT_CLOUD_SCOPE`, and the impersonation env (`CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT` / `AWS_PROFILE`) from the source's `AssumedIdentity`.
- [ ] **Step 2:** Add `MCPAliasCloudPrefix = "triagent-cloud-"`, the env constants, and the cloud loop mirroring the linked-repos loop.
- [ ] **Step 3:** Run `go test ./internal/preflight/ -race` → PASS.
- [ ] **Step 4:** Commit `feat(preflight): wire triagent-cloud-<alias> servers with pinned-identity env (#NEW)`.

### Task D3: Preflight probe and visible degrade

**Files:**
- Modify: `internal/preflight/preflight.go`
- Test: `internal/preflight/preflight_test.go`

- [ ] **Step 1:** Failing test — when a cloud source's `cloud.Probe` returns `Valid:false`, the session still starts (no error) but the source is marked unavailable in the `Result`; when `Valid:true` it's available.
- [ ] **Step 2:** Add a `CloudSources []CloudSourceStatus` field to `Result`; run `cloud.Probe` per source after kubeconfig freeze; never return an error for a failed cloud probe (degrade, don't block); attach the `Hint`.
- [ ] **Step 3:** Run `go test ./internal/preflight/ -race` → PASS.
- [ ] **Step 4:** Commit `feat(preflight): cloud identity probe with visible degrade (#NEW)`.

### Task D4: Connections array and API

**Files:**
- Modify: `internal/connections/connections.go`, `internal/server/handlers_connections.go`
- Test: `internal/connections/connections_test.go`, `internal/server/handlers_connections_test.go`

- [ ] **Step 1:** Failing test — `GET /api/connections` includes a `cloud` array of `{provider, assumed_identity, valid, hint}` built from the profile's cloud sources probed at request time; the entries are read-only (no `PUT`/`DELETE` route added for cloud).
- [ ] **Step 2:** Extend the response builder to enumerate profile cloud sources and run `cloud.Probe`; reuse `IdentityStatus` fields directly.
- [ ] **Step 3:** Run `go test ./internal/connections/ ./internal/server/ -race` → PASS.
- [ ] **Step 4:** Commit `feat(connections): read-only cloud identity status in /api/connections (#NEW)`.

### Task D5: Frontend pill

**Files:**
- Modify: the connections panel component under `frontend/`
- Test: the panel's vitest spec

- [ ] **Step 1:** Failing vitest — the panel renders a cloud pill per `cloud[]` entry showing the assumed identity and a checkmark when `valid`, and the reauth `hint` when not; the pill has no edit affordance.
- [ ] **Step 2:** Render the cloud entries alongside Slack/incident.io, read-only.
- [ ] **Step 3:** Run `cd frontend && npm test -- --run` and `npm run typecheck` → PASS.
- [ ] **Step 4:** Commit `feat(web): read-only cloud identity pills in connections panel (#NEW)`.

---

## Self-review

- **Spec coverage:** package/`--provider`/alias (A1, A7, D2); thin typed tools (A6); `run_cli` + `list_allowed_commands` (A6); no-shell harness + deny floor + scope + truncation (A2–A4); shared probe (A5) across `session_status` (A6), preflight (D3), connections (D4); pinned-identity impersonation env (D2, B2, C2); visible degrade (D3); read-only connections pill (D4–D5); GCP/AWS providers (B, C). Alternatives/non-goals (SDK, OAuth, mutation) are enforced by the CLI-only convention, the deny floor, and the absence of write paths.
- **Placeholder scan:** provider projection internals (B2–B3, C2–C3) are specified as "parse this fixture into this struct" with the fixture and struct named; the exact field-by-field parse is filled during TDD against captured CLI output, which is the correct altitude (inventing `gcloud` JSON keys now would be a guess). No `TBD`/`TODO` remain.
- **Type consistency:** `Provider`, `RunFunc`, `Inventory`/`Scope`, `IdentityStatus`, `CLIResult`, `CommandAllowlist`/`Command`, `DenyFloor`, `ScopeAllowlist`, `cloud.Probe`, `MCPAliasCloudPrefix`, and the `TRIAGENT_CLOUD_*` env names are used consistently across tasks and the contracts table.
