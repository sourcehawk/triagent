---
name: fanning-out-with-worktrees
description:
  Use when an orchestrator needs to dispatch parallel subagents into
  per-PR worktrees off a feature branch — typically invoked from
  developing-a-feature for multi-PR features.
---

# fanning-out-with-worktrees

## When to invoke

When you're the orchestrator for multi-PR feature work and these prerequisites are met:

- The `feature/<slug>` integration branch and the main feature worktree exist (set up by `developing-a-feature` before
  invoking this skill).
- The plan (`docs/superpowers/plans/<date>-<slug>-plan.md`) has a `## Contracts` section with a Realization strategy
  per row.
- The state file (`docs/superpowers/plans/<date>-<slug>-state.md`) has rows for each sub-issue.

Skip for single-PR features — there's no fan-out, just one branch off main.

## Why this exists

Parallel fan-out compounds in complexity quickly: contract handoff, isolation, wave dependencies, propagation, state
tracking, per-PR ripening. Wrapping the choreography in one skill keeps `developing-a-feature` focused on the
single-vs-multi decision and the integration-PR endgame, and gives any future use case (parallel migration work,
parallel refactor, cross-module sweep) a reusable orchestration entry point.

## Workflow

### 1. Plan the waves

Look at the plan's `## Contracts` table. Sub-PRs that are contract **producers** go in the FOUNDATIONAL wave; sub-PRs
that are contract **consumers** go in subsequent waves, keyed by when their contract is realized:

- `data-only` and `pre-merge stub PR` realizations → consumers can start as soon as the producer's stub PR has merged
  into the feature branch (or the data-only contract is documented).
- `stub-on-producer-branch` → consumers branch from the producer's branch (not the feature branch); dispatch is timed
  to after the producer's PR is open with the stub in place.

Sub-PRs with no contract dependency → all in one wave.

Record the wave assignments in the state file's `## Phases` section before dispatching.

### 2. Create sub-worktrees and dispatch wave N

For each sub-PR in the wave, the orchestrator creates the worktree first:

```
git worktree add .claude/worktrees/<slug>--<sub-name> -b <sub-branch> <base-ref>
```

`<base-ref>` is `feature/<slug>` for default sub-PRs, `feature/<slug>` after the stub PR merged (for `pre-merge stub
PR` consumers), or the producer's branch (for `stub-on-producer-branch` consumers).

Then dispatch one subagent per sub-PR. **REQUIRED SUB-SKILL:** `superpowers:dispatching-parallel-agents`.

Each dispatch prompt MUST include:

1. **Isolation verification as the first action.** `cd <worktree-path> && pwd && git branch --show-current` — the
   subagent confirms it's on the sub-branch in the right worktree before any edit. Commits land on the wrong branch
   otherwise.
2. **Context handoff.** State file path, plan path, spec path, the issue number it's working, and the relevant
   contract row(s) (Name + Producer + Consumer + Shape + Realization). The subagent implements **against the
   contract** — it does not re-discover or re-design it.
