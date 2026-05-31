# Cloud active-target selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `set_active_target` tool that lets the operator agent choose which project (GCP) or account (AWS) subsequent `run_cli` commands run against, from a deployment-pinned set, applied as an MCP-controlled per-exec env var.

**Architecture:** Extends the shipped cloud-context MCP (`pkg/mcp/cloud/`) — see `docs/superpowers/specs/2026-05-31-cloud-active-target-selection-design.md`. The `cloud.Server` holds the active target as in-memory session state and merges a provider-supplied env var into each `run_cli` child (`CLOUDSDK_CORE_PROJECT` for GCP, `AWS_PROFILE` for AWS); the agent never supplies the target flags (they stay deny-floored). AWS multi-account uses a per-source `accounts` list; triagent generates the `~/.aws/config` profiles at startup, so it holds no credentials.

**Tech Stack:** Go (`os/exec`, `os`, `text/template`/string building, `gopkg.in/ini.v1` or hand-written ini), the existing `pkg/mcp/cloud` package, `testify`.

**Folds into:** the `feature/cloud-context-mcp` branch (PR #53). Two sub-PRs (A: provider-agnostic core; B: provider impls + AWS accounts config + launcher wiring), each self-merged into the feature branch.

---

## Contracts

The provider-agnostic core (PR A) defines two new `Provider` interface methods; the gcp/aws impls (PR B) realize them.

| Name | Producer | Consumer | Shape |
| ---- | -------- | -------- | ----- |
| `provider-active-target` | PR A (interface + fake) | PR B (gcp, aws) | `ActiveTargetEnv(targetID string) []string` — the env var(s) to set for a target. GCP: `["CLOUDSDK_CORE_PROJECT=<id>"]`. AWS: `["AWS_PROFILE=<generated profile for id>"]`. |
| `provider-configured-targets` | PR A | PR B | `ConfiguredTargets() []Target` — targets the provider itself knows from config. AWS returns its `accounts` list; GCP returns `nil` (its set comes from `scope.projects`/inventory). |

`Target` is `struct { ID, Name string }` (same shape as `Scope`; reused conceptually).

## Conventions

Inherit the cloud package's conventions (provider impls live in `pkg/mcp/cloud/providers/<name>/`; CLI-only; testify table tests over fixtures; the deny floor and `Allows` are unchanged). New rules:

- **Active target is per-exec env, never `os.Setenv`.** The `Server` stores the active target ID in memory and builds the target env into each `execCLI` call (extending `subprocessEnv`). No process-global mutation, matching the probe fix.
- **The agent never names a profile or arbitrary target.** `set_active_target` validates the ID against the selectable set and rejects anything else; `--project`/`--account`/`--profile` stay deny-floored.
- **triagent generates AWS profiles, holds no credential.** Generated profiles are written to a managed, clearly-delimited block in `$HOME/.aws/config`; each is `role_arn` + the source's `source_profile` (the operator's SSO base). Generation is idempotent.

---

## PR A — provider-agnostic active-target core (against the fake)

### Task A1: `Target` type and the two `Provider` methods

**Files:**
- Modify: `pkg/mcp/cloud/provider.go`
- Modify: `pkg/mcp/cloud/fake_test.go`
- Test: `pkg/mcp/cloud/server_test.go`

- [ ] **Step 1: Write the failing test** — `fakeProvider` satisfies the extended interface.

```go
// server_test.go (add)
func TestFakeProviderSatisfiesActiveTargetContract(t *testing.T) {
	var p Provider = &fakeProvider{}
	require.NotNil(t, p)
	// compile-time: the interface now includes ActiveTargetEnv + ConfiguredTargets
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./pkg/mcp/cloud/ -run TestFakeProviderSatisfiesActiveTargetContract` → FAIL (fakeProvider missing methods).

- [ ] **Step 3: Add `Target` + the interface methods** in `provider.go`:

