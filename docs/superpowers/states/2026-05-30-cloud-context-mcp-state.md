---
feature: cloud-context-mcp
spec: docs/superpowers/specs/2026-05-30-cloud-context-mcp-design.md
plan: docs/superpowers/plans/2026-05-30-cloud-context-mcp.md
tracking_issue: #44
feature_branch: feature/cloud-context-mcp
feature_worktree: .claude/worktrees/cloud-context-mcp
sub_pr_approval: autonomous
integration_pr:
status: developing
---

# Read-only cloud-context MCP (GCP and AWS) — orchestration state

## Phases

- **Phase 1 (foundational)** — `#45` (scaffold + harness; produces every contract). **Done** — self-merged as #48.
- **Phase 2a (providers, parallel)** — `#43` (GCP provider), `#46` (AWS provider). In flight.
- **Phase 2b (launcher, gated)** — `#47` (launcher integration). Gated on **both** #43 and #46 self-merging: its preflight + connections probe constructs `cloud.Provider` values to call `cloud.Probe`, so it imports the provider packages (see Bubble-up log). Dispatched only after 2a merges and the shared provider factory exists.

## PRs / worktrees

| Issue | Branch | Worktree path | PR (→ base) | Status |
| ----- | ------ | ------------- | ----------- | ------ |
| #45 — scaffold + harness | (merged, branch deleted) | (removed) | #48 → feature/cloud-context-mcp | self-merged |
| #43 — GCP provider | feature/cloud-context-mcp--gcp | .claude/worktrees/cloud-context-mcp--gcp | _tbd_ → feature/cloud-context-mcp | dispatched |
| #46 — AWS provider | feature/cloud-context-mcp--aws | .claude/worktrees/cloud-context-mcp--aws | _tbd_ → feature/cloud-context-mcp | dispatched |
| #47 — launcher integration | _tbd (Wave 2b)_ | _tbd (Wave 2b)_ | _tbd_ → feature/cloud-context-mcp | blocked (Wave 2b: needs #43 + #46 merged) |

## Contracts

| Name | Realization | Realized in | Status |
| ---- | ----------- | ----------- | ------ |
| `cloud-provider-interface` | stub-on-producer-branch (`cloud.Provider` + `fakeProvider` land in #45) | #45 (#48) | locked |
| `cloud-identity-probe` | stub-on-producer-branch (`cloud.Probe` + `IdentityStatus` exported by #45) | #45 (#48) | locked |
| `cloud-serve-cli` | data-only (`serve --kind=cloud --provider=<gcp\|aws>`) | #45 (#48) | locked |
| `cloud-env-contract` | data-only (`TRIAGENT_CLOUD_*` consts in `cloud/env.go`; provider impersonation env via `Provider.EnvPassthrough() []string`) | #45 (#48), provider names in #43/#46 | locked |
| `cloud-provider-factory` | new (discovered): `pkg/mcp/cloud/providers.New(name) (cloud.Provider, error)`, importing gcp+aws; `serve.go` + `preflight` + `connections` consume it | #47 (Wave 2b) | pending |

All four contracts landed with #45 (squash-merged as #48). Phase 2 (#43/#46/#47) is now unblocked. The `Provider` interface gained `EnvPassthrough() []string` during #45 review (see Bubble-up log) — #43/#46 must implement it, returning their CLI's credential/impersonation var names; `PATH`/`HOME` are already in the harness base set.

## Bubble-up log

- **2026-05-30 — discovered cross-PR dependency: #47 depends on #43 + #46 at compile time (plan corrected).** The plan claimed PR D (launcher) is "independent of B/C at compile time (references env-var name constants, not provider packages)." That is wrong: D3 (preflight) and D4 (connections) call `cloud.Probe(ctx, cloud.Provider)`, which needs a concrete `cloud.Provider`. A factory can't live in the `cloud` package (gcp/aws import `cloud`, so it would cycle); it must be a neutral package importing both providers — mirroring how the launcher already imports `pkg/auth/teleport` + `pkg/auth/kubeconfig` to build `auth.Provider`. **Resolution:** re-sequenced #47 to Phase 2b (after #43 + #46 self-merge). #47 introduces a shared provider factory `pkg/mcp/cloud/providers` (`New(name) (cloud.Provider, error)`) and refactors `cmd/triagent-mcp/serve.go`'s `newCloudProvider` to delegate to it — a third consumer (serve.go, preflight, connections) justifies the shared helper over copy-paste. **Propagation:** the premature #47 worktree/branch was removed; the plan's PR-breakdown dependency column and PR-D header are corrected; a `cloud-provider-factory` contract row is added. #43/#46 are unaffected (each still wires only its own `serve.go` arm; the factory extraction happens in #47 once serve.go is no longer contended).

- **2026-05-30 — known `serve.go` resource conflict between #43 and #46 (dispatch-time, pre-logged).** Both providers wire into `cmd/triagent-mcp/serve.go`: each adds an import (`providers/gcp` vs `providers/aws`) to the same import group and replaces its arm of the `newCloudProvider` stub switch (currently a combined `case "gcp", "aws":`). The import-group collision makes a trivial conflict inevitable at whichever provider PR merges **second**. **Resolution (orchestrator owns it):** dispatch both in parallel; each agent makes a minimal, localized edit (only its own import + its own case arm, leaving the other arm's "not built yet" stub untouched). At the second provider merge, resolve by taking the union — both imports, both real case arms. #47 (launcher) touches a disjoint file set and is conflict-free.

- **2026-05-30 — minimal-env seam missing in the harness (blocks #45 merge).** `cloud.Server.run` (server.go) calls `execCLI(..., argv, nil, ...)`; in Go a nil `cmd.Env` inherits the full parent environment, contradicting the spec's "explicit minimal `cmd.Env`" and `harness.go`'s own doc comment, and leaking the launcher's process env into `gcloud`/`aws`. The env-forwarding seam is owned by the parent package (conventions: subpackages own only CLI specifics), so it must land in #45 before fan-out. Resolution: #45 follow-up adds a provider-contributed env-passthrough (var **names** the CLI needs forwarded) merged with a minimal base set, built once and passed to `execCLI`; `fakeProvider` returns none. **Propagation:** #43/#46 implement the new `Provider` env-passthrough method; #47 unaffected (still injects env onto the `triagent-mcp` process). Interface grows by one method before consumers branch.
- **2026-05-30 — tests must use `testify` (user directive).** All cloud tests convert to `assert`/`require`; CLAUDE.md amended to make this the repo standard (testify is already used in 166 test files). **Propagation:** #43/#46/#47 inherit the rule via CLAUDE.md; their tests use testify from the start.

## Resume checklist

For a fresh Claude session resuming this work:

1. Read this state file in full.
2. Read the plan at the path in the `plan:` frontmatter.
3. Read the spec at the path in the `spec:` frontmatter.
4. Verify each open PR's actual state via `gh pr view <num>`.
5. For each `in-progress` or `draft` row, `cd` to the worktree path and check `git status` + `git log --oneline main..HEAD`.
6. Re-dispatch subagents as needed per `feature-dev-workflow:developing-a-feature` (Phase 2 fans out only after #45 merges).
