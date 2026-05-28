---
feature: e2e-golden-path-coverage
spec: docs/superpowers/specs/2026-05-28-e2e-golden-path-coverage-design.md
plan: docs/superpowers/plans/2026-05-28-e2e-golden-path-coverage-plan.md
tracking_issue: sourcehawk/triagent#13
feature_branch: feature/e2e-golden-path-coverage
feature_worktree: .claude/worktrees/e2e-golden-path-coverage
sub_pr_approval: autonomous                   # confirmed at developing-a-feature Step 2
integration_pr:                               # filled in once the feature → main PR opens
status: foundational-wave
---

# End-to-end golden-path test suite — orchestration state

## Phases

- **Phase 1 (foundational)** — `sourcehawk/triagent#14` (harness contract producer)
- **Phase 2 (consumers, parallel)** — `sourcehawk/triagent#15`, `sourcehawk/triagent#16`, `sourcehawk/triagent#17`
- **Phase 3 (consumers, parallel)** — `sourcehawk/triagent#18`, `sourcehawk/triagent#19` (start after #16 locks `browser-harness`)

## PRs / worktrees

| Issue                     | Branch                       | Worktree path                                            | PR (→ base)              | Status      |
| ------------------------- | ---------------------------- | -------------------------------------------------------- | ------------------------ | ----------- |
| sourcehawk/triagent#14    | e2e-harness-foundation       | (removed; merged)                                        | sourcehawk/triagent#20 → feature/e2e-golden-path-coverage | self-merged |
| sourcehawk/triagent#15    | e2e-flow1-boot               | .claude/worktrees/e2e-golden-path-coverage--flow1        | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#16    | e2e-flow2-investigation      | .claude/worktrees/e2e-golden-path-coverage--flow2        | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#17    | e2e-flow2b-k8s               | .claude/worktrees/e2e-golden-path-coverage--flow2b       | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#18    | e2e-flows34-editors          | .claude/worktrees/e2e-golden-path-coverage--editors      | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#19    | e2e-flow5-repos              | .claude/worktrees/e2e-golden-path-coverage--repos        | → feature/e2e-golden-path-coverage | not-started |

## Contracts

| Name                 | Realization              | Realized in              | Status  |
| -------------------- | ------------------------ | ------------------------ | ------- |
| `harness-api`        | code: #14 merges first   | sourcehawk/triagent#20   | locked  |
| `stub-script-format` | data-only                | sourcehawk/triagent#20   | locked  |
| `claude-stub-trace`  | code: #14                | sourcehawk/triagent#20   | locked  |
| `gh-stub-contract`   | data-only + code: #14    | sourcehawk/triagent#20   | locked  |
| `healthz-shape`      | code: #14                | sourcehawk/triagent#20   | locked  |
| `browser-harness`    | code: #16 before wave 3  | sourcehawk/triagent#16   | pending |
| `signal-release`     | code: #14                | sourcehawk/triagent#20   | locked  |

## Bubble-up log

- **2026-05-28 — #14 contract refinements (propagate to wave 2/3 dispatch prompts).** The foundation subagent locked these concrete details on top of the plan's Contracts table; consumers must use them verbatim:
  1. `triagent start` gained a real `--port` flag (`127.0.0.1:<port>`; `0` = random). Flow 1 (#15) asserts it exists.
  2. `/healthz` version comes from a new `server.Options.Version` (threaded from `cmd/triagent`; defaults to `"dev"` locally). `/healthz` is **unauthenticated** (exempted in `authMiddleware`).
  3. Stub env vars the harness sets: `CLAUDE_STUB_SCRIPT`, `GH_STUB_SCRIPT`, `CLAUDE_STUB_TRACE_DIR`, `GH_STUB_TRACE_DIR`, `CLAUDE_STUB_SIGNAL_DIR`. Traces land at `<state_dir>/traces/`; signals at `<state_dir>/signals/<name>`.
  4. `seedFixtures` copies session/playbook/wiki/repo scenarios into `${XDG_CONFIG_HOME}/triagent/<profile>/<bucket>`; the profile is loaded via `--profile <fixture-path>` (not copied).
  5. `expect_tool_call`/`expect_tool_result` block on a stdin read only (no MCP client in the stub) — the real round-trip is realized by #17 (k8s flow).
  6. `Harness.Browser` is a stable placeholder type; #16 realizes the config-overlay + `Run`. `K8s`/`K8sFixtures` Options fields are reserved (wired by #17).
  Propagation: folded into the wave-2 (#15/#16/#17) and wave-3 (#18/#19) dispatch prompts; no running subagent to update.

## Resume checklist

For a fresh Claude session resuming this work:

1. Read this state file in full.
2. Read the plan at the path in the `plan:` frontmatter.
3. Read the spec at the path in the `spec:` frontmatter.
4. Verify each open PR's actual state via `gh pr view <num> --repo sourcehawk/triagent`.
5. For each `in-progress` or `draft` row, `cd` to the worktree path and check `git status` + `git log --oneline feature/e2e-golden-path-coverage..HEAD`.
6. Re-dispatch subagents as needed per `developing-a-feature` (parallel waves still in flight; the orchestrator watch loop continues).
