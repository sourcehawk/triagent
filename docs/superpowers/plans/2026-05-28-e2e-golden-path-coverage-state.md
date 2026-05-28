---
feature: e2e-golden-path-coverage
spec: docs/superpowers/specs/2026-05-28-e2e-golden-path-coverage-design.md
plan: docs/superpowers/plans/2026-05-28-e2e-golden-path-coverage-plan.md
tracking_issue: sourcehawk/triagent#13
feature_branch: feature/e2e-golden-path-coverage
feature_worktree: .claude/worktrees/e2e-golden-path-coverage
sub_pr_approval: autonomous                   # confirmed at developing-a-feature Step 2
integration_pr:                               # filled in once the feature → main PR opens
status: planning
---

# End-to-end golden-path test suite — orchestration state

## Phases

- **Phase 1 (foundational)** — `sourcehawk/triagent#14` (harness contract producer)
- **Phase 2 (consumers, parallel)** — `sourcehawk/triagent#15`, `sourcehawk/triagent#16`, `sourcehawk/triagent#17`
- **Phase 3 (consumers, parallel)** — `sourcehawk/triagent#18`, `sourcehawk/triagent#19` (start after #16 locks `browser-harness`)

## PRs / worktrees

| Issue                     | Branch                       | Worktree path                                            | PR (→ base)              | Status      |
| ------------------------- | ---------------------------- | -------------------------------------------------------- | ------------------------ | ----------- |
| sourcehawk/triagent#14    | e2e-harness-foundation       | .claude/worktrees/e2e-golden-path-coverage--foundation   | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#15    | e2e-flow1-boot               | .claude/worktrees/e2e-golden-path-coverage--flow1        | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#16    | e2e-flow2-investigation      | .claude/worktrees/e2e-golden-path-coverage--flow2        | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#17    | e2e-flow2b-k8s               | .claude/worktrees/e2e-golden-path-coverage--flow2b       | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#18    | e2e-flows34-editors          | .claude/worktrees/e2e-golden-path-coverage--editors      | → feature/e2e-golden-path-coverage | not-started |
| sourcehawk/triagent#19    | e2e-flow5-repos              | .claude/worktrees/e2e-golden-path-coverage--repos        | → feature/e2e-golden-path-coverage | not-started |

## Contracts

| Name                 | Realization              | Realized in              | Status  |
| -------------------- | ------------------------ | ------------------------ | ------- |
| `harness-api`        | code: #14 merges first   | sourcehawk/triagent#14   | pending |
| `stub-script-format` | data-only                | n/a (documented in plan) | pending |
| `claude-stub-trace`  | code: #14                | sourcehawk/triagent#14   | pending |
| `gh-stub-contract`   | data-only + code: #14    | sourcehawk/triagent#14   | pending |
| `healthz-shape`      | code: #14                | sourcehawk/triagent#14   | pending |
| `browser-harness`    | code: #16 before wave 3  | sourcehawk/triagent#16   | pending |
| `signal-release`     | code: #14                | sourcehawk/triagent#14   | pending |

## Bubble-up log

- _No concerns yet._

## Resume checklist

For a fresh Claude session resuming this work:

1. Read this state file in full.
2. Read the plan at the path in the `plan:` frontmatter.
3. Read the spec at the path in the `spec:` frontmatter.
4. Verify each open PR's actual state via `gh pr view <num> --repo sourcehawk/triagent`.
5. For each `in-progress` or `draft` row, `cd` to the worktree path and check `git status` + `git log --oneline feature/e2e-golden-path-coverage..HEAD`.
6. Re-dispatch subagents as needed per `developing-a-feature` (parallel waves still in flight; the orchestrator watch loop continues).