```go
// Target is one selectable project (gcp) or account (aws) the agent may make active.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Provider (add to the interface):
	// ConfiguredTargets is the deployment-configured selectable set the provider
	// itself knows (aws: its accounts list). Empty when the set comes from the
	// server's scope/inventory instead (gcp).
	ConfiguredTargets() []Target
	// ActiveTargetEnv returns the env var(s) that pin the CLI to targetID for the
	// next invocation: gcp CLOUDSDK_CORE_PROJECT, aws AWS_PROFILE. The agent never
	// supplies these; the server sets them per-exec.
	ActiveTargetEnv(targetID string) []string
```

- [ ] **Step 4: Implement on `fakeProvider`** in `fake_test.go`:

```go
func (f *fakeProvider) ConfiguredTargets() []Target { return f.targets }
func (f *fakeProvider) ActiveTargetEnv(id string) []string { return []string{"FAKE_TARGET=" + id} }
```
Add `targets []Target` to the `fakeProvider` struct.

- [ ] **Step 5: Run** `go test ./pkg/mcp/cloud/ -run TestFakeProviderSatisfiesActiveTargetContract -v` → PASS.

- [ ] **Step 6: Commit** `feat(cloud): Target type and active-target provider methods (#47-followup)`.

### Task A2: Server active-target state + selectable set + apply

**Files:**
- Modify: `pkg/mcp/cloud/server.go`
- Test: `pkg/mcp/cloud/server_test.go`

- [ ] **Step 1: Write failing tests** for the selectable set and env application:

```go
func TestSelectableTargetsPrefersConfigured(t *testing.T) {
	p := &fakeProvider{targets: []Target{{ID: "acct-1", Name: "one"}}}
	s := newTestServer(t, p) // helper already present
	got := s.selectableTargets(context.Background())
	assert.Equal(t, []Target{{ID: "acct-1", Name: "one"}}, got)
}

func TestSetActiveTargetRejectsOutOfSet(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}})
	require.Error(t, s.setActive("acct-9"))
	require.NoError(t, s.setActive("acct-1"))
	assert.Equal(t, "acct-1", s.activeTarget)
}

func TestSubprocessEnvIncludesActiveTarget(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}})
	require.NoError(t, s.setActive("acct-1"))
	assert.Contains(t, s.subprocessEnv(), "FAKE_TARGET=acct-1")
}
```

- [ ] **Step 2: Run** → FAIL (`selectableTargets`/`setActive`/`activeTarget` undefined).

- [ ] **Step 3: Implement** on `Server` in `server.go`:

```go
// add fields: activeTarget string

// selectableTargets returns the set the agent may choose from: the provider's
// configured targets (aws accounts) when present, else the scope projects, else
// (unconstrained) the live inventory scopes.
func (s *Server) selectableTargets(ctx context.Context) []Target {
	if t := s.provider.ConfiguredTargets(); len(t) > 0 {
		return t
	}
	if len(s.scope.Projects) > 0 {
		out := make([]Target, 0, len(s.scope.Projects))
		for _, p := range s.scope.Projects {
			out = append(out, Target{ID: p, Name: p})
		}
		return out
	}
	inv, err := s.provider.Inventory(ctx, s.run)
	if err != nil {
		return nil
	}
	out := make([]Target, 0, len(inv.Scopes))
	for _, sc := range inv.Scopes {
		out = append(out, Target{ID: sc.ID, Name: sc.Name})
	}
	return out
}

func (s *Server) setActive(id string) error {
	for _, t := range s.selectableTargets(context.Background()) {
		if t.ID == id {
			s.activeTarget = id
			return nil
		}
	}
	return fmt.Errorf("target %q is not in the configured set", id)
}
```
Extend `subprocessEnv()` to append `s.provider.ActiveTargetEnv(s.activeTarget)` when `s.activeTarget != ""`:

```go
func (s *Server) subprocessEnv() []string {
	env := minimalEnv(s.provider.EnvPassthrough())
	if s.activeTarget != "" {
		env = append(env, s.provider.ActiveTargetEnv(s.activeTarget)...)
	}
	return env
}
```

- [ ] **Step 4: Run** `go test ./pkg/mcp/cloud/ -run 'TestSelectable|TestSetActive|TestSubprocessEnvIncludes' -race -v` → PASS.

- [ ] **Step 5: Default-active + require-selection.** Add to `New`: if exactly one configured target, set `activeTarget` to it. Add a `requireActiveTarget()` guard `run` uses (Task A4). Test:

