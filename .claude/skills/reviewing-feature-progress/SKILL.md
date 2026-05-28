---
name: reviewing-feature-progress
description:
  Use at feature-development orchestration checkpoints — between
  fan-out waves, before opening the integration PR, and before
  flipping the integration PR ready.
---

# reviewing-feature-progress

## When to invoke

Three checkpoints during a feature in flight:

1. **Between fan-out waves.** After wave N is fully self-merged and before wave N+1 dispatches — catches drift while
   it's still cheap to fix.
2. **Before opening the integration PR.** The integration PR is the external-review surface; its first impression
   has to be right.
3. **Before flipping a draft integration PR to ready.** Any feedback addressed since the draft opened needs to be
   re-aligned against the spec/plan.

Skip for ad-hoc fixes that didn't go through `planning-a-feature`.

## Why this exists

Per-sub-PR review (`review` skill, run by the orchestrator inside `fanning-out-with-worktrees`) checks one diff at
one moment. It doesn't check whether all sub-PRs together still implement what the spec promised, whether the
acceptance criteria are covered, or whether the feature branch as a whole compiles and tests cleanly. Drift
accumulates silently across waves; this skill catches it at the boundary.

## Workflow

### 1. Re-read the artifacts

Open these in order:

- The state file (`docs/superpowers/plans/<date>-<slug>-state.md`).
- The plan (`docs/superpowers/plans/<date>-<slug>-plan.md`), with focus on the `## Contracts` section and the
  PR-by-PR breakdown.
- The spec (`docs/superpowers/specs/<date>-<slug>-design.md`), with focus on goals + non-goals.
- Each closed sub-issue's `## Acceptance criteria` section (`gh issue view <num> --repo sourcehawk/triagent`).

### 2. Walk each self-merged sub-PR against the plan

For every row in the state file's `## PRs / worktrees` table with status `self-merged`:

- **Diff vs plan.** Use the `review` skill against the sub-PR number to walk the diff with full context. Cross-check
  against the sub-issue's acceptance criteria — does the diff cover every bullet?
- **Contract realization.** If the sub-PR was a contract producer, inspect the symbol / wire / data layout that
  actually shipped against what the contract row in the plan documented. The contract row's `Status` should be
  `locked` and `Realized in` should point at the merged sub-PR.

### 3. Acceptance-criteria coverage

For every sub-issue (whether `self-merged` or still open):

- Is every bullet in the issue's `## Acceptance criteria` section now testable / observable in the feature branch?
- If a criterion isn't covered, classify:
  - **Missing implementation** — file or surface a follow-up sub-PR; do not open the integration PR yet.
  - **De-scoped during planning** — update the issue body to remove the criterion (via `writing-github-issues` Step
    2B, with confirmation); do not silently drop it.
  - **Rephrased / equivalent** — the implementation satisfies the criterion under a different name; update the
    criterion to match the language used in the diff.

### 4. State-file integrity check

Walk the state file and verify reality against record:

- Every `self-merged` row's PR has actually merged into the feature branch (`gh pr view <num> --json mergedAt --jq
  .mergedAt`).
- Every `locked` contract row's `Realized in` PR is in fact merged.
- Every `## Bubble-up log` entry has a propagation path recorded — no concerns left unresolved.
- The `feature_branch` and `feature_worktree` frontmatter still point at real things on disk
  (`git rev-parse --verify feature/<slug>` + `ls <feature_worktree>`).

If anything is out of sync, fix the state file before continuing — the resumed-session contract depends on it.

### 5. End-to-end verification on the feature branch

**REQUIRED SUB-SKILL:** `superpowers:verification-before-completion`. Run the project-wide checks on the main feature
worktree (which holds the integration state — sub-worktrees only hold their own sub-branch):

```
cd <feature_worktree>
git pull origin feature/<slug>
make test
make lint
```

If any sub-PR in the feature touched `frontend/`, also:

```
cd frontend && npm run typecheck
```

Paste the output. The feature branch must be green end to end before the integration PR opens — a sub-PR's isolated
CI passing doesn't guarantee the integration compiles, since each sub-PR's tests ran against its own branch state,
not the post-merge state.

### 6. Synthesize the gap list

Produce a short summary for the orchestrator (and the user, if this is a pre-integration-PR or pre-ready checkpoint):

- **Drift found** — one bullet per discrepancy between spec/plan and what the diffs actually do.
- **Acceptance criteria uncovered** — one bullet per missing or rephrased criterion, with the classification from
  Step 3.
- **State-file fixes** — one bullet per row corrected.
- **Verification status** — `make test` / `make lint` / typecheck pass/fail.

Decide:

- **All clean** → open the integration PR (or flip ready) per `developing-a-feature`.
- **Coverable by a follow-up sub-PR** → re-enter `developing-a-feature` Step 4 and dispatch a follow-up subagent into
  a new sub-worktree. Update the state file with the new row.
- **Needs spec/plan refinement** → re-invoke `planning-a-feature` Steps 6/7 (write/update the plan, refine the
  issues), surface the changes to the user, then continue.

## Anti-patterns

- **Skipping the check at phase transitions.** Each wave's drift compounds; surfacing it at the boundary is the
  cheapest place to fix it.
- **Running verification on a sub-worktree instead of the main feature worktree.** Sub-worktrees only have their own
  sub-branch checked out. The feature branch — where the integration shows up — lives in the main feature worktree.
- **Marking the integration PR ready without re-running this checkpoint after external feedback.** Reviewer-requested
  changes can re-introduce drift the original review missed.
- **Treating the acceptance-criteria gap as a docs problem.** A missing criterion is either missing implementation or
  a planning oversight. Don't silently delete it; classify and act.

## Red flags

| Thought                                                            | Reality                                                                                                            |
| ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------ |
| "The sub-PRs all passed CI, the integration PR will too"           | Per-sub-PR CI runs against the sub-branch, not the feature branch. The integration is new state.                   |
| "The spec is old, no point checking against it"                    | Spec is the durable ADR. If it's stale, fix the spec — don't ignore it.                                            |
| "Acceptance criteria mismatch is fine, the spirit is the same"     | Acceptance criteria are checkable conditions. Either they're met or they're not. Update the issue if the criterion changed. |
| "I'll skip the state-file walk, I've been keeping it current"      | The resumed-session contract is what the state file SAYS — verify it; don't trust your memory.                     |
| "make lint passed in my sub-worktree, no need to re-run on feature" | Lint is per-package; cross-package issues only surface on the integrated branch.                                   |
