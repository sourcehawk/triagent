---
name: developing-a-feature
description:
  Use when starting implementation against a written plan and the
  tracking issue(s) from planning-a-feature, or when handed a plan
  by someone else.
---

# developing-a-feature

## When to invoke

When the plan from `planning-a-feature` (or equivalent) is committed and you're about to start writing code. Skip for
ad-hoc fixes — those go directly through `superpowers:test-driven-development` and `opening-a-pull-request`.

## Workflow

### 1. Read plan + spec

Open both `docs/superpowers/plans/<date>-<slug>-plan.md` and `docs/superpowers/specs/<date>-<slug>-design.md`. Note:

- The PR breakdown (one PR or multiple).
- The contract table in the plan's `## Contracts` section (if any).
- The dependency ordering — what must land first.

If the plan is missing or stale (spec evolved without the plan being updated), STOP and re-invoke
`planning-a-feature` Step 6. Don't implement against a stale plan.

### 2. Decide: sequential or parallel?

- **Sequential** (single PR, or PRs that strictly depend on each other in order) → one worktree, one Claude session,
  one PR at a time. Skip to Step 4.
- **Parallel** (multiple PRs the plan marks parallelizable, with contracts defined) → dispatch parallel subagents
  (Step 3).

### 3. Fan out (parallel work only)

**REQUIRED SUB-SKILL:** Invoke `superpowers:dispatching-parallel-agents` to dispatch one subagent per parallel PR.

Each subagent's dispatch prompt MUST include:

1. **Isolation as first step.** The subagent invokes `superpowers:using-git-worktrees` to create or enter a worktree
   on a dedicated branch. Embed `cd <worktree-path> && pwd && git branch --show-current` as the verification opener
   so the subagent confirms it's on the right branch before any edit (project-wide convention; commits land on the
   wrong branch otherwise).
2. **Context handoff.** The dispatch hands the subagent: the plan path, the spec path, the issue number it's working,
   and the relevant contract row(s) from the plan's `## Contracts` section. The subagent implements **against the
   contract** — it does not re-discover or re-design it.
3. **Implementation skills.** The subagent follows `superpowers:test-driven-development` + `testing-a-feature` for
   every change.
4. **Completion.** The subagent invokes `opening-a-pull-request` for its branch with `Fixes #<sub-issue>` in the
   PR body, then reports the PR URL + contract-row name(s) back to the orchestrator.

**Multi-wave fan-out.** When the plan has foundational PRs (contract producers) that consumers depend on, dispatch
in waves: producers first, then re-enter this step with the consumers once each producer's PR is open (not merged).
Consumers compile against the contract row, not the producer's branch — so a producer's PR being open is enough
signal that the contract shape is final. Don't dispatch a consumer wave before the producer wave's PRs surface; the
consumer would have nothing concrete to align against.

**Hold all merges until every wave's PRs are open.** The "land in dependency order" merge happens in Step 7, after
every parallel PR (across all waves) has surfaced. A producer merged while a consumer is mid-implementation forces
the consumer to compile against the merged shape — which may have drifted from the contract row if the producer
edited under review.

**Contract drift mid-fan-out.** If a contract row needs to shift after consumers have started (reviewer feedback, an
edge case the producer hit), pause every affected consumer, update the plan's `## Contracts` row, surface the diff
to each paused subagent (or kill+redispatch), then resume. Don't let a contract drift silently — the consumers will
silently diverge.

### 4. Implement (sequential work, or per-subagent)

**REQUIRED SUB-SKILL:** `superpowers:test-driven-development` for every code change. Tests first, watch them fail for
the right reason, then implement.

**REQUIRED SUB-SKILL:** `testing-a-feature` for the assertion shape — black-box against docstrings/contracts, not
implementation.

Per PR:
- Commits follow CLAUDE.md conventions: `<type>(<area>): <imperative summary> (#<sub-issue>)`.
- Run `make test` and `make lint` (and `cd frontend && npm run typecheck` if frontend touched) before claiming work
  is done.

### 5. Verify before completion

**REQUIRED SUB-SKILL:** `superpowers:verification-before-completion`. Forbids claiming "done" without evidence — run
the verification commands, paste the output, then make the claim. `make test` runs the whole tree because launcher
cross-package wiring breaks on edits that look local.

### 6. Open the PR

**REQUIRED SUB-SKILL:** `opening-a-pull-request` — draft or ready, body opens with `Fixes #<sub-issue>` so the issue
auto-closes on merge.

For epic-tracked work: the PR closes the sub-issue, not the epic. The epic stays open until every sub-issue closes;
GitHub's sub-issue progress bar tracks it without manual state.

### 7. Land in dependency order

When all parallel PRs are ready, merge in the order the plan defines (producers before consumers). The orchestrator
is responsible for the merge order; the subagents don't merge themselves.

## Anti-patterns

- **Fanning out without contracts.** "Two subagents on these two PRs" with no contract = divergent implementations
  that block at integration. If the plan didn't define a contract, don't dispatch in parallel — re-invoke
  `planning-a-feature` Step 5 and add one.
- **One subagent doing two parallel PRs.** The whole point of parallel dispatch is wall-clock savings; one agent
  serializes them. One subagent per PR.
- **Merging mid-fan-out.** Don't merge any PR while sibling parallel PRs are still in flight — the consumer must
  compile against the producer's final shape, not its mid-implementation shape.
- **Skipping `verification-before-completion` because "tests passed in my package".** `make test` runs the whole
  tree because launcher cross-package wiring breaks on edits that look local.

## Red flags

| Thought                                                              | Reality                                                                                                |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| "The plan says parallel but I don't see contracts — I'll just guess" | Plan is incomplete. Stop and define the contract, or sequence the work.                                |
| "I'll merge this one early, the others can rebase"                   | Forces every parallel consumer to rebase against your draft assumptions. Land in plan order.           |
| "The subagent will figure out the worktree on its own"               | Embed `cd <path> && pwd && git branch --show-current` in the dispatch or commits land on the wrong branch. |
| "Tests pass, I'll skip make lint"                                    | Lint is a CI gate. Running it locally is the cheapest place to catch the failure.                      |
