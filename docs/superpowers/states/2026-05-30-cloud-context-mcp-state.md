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

- **Phase 1 (foundational)** — `#45` (scaffold + harness; produces every contract)
- **Phase 2 (consumers, parallel)** — `#43` (GCP provider), `#46` (AWS provider), `#47` (launcher integration)

## PRs / worktrees

| Issue | Branch | Worktree path | PR (→ base) | Status |
| ----- | ------ | ------------- | ----------- | ------ |
| #45 — scaffold + harness | feature/cloud-context-mcp--scaffold | .claude/worktrees/cloud-context-mcp--scaffold | _tbd_ → feature/cloud-context-mcp | dispatched |
| #43 — GCP provider | _tbd_ | _tbd_ | _tbd_ → feature/cloud-context-mcp | not-started |
| #46 — AWS provider | _tbd_ | _tbd_ | _tbd_ → feature/cloud-context-mcp | not-started |
| #47 — launcher integration | _tbd_ | _tbd_ | _tbd_ → feature/cloud-context-mcp | not-started |

## Contracts

| Name | Realization | Realized in | Status |
| ---- | ----------- | ----------- | ------ |
| `cloud-provider-interface` | stub-on-producer-branch (`cloud.Provider` + `fakeProvider` land in #45) | #45 | pending |
| `cloud-identity-probe` | stub-on-producer-branch (`cloud.Probe` + `IdentityStatus` exported by #45) | #45 | pending |
| `cloud-serve-cli` | data-only (`serve --kind=cloud --provider=<gcp\|aws>`) | n/a | pending |
| `cloud-env-contract` | data-only (`TRIAGENT_CLOUD_*` + impersonation env consts) | #45 / #43 / #46 | pending |

All four contracts are produced by #45, so Phase 2 cannot start until #45 merges into the feature branch. They flip to `locked` once #45's interface, probe, and env constants land.

## Bubble-up log

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