```go
func TestSingleTargetIsDefaultActive(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "only"}}})
	assert.Equal(t, "only", s.activeTarget)
}
func TestMultiTargetHasNoDefault(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "a"}, {ID: "b"}}})
	assert.Equal(t, "", s.activeTarget)
}
```
Implement the single-target default in `New` (after constructing `s`, call `if t := s.provider.ConfiguredTargets(); len(t)==1 { s.activeTarget = t[0].ID }`; for gcp single scope project, mirror via `selectableTargets` length check). Run → PASS.

- [ ] **Step 6: Commit** `feat(cloud): server active-target state, selectable set, and env application`.

### Task A3: `set_active_target` tool

**Files:**
- Create: `pkg/mcp/cloud/tools_target.go`
- Modify: `pkg/mcp/cloud/specs.go`, `pkg/mcp/cloud/server.go` (registerOn)
- Test: `pkg/mcp/cloud/tools_test.go`, `pkg/mcp/cloud/tools_wire_test.go`

- [ ] **Step 1: Write failing tests** (driven by `fakeProvider`): `set_active_target` with a valid ID sets the active target and returns the new `session_status`; an invalid ID returns a tool error; the wire test includes `set_active_target`.

```go
func TestSetActiveTargetTool(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}, identity: IdentityStatus{Provider: "fake", Valid: true}})
	_, out, err := s.setActiveTarget(context.Background(), nil, SetActiveTargetInput{Target: "acct-1"})
	require.NoError(t, err)
	assert.True(t, out.Valid)
	assert.Equal(t, "acct-1", s.activeTarget)

	res, _, _ := s.setActiveTarget(context.Background(), nil, SetActiveTargetInput{Target: "nope"})
	assert.True(t, res.IsError)
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** `tools_target.go`:

```go
package cloud

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descSetActiveTarget = "Choose which project (GCP) or account (AWS) subsequent run_cli commands run against, from the configured set shown by list_inventory. You cannot choose a target outside that set. Read-only."

type SetActiveTargetInput struct {
	Target string `json:"target" jsonschema:"The project id (GCP) or account id (AWS) to activate, from list_inventory."`
}

type SetActiveTargetOutput = IdentityStatus

