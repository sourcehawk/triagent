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

### 1. Read the state file first, then plan + spec

The orchestration state file (`docs/superpowers/plans/<date>-<slug>-state.md`, created by `planning-a-feature` Step 7)
is the entry point — it points at the plan, the spec, the tracking issue, the open PRs, the worktrees, and any
bubble-up concerns logged so far. Read it in full before anything else; follow the file's "Resume checklist"
section to verify reality against the recorded state.

Then open the plan and spec it references. Note:

- The PR breakdown (one PR or multiple).
- The contract table in the plan's `## Contracts` section (if any).
- The dependency ordering — what must land first.
- Each contract's Realization strategy (pre-merge stub PR / stub-on-producer-branch / data-only).

If the plan is missing, stale, or the state file's recorded state doesn't match reality (a PR's actual status has
drifted from the row), STOP and reconcile — re-invoke `planning-a-feature` Step 6 if the plan needs to change, or
update the state file's rows to match reality before continuing.

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
   and the relevant contract row(s) from the plan's `## Contracts` section — **including each row's Realization
   strategy** so the subagent knows whether to branch from main (pre-merge stub already landed, data-only contract),
   from the producer's branch (stub-on-producer-branch), or block until the stub PR merges. The subagent implements
   **against the contract** — it does not re-discover or re-design it.
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

**Orchestrator watch loop.** While the parallel subagents run, the orchestrator is the integration point — not an
idle waiter. Watch for concerns that bubble up from any subagent and propagate the resolution across every subagent
the concern touches. Silent divergence is the failure mode this watch loop exists to prevent.

Categories of concerns to watch for:

- **Contract drift.** A row in the plan's `## Contracts` table needs to shift (reviewer feedback, an edge case the
  producer hit). Pause every affected consumer, update the plan's row, propagate.
- **Spec ambiguity surfaced mid-implementation.** A subagent hits a case the spec didn't cover. Surface to the user,
  get a decision, amend the spec (or add a note to the plan), and propagate to every subagent whose scope touches
  the same surface.
- **Discovered cross-PR dependency.** A subagent finds it needs a helper, type, or behaviour from another PR that the
  plan didn't enumerate. Decide whether the helper becomes a new contract (file an issue, add a contract row),
  inlines into the current PR, or is something one of the other subagents is already producing.
- **Test failure in shared infrastructure.** One subagent breaks a test that another subagent's PR relies on.
  Coordinate the fix into the right PR; don't let both subagents fix it independently and merge competing patches.
- **External dependency change.** A Go module bump, a frontend lib update, an API shift the orchestrator notices —
  affects every running subagent.
- **Resource conflict.** Two subagents both editing the same file or symbol. Re-scope one to avoid the collision, or
  serialize the work.

How to propagate the resolution:

- **Subagent still running** → use `SendMessage` with the subagent's id to push the resolution with full context.
  The subagent resumes with the update applied.
- **Subagent finished, PR still open** → re-dispatch a focused follow-up with the PR number and the specific change
  to apply.
- **Subagent not yet dispatched (later wave)** → update its dispatch prompt's context block before launching, so the
  next wave starts with the resolution in hand.

A concern raised by one subagent and not propagated to the others is how this whole pattern fails. The orchestrator
owns propagation.

**Keep the state file current.** The orchestrator updates `docs/superpowers/plans/<date>-<slug>-state.md` as
reality moves:

- When a subagent gets its worktree → fill in the branch + worktree path columns for its row.
- When a subagent opens a PR → fill in the PR number + status (`draft` / `ready`).
- When a contract's realization completes (stub PR merges, producer branch ships its stub) → flip the contract row's
  status to `locked` and fill in the `Realized in` pointer.
- When a bubble-up concern is raised and resolved → append a dated entry to the `## Bubble-up log` (newest at top)
  naming the concern, the resolution, and the propagation path used (`SendMessage`, re-dispatch, next-wave prompt).
- When a PR merges → flip the row's status to `merged`.
- When phase 1 completes and phase 2 dispatches → flip `status:` in the frontmatter (`foundational-wave` →
  `consumer-wave` → `review` → `merged`).

A stale state file is worse than no state file — a resumed session reads it as ground truth. Commit state-file
updates with each phase transition; don't let a session end with the file out of sync.

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

### 8. Tear down the planning artifacts

Once every sub-issue is closed and the feature has shipped (the epic auto-closes via the sub-issue progress bar),
delete the plan + state file in a final commit (`chore(<slug>): remove plan + state, feature shipped`). The spec
stays — it's the durable ADR. The plan and state file are scratch; leaving them committed past readiness pollutes
the repo with stale operational state that future `grep`s have to wade through.

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