3. **Implementation skills.** `superpowers:test-driven-development` + `testing-a-feature` for every change.
4. **PR completion.** When the implementation is done and verified, the subagent invokes `opening-a-pull-request`
   with `--base feature/<slug>` and `Towards #<sub-issue>` in the body (NOT `Fixes` / `Closes` — those don't fire on
   non-default-branch merges; `Towards` is the explicit "keep this issue open until the orchestrator closes it
   manually" keyword). The subagent reports the PR URL back to the orchestrator.

### 3. Update the state file as subagents start work

As each subagent surfaces its worktree path and branch, the orchestrator fills in the row in the state file's
`## PRs / worktrees` table. When a subagent opens its draft PR, the orchestrator fills in the PR column with the
base ref (`#<num> → feature/<slug>`) and flips status to `draft`.

A stale row is worse than no row — a resumed session reads the state file as ground truth.

### 4. Watch loop

While subagents run, the orchestrator is the integration point — not an idle waiter. Watch for concerns that bubble
up from any subagent and propagate the resolution across every subagent the concern touches. Silent divergence is
the failure mode this watch loop exists to prevent.

Categories of concerns to watch for:

- **Contract drift.** A row in the `## Contracts` table needs to shift (reviewer feedback, an edge case the producer
  hit). Pause every affected consumer, update the plan's row, propagate.
- **Spec ambiguity surfaced mid-implementation.** A subagent hits a case the spec didn't cover. Surface to the user,
  get a decision, amend the spec (or add a note to the plan), and propagate to every subagent whose scope touches
  the same surface.
- **Discovered cross-PR dependency.** A subagent finds it needs a helper, type, or behaviour from another PR that the
  plan didn't enumerate. Decide whether the helper becomes a new contract (file an issue, add a contract row),
  inlines into the current PR, or is something one of the other subagents is already producing.
- **Test failure in shared infrastructure.** One subagent breaks a test that another subagent's PR relies on.
  Coordinate the fix into the right PR; don't let both subagents fix it independently.
- **External dependency change.** A Go module bump, a frontend lib update, an API shift — affects every running
  subagent.
- **Resource conflict.** Two subagents both editing the same file or symbol. Re-scope one to avoid the collision, or
  serialize the work.

How to propagate the resolution:

- **Subagent still running** → `SendMessage` with the subagent's id to push the resolution with full context. The
  subagent resumes with the update applied.
- **Subagent finished, PR still open** → re-dispatch a focused follow-up with the PR number and the specific change.
- **Subagent not yet dispatched (later wave)** → update its dispatch prompt's context block before launching.

Append a dated entry to the state file's `## Bubble-up log` (newest at top) naming the concern, the resolution, and
the propagation path used. The orchestrator owns propagation; a concern raised by one subagent and not propagated to
the others is how this whole pattern fails.

### 5. Per-sub-PR ripening — all orchestrator-driven

When a sub-PR is ready (subagent reports `ready` and the relevant verification commands pass), the **orchestrator**
takes over for the rest of the lifecycle. Every action below — review, merge, sub-issue close, state-file update —
is the orchestrator's, not the worktree subagent's. Three reasons this is the right division of labour:

- **Review independence.** The subagent that wrote the code is the wrong reviewer for the same code; the
  orchestrator's distance from the implementation is the whole point.
- **Global view.** Only the orchestrator holds the merge-order context (which contract rows are `locked`, which
  sibling PRs are still in flight, which wave we're in). A subagent merging on its own would commit to ordering it
  can't see.
- **Worktree topology.** Subagents live in their per-sub-PR worktrees; only the orchestrator's main feature worktree
  has `feature/<slug>` checked out, so the merge naturally happens on the orchestrator's side.

Per sub-PR, in order:

1. **Review.** **REQUIRED SUB-SKILL:** the `review` skill — invoke it against the sub-PR number to walk the diff with
   full PR context. This review is weaker than external review (which lands at the integration PR) but stronger than
   nothing; it catches issues that would otherwise pile onto the integration PR reviewer.
2. **Approval gate, per the state file's `sub_pr_approval` mode.** Every gate covers the **bundle**: merge + sub-issue
   close + state-file update. The close is bodyless (no `--comment` flag) — GitHub automatically cross-references
   the sub-issue from the merge commit via the sub-PR's body keyword, so no custom comment is needed and there's
   no "specific body about to land" for the close mutation.
   - **`autonomous`** (default) — proceed straight through the bundle in steps 3-5. The operator opted into the
     mechanical bundle (review → merge → bodyless close → state update) in `developing-a-feature` Step 2.
   - **`manual`** — pause and ask the operator for explicit approval before the bundle. The prompt MUST surface:
     a one-line summary of the review findings ("review clean" / "<N> findings, none blocking" / specific concerns),
     the PR's title and diff size, and a note that closing sub-issue `#<sub-issue>` follows the merge. Wait for an
     explicit yes. On push-back, route the concern back to the worktree subagent via `SendMessage` instead of
     merging.
3. **Merge into the feature branch.** `gh pr merge <num> --merge --repo sourcehawk/triagent` (or `--squash` /
   `--rebase` per project preference).
4. **Close the sub-issue.** `gh issue close <sub-issue> --repo sourcehawk/triagent`. Sub-PRs into a non-default
   branch don't trigger `Fixes`/`Closes` — manual close is the workaround. The body's `Towards #<sub-issue>`
   keyword left the issue open precisely so the orchestrator can close it here; the cross-reference from the merge
   commit (which references `#<sub-pr>`, which references `#<sub-issue>`) is preserved automatically without a
   custom comment.
5. **Update the state file.** Flip the row's status to `self-merged`. If the sub-PR was the realization of a
   contract (e.g. a pre-merge stub PR), flip the contract row's status to `locked` and fill in the `Realized in`
   pointer.

### 6. Checkpoint review, then dispatch wave N+1

When every sub-PR in wave N is `self-merged` AND every contract tied to wave N's producers is `locked`:

1. **REQUIRED SUB-SKILL:** `reviewing-feature-progress` — run the alignment checkpoint at the wave boundary. Drift
   accumulated across wave N is cheapest to fix here, not at the integration PR. If the checkpoint surfaces gaps,
   address them (follow-up sub-PR in wave N, plan/issue refinement, or both) before moving on.
2. Once the checkpoint is clean, dispatch wave N+1. Wave N+1's subagents pick up the realized contracts because the
   orchestrator updated the state file's `## Contracts` table.

Repeat Steps 2 → 6 for each wave.

### 7. All waves complete → hand back

When every wave is complete (every sub-issue closed, every row `self-merged`, every contract `locked`), update the
state file's frontmatter `status:` to `review` and return control to `developing-a-feature`. The next step is the
integration PR (`feature/<slug>` → `main` with `Closes #<epic>`) which `developing-a-feature` owns.

## Anti-patterns

- **Dispatching parallel agents without contracts.** "Two subagents on these two PRs" with no contract = divergent
  implementations that block at integration. If the plan didn't define a contract, don't dispatch — go back to
  `planning-a-feature` Step 6 and add one.
- **Skipping wave dependencies.** Consumers dispatched before producers' contracts are realized produce code against
  an imagined shape. Dispatch wave N+1 only after wave N is fully self-merged and contracts locked.
- **Self-merging without orchestrator review.** The integration PR is where external review lands, but sub-PRs still
  need a review pass before going into the feature branch. Skipping it dumps issues onto the integration PR reviewer.
- **Letting the worktree subagent review its own PR.** A subagent reviewing the code it just wrote has the same
  blind spots in review that it had in implementation. The orchestrator owns sub-PR review precisely because it
  didn't write the code.
- **Letting a bubble-up concern die in one subagent.** Propagation is the whole point of the watch loop.
- **Forgetting to manually close the sub-issue.** The keyword doesn't fire on non-default-branch merges; the issue
  page silently shows "open" even though the work shipped.
- **Letting the state file go stale.** A resumed session reads it as ground truth. Update every row as reality moves;
  commit the state-file diff per phase transition.

## Red flags

| Thought                                                              | Reality                                                                                                |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| "The plan says parallel but I don't see contracts — I'll just guess" | Plan is incomplete. Stop and define the contract, or sequence the work.                                |
| "I'll dispatch wave 2 now, wave 1 is almost done"                    | Almost-done producers haven't `locked` their contract rows. Wave 2 will diverge. Wait.                 |
| "The subagent will figure out the worktree on its own"               | Create the worktree yourself and embed `cd <path> && pwd && git branch --show-current` in the dispatch. |
| "Self-review feels redundant, the integration PR catches everything" | Integration PR reviewer can't catch every issue across 5 sub-PRs in one sitting. Catch them per PR.    |
| "The subagent already verified the diff, no need for the orchestrator to review again" | Verification (tests pass) is not review (does the diff express intent cleanly). Different signals. |
| "I'll close the sub-issue once the integration PR merges"            | Issues that read "open" while their work has shipped clutter the triage view. Close manually at self-merge. |
