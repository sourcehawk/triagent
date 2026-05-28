<!--
Orchestration state file for a planned feature. See
.claude/skills/planning-a-feature/SKILL.md for when this is created
and .claude/skills/developing-a-feature/SKILL.md for how it's updated
during implementation.

This file is scratch — same lifecycle as the plan. Tracked in git so
it survives sessions, worktrees, and machines; deleted by the
orchestrator's last commit when the feature ships (every sub-issue
closed and the epic closed).

The `status:` field is one of: planning | foundational-wave |
consumer-wave | review | merged.
-->

---
feature: <slug>
spec: docs/superpowers/specs/YYYY-MM-DD-<slug>-design.md
plan: docs/superpowers/plans/YYYY-MM-DD-<slug>-plan.md
tracking_issue: sourcehawk/triagent#<epic-num>
status: planning
---

# <Feature title> — orchestration state

## Phases

<!--
Implementation phases as the plan defines them. Each phase names the
sub-issues whose work runs in that phase; foundational phases
(contract producers) come before consumer phases. Single-PR features
collapse to one phase with one issue.
-->

- **Phase 1 (foundational)** — `sourcehawk/triagent#<n1>`, `sourcehawk/triagent#<n2>`
- **Phase 2 (consumers)** — `sourcehawk/triagent#<n3>`, `sourcehawk/triagent#<n4>`

## PRs / worktrees

<!--
One row per sub-issue. Branch and worktree are filled in when the
work starts; PR and status are filled in as the work progresses.
Keep this in sync with reality — a stale row is worse than no row.

Status values: not-started | in-progress | draft | ready | merged.
-->

| Issue                       | Branch                | Worktree path                    | PR                          | Status        |
| --------------------------- | --------------------- | -------------------------------- | --------------------------- | ------------- |
| sourcehawk/triagent#<n1>    | <branch-name>         | .claude/worktrees/<name>         | sourcehawk/triagent#<pr>    | not-started   |

## Contracts

<!--
Mirror of the plan's `## Contracts` table with a Realized-in pointer
and a Status. Status flips to `locked` once the stub PR merges, the
data-only row is documented, or the producer-branch stub is ready.
-->

| Name              | Realization                                              | Realized in                          | Status            |
| ----------------- | -------------------------------------------------------- | ------------------------------------ | ----------------- |
| `<contract-name>` | pre-merge stub PR / stub-on-producer-branch / data-only  | sourcehawk/triagent#<pr> or "n/a"    | pending / locked  |

## Bubble-up log

<!--
Concerns raised by any subagent during fan-out, the resolution, and
how it was propagated. One entry per concern; newest at the top. The
orchestrator owns appending here. Subagents surface concerns; they
don't write to this log directly.
-->

- _No concerns yet._

## Resume checklist

For a fresh Claude session resuming this work:

1. Read this state file in full.
2. Read the plan at the path in the `plan:` frontmatter.
3. Read the spec at the path in the `spec:` frontmatter.
4. Verify each open PR's actual state via `gh pr view <num> --repo sourcehawk/triagent`.
5. For each `in-progress` or `draft` row, `cd` to the worktree path and check `git status` + `git log --oneline main..HEAD`.
6. Re-dispatch subagents as needed per `developing-a-feature` (parallel waves still in flight; the orchestrator watch
   loop continues).