func (s *Server) setActiveTarget(ctx context.Context, _ *mcp.CallToolRequest, in SetActiveTargetInput) (*mcp.CallToolResult, SetActiveTargetOutput, error) {
	if err := s.setActive(in.Target); err != nil {
		return errorResult(fmt.Sprintf("set_active_target rejected: %v", err)), SetActiveTargetOutput{}, nil
	}
	st, _ := Probe(ctx, s.provider, s.expectedIdentity, s.subprocessEnv())
	return nil, st, nil
}
```
Register it in `registerOn` and add it to `ToolSpecs()` (between `session_status` and `run_cli`).

- [ ] **Step 4: Run** `go test ./pkg/mcp/cloud/ -race -v` → PASS (incl. the wire test).

- [ ] **Step 5: Commit** `feat(cloud): set_active_target tool and spec`.

### Task A4: `run_cli` requires an active target when several exist

**Files:**
- Modify: `pkg/mcp/cloud/server.go` (`run`), `pkg/mcp/cloud/tools_cli.go`
- Test: `pkg/mcp/cloud/tools_test.go`

- [ ] **Step 1: Write failing test** — with multiple targets and none active, `run_cli` returns an actionable error; after `set_active_target`, it runs.

```go
func TestRunCLIRequiresActiveTargetWhenMultiple(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "a"}, {ID: "b"}}, binary: "/bin/echo",
		allow: &CommandAllowlist{Commands: []Command{{Path: "echo"}}}})
	res, _, _ := s.runCLI(context.Background(), nil, RunCLIInput{Argv: []string{"echo", "x"}})
	assert.True(t, res.IsError)
	assert.Contains(t, errText(res), "set_active_target")
}
```
(`errText` reads the error content; add if absent.)

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — in `Server.run`, before `validateArgv`: `if s.activeTarget == "" && len(s.selectableTargets(ctx)) > 1 { return CLIResult{}, errNoActiveTarget }` where `var errNoActiveTarget = errors.New("no active target; call set_active_target to choose one")`. `runCLI` surfaces it as a tool error.

- [ ] **Step 4: Run** `go test ./pkg/mcp/cloud/ -race -v` → PASS.

- [ ] **Step 5: Commit** `feat(cloud): run_cli requires an active target when several are configured`.

### Task A5: `session_status` reports the active target

**Files:**
- Modify: `pkg/mcp/cloud/provider.go` (`IdentityStatus` add `ActiveTarget string json:"active_target,omitempty"`), `pkg/mcp/cloud/tools_status.go`
- Test: `pkg/mcp/cloud/tools_test.go`

- [ ] **Step 1: Write failing test** — `session_status` includes the active target.

```go
func TestSessionStatusReportsActiveTarget(t *testing.T) {
	s := newTestServer(t, &fakeProvider{targets: []Target{{ID: "acct-1"}}, identity: IdentityStatus{Provider: "fake", Valid: true}})
	require.NoError(t, s.setActive("acct-1"))
	_, out, _ := s.sessionStatus(context.Background(), nil, SessionStatusInput{})
	assert.Equal(t, "acct-1", out.ActiveTarget)
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — add `ActiveTarget` to `IdentityStatus`; in `sessionStatus`, set `st.ActiveTarget = s.activeTarget` on the returned status. (Probe leaves it empty; the server fills it.)

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit** `feat(cloud): session_status reports the active target`.

---

## PR B — provider impls, AWS accounts config, launcher wiring

### Task B1: GCP provider implements the active-target methods

**Files:**
- Modify: `pkg/mcp/cloud/providers/gcp/provider.go`
- Test: `pkg/mcp/cloud/providers/gcp/provider_test.go`

- [ ] **Step 1: Failing test:**

```go
func TestGCPActiveTargetEnv(t *testing.T) {
	p, _ := newWithBinary("/usr/bin/gcloud")
	assert.Equal(t, []string{"CLOUDSDK_CORE_PROJECT=proj-1"}, p.ActiveTargetEnv("proj-1"))
	assert.Nil(t, p.ConfiguredTargets())
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** on the gcp `Provider`:

```go
func (p *Provider) ConfiguredTargets() []cloud.Target { return nil }
func (p *Provider) ActiveTargetEnv(id string) []string { return []string{"CLOUDSDK_CORE_PROJECT=" + id} }
```

- [ ] **Step 4: Run** → PASS. **Step 5: Commit** `feat(cloud/gcp): active-target via CLOUDSDK_CORE_PROJECT (#43-followup)`.

### Task B2: Profile config + AWS `accounts` on the profile model

**Files:**
- Modify: `internal/profile/profile.go` (`CloudSource`), `internal/profile/validate.go`
- Test: `internal/profile/profile_test.go`

- [ ] **Step 1: Failing test** — a `cloud:` aws source parses an `accounts` list and a `source_profile`; validation requires `source_profile` and at least one account when `accounts` is used, and unique account ids.

```go
func TestCloudSourceAWSAccounts(t *testing.T) {
	p, err := Parse([]byte(`
cloud:
  - alias: prod-aws
    provider: aws
    source_profile: sso-admin
    accounts:
      - {account_id: "111111111111", role_arn: "arn:aws:iam::111111111111:role/triage-readonly"}
      - {account_id: "222222222222", role_arn: "arn:aws:iam::222222222222:role/triage-readonly"}
`))
	require.NoError(t, err)
	require.NoError(t, p.Validate())
	assert.Len(t, p.Cloud[0].Accounts, 2)
}
```
Add a negative case: duplicate account_id → `Validate` error; aws `accounts` without `source_profile` → error.

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — extend `CloudSource`:

```go
type CloudAccount struct {
	AccountID string `yaml:"account_id"`
	RoleARN   string `yaml:"role_arn"`
}
// in CloudSource:
	SourceProfile string         `yaml:"source_profile,omitempty"` // aws SSO base profile for generated assume-role profiles
	Accounts      []CloudAccount `yaml:"accounts,omitempty"`
```
Extend `Validate`: for an aws source with `Accounts`, require `SourceProfile`, each `account_id`/`role_arn` non-empty, and account ids unique. The single-`assumed_identity` form stays valid (one-account case).

- [ ] **Step 4: Run** `go test ./internal/profile/ -race -v` → PASS. **Step 5: Commit** `feat(profile): aws cloud accounts list + source_profile (#47-followup)`.

### Task B3: AWS provider — configured targets, generated profiles, active-target env

**Files:**
- Modify: `pkg/mcp/cloud/providers/aws/provider.go`
- Create: `pkg/mcp/cloud/providers/aws/profiles.go` (profile generation)
- Test: `pkg/mcp/cloud/providers/aws/provider_test.go`, `profiles_test.go`

- [ ] **Step 1: Failing test for the profile generator** — given accounts + a source_profile + an alias, it produces a managed `~/.aws/config` block, idempotently.

```go
func TestGenerateProfilesBlock(t *testing.T) {
	dir := t.TempDir(); cfg := filepath.Join(dir, "config")
	accs := []Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/triage-readonly"}}
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))
	b, _ := os.ReadFile(cfg)
	assert.Contains(t, string(b), "[profile triagent-cloud-prod-aws-111111111111]")
	assert.Contains(t, string(b), "role_arn       = arn:aws:iam::111111111111:role/triage-readonly")
	assert.Contains(t, string(b), "source_profile = sso-admin")
	// idempotent: second write does not duplicate
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))
	b2, _ := os.ReadFile(cfg)
	assert.Equal(t, 1, strings.Count(string(b2), "[profile triagent-cloud-prod-aws-111111111111]"))
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** `profiles.go`: `writeManagedProfiles(configPath, alias, sourceProfile string, accs []Account)` rewrites a delimited block (`# BEGIN triagent-cloud-<alias>` … `# END triagent-cloud-<alias>`) atomically (tmp-file-then-rename, the repo's `atomicWrite` idiom), replacing any prior block for that alias so it is idempotent. Profile name: `triagent-cloud-<alias>-<account_id>`. Define `profileName(alias, accountID)` for reuse.

- [ ] **Step 4: Implement the provider methods.** `New` gains the accounts + alias + source_profile (passed from serve.go, Task B5); store `accounts []Account` and `alias`. On `New`, call `writeManagedProfiles($HOME/.aws/config or AWS_CONFIG_FILE, alias, sourceProfile, accounts)`.

```go
func (p *Provider) ConfiguredTargets() []cloud.Target {
	out := make([]cloud.Target, 0, len(p.accounts))
	for _, a := range p.accounts { out = append(out, cloud.Target{ID: a.ID, Name: a.ID}) }
	return out
}
func (p *Provider) ActiveTargetEnv(id string) []string {
	return []string{"AWS_PROFILE=" + profileName(p.alias, id)}
}
```
Test `ConfiguredTargets`/`ActiveTargetEnv` against a provider built with two accounts.

- [ ] **Step 5: Run** `go test ./pkg/mcp/cloud/providers/aws/ -race -v` → PASS. **Step 6: Commit** `feat(cloud/aws): configured accounts, generated profiles, active-target env (#46-followup)`.

### Task B4: AWS inventory reflects the configured accounts

**Files:**
- Modify: `pkg/mcp/cloud/providers/aws/inventory.go`
- Test: `pkg/mcp/cloud/providers/aws/inventory_test.go`

- [ ] **Step 1: Failing test** — when the provider has a configured `accounts` list, `Inventory` returns exactly those accounts (the reachable/selectable set), without calling `organizations list-accounts`.

```go
func TestInventoryUsesConfiguredAccounts(t *testing.T) {
	p := providerWithAccounts(t, []Account{{ID: "111111111111"}, {ID: "222222222222"}})
	inv, err := p.Inventory(context.Background(), failRun(t)) // run must NOT be called
	require.NoError(t, err)
	assert.Len(t, inv.Scopes, 2)
}
```
(`failRun` fails the test if invoked.)

- [ ] **Step 2: Run** → FAIL (current Inventory calls `organizations list-accounts`).

- [ ] **Step 3: Implement** — when `len(p.accounts) > 0`, `Inventory` returns those as `Scopes` directly. When empty (single-account legacy), keep the existing `organizations list-accounts` + caller-account fallback.

- [ ] **Step 4: Run** → PASS. **Step 5: Commit** `fix(cloud/aws): inventory reflects the configured accounts, not the whole org`.

### Task B5: serve.go + mcpconfig wiring

**Files:**
- Modify: `cmd/triagent-mcp/serve.go` (`runCloud`, env consts), `internal/preflight/mcpconfig.go`, `pkg/mcp/cloud/env.go`
- Test: `cmd/triagent-mcp/serve_cloud_test.go`, `internal/preflight/mcpconfig_test.go`

- [ ] **Step 1: Failing test (mcpconfig)** — an aws `CloudSource` with `accounts` + `source_profile` emits the accounts + source_profile to the cloud subprocess (JSON env `TRIAGENT_CLOUD_AWS_ACCOUNTS`, `cloud.EnvAWSSourceProfile`), and a gcp source is unaffected.

```go
func TestCloudSourceAWSAccountsEnv(t *testing.T) {
	env, err := cloudSourceEnv(profile.CloudSource{Alias: "prod-aws", Provider: "aws", SourceProfile: "sso-admin",
		Accounts: []profile.CloudAccount{{AccountID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}})
	require.NoError(t, err)
	assert.Contains(t, env[cloud.EnvAWSSourceProfile], "sso-admin")
	assert.NotEmpty(t, env[cloud.EnvAWSAccounts]) // JSON array
}
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement** — add `cloud.EnvAWSAccounts = "TRIAGENT_CLOUD_AWS_ACCOUNTS"` (JSON) and `cloud.EnvAWSSourceProfile = "TRIAGENT_CLOUD_AWS_SOURCE_PROFILE"` to `env.go`. `cloudSourceEnv` JSON-encodes the accounts and sets the source_profile for aws sources. `runCloud` decodes them and passes them to `aws.New(...)`; for the single-`assumed_identity` legacy form, build a one-element accounts list. Pass the source alias through (already available as the MCP alias).

- [ ] **Step 4: Run** `go test ./cmd/triagent-mcp/ ./internal/preflight/ -race -v` → PASS.

- [ ] **Step 5: Full verification** — `make test-go`, `make lint`, and `make build` all green. Commit `feat(cloud): wire aws accounts + source_profile through serve and mcpconfig (#47-followup)`.

### Task B6: docs

**Files:**
- Modify: `docs/content/cloud-providers.md`
- Test: a fresh-reader pass per `feature-dev-workflow:writing-docs` (RED/GREEN), then `make docs`.

- [ ] **Step 1:** Document `set_active_target`, the AWS `accounts` + `source_profile` config (with a multi-account example), the "one identity many projects (GCP) vs one role per account (AWS)" model, and that `run_cli` requires an active target when several are configured. Run the writing-docs fresh-reader loop; `make docs` builds clean. Commit `docs(cloud): document multi-account/project active-target selection`.

---

## Self-review

- **Spec coverage:** `set_active_target` (A3); selectable set incl. scope/inventory fallback (A2); per-exec env apply, never os.Setenv (A2 `subprocessEnv`); default-active + require-selection (A2/A4); GCP `CLOUDSDK_CORE_PROJECT` (B1); AWS accounts config (B2), generated profiles + `AWS_PROFILE` (B3); inventory honesty (B4); launcher wiring (B5); session_status active target (A5); docs (B6). The runtime-AssumeRole broker stays rejected (no task builds it). Region switching untouched (no task).
- **Placeholder scan:** the AWS profile-generation ini format, the env-var names, the tool schema, and all test bodies are spelled out; no TBD/TODO.
- **Type consistency:** `Target{ID,Name}`, `ActiveTargetEnv(string) []string`, `ConfiguredTargets() []Target`, `CloudAccount{AccountID,RoleARN}`, `SetActiveTargetInput{Target}`, `profileName(alias, accountID)`, `EnvAWSAccounts`/`EnvAWSSourceProfile` are used consistently across PR A and PR B.
