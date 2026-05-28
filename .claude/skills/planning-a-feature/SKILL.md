---
name: planning-a-feature
description:
  Use at feature conception — before writing any code, before filing
  any issue, before drafting any plan.
---

# planning-a-feature

## When to invoke

At the start of any feature larger than a one-off fix or refactor. If you're about to brainstorm, write a spec, file
a tracking issue, or write an implementation plan, this skill is the choreography that sequences those tools so the
artifacts land in the right order.

Skip for:
- One-off bug fixes (file a `bug` issue directly via `writing-github-issues`).
- Single-line refactors or test additions.
- Work that's already plan-shaped from someone else — jump to `developing-a-feature`.

## Why this exists

A senior engineering team's pre-implementation pipeline is **visible, fast, and parallelizable** — not a linear queue
of "brainstorm → spec → tickets → plan → code" run by one person in their head. Each step is explicit, hands off
between sub-skills cleanly, and surfaces the parallelism decision as a first-class output so implementation can fan
out instead of dragging.

## Workflow

### 1. Brainstorm → spec

**REQUIRED SUB-SKILL:** Invoke `superpowers:brainstorming` to nail down intent, scope, and design choices before any
artifact is written.

Output: a spec at `docs/superpowers/specs/YYYY-MM-DD-<slug>-design.md`. The spec is a durable ADR — WHAT we're
building and WHY, with goals, non-goals, design choices, risks, alternatives considered. It does **not** enumerate
PRs, commits, or parallelism contracts (those live in the plan, see step 5).

Slug is short and descriptive ("multi-tenant-profiles", not "implement-multi-tenant-profile-system"). Date is the
conception date.

### 2. User reviews the spec

Pause. Surface the spec path and wait for explicit "approved" or redirection before moving on. A spec the user hasn't
read is a draft, and drafts don't get tickets filed against them.

### 3. PR-shape decision

Decide whether the work ships as:

- **One PR** — single self-contained change, one reviewer pass, one merge to main.
- **Multiple PRs** — multiple feature-sized chunks, each independently reviewable, possibly parallelizable. Multi-PR
  features land via the **feature-branch model**: a long-lived `feature/<slug>` branch off main; every sub-PR is a
  real GitHub PR targeting `feature/<slug>` (not main); when every sub-PR has been self-merged into the feature
  branch, a final **integration PR** from `feature/<slug>` to main collects the whole feature for external review.
  Main stays shippable throughout the work; each sub-PR retains full GitHub visibility (comments, reviews, history).

The PR-shape judgment is grounded in **reviewer cost**: a 2000-line PR is unreviewable even if the work is "one
thing". If you can name two independent surfaces that ship value separately, that's two PRs and the feature-branch
model applies.

The decision is noted in chat (or as a one-line `## Implementation breakdown` paragraph in the spec naming the PRs —
NOT the contracts between them). The spec stays a clean ADR otherwise.

### 4. File the tracking issue(s)

**REQUIRED SUB-SKILL:** Invoke `writing-github-issues` with the PR-shape decision in hand.

- Single PR → file one `feature` (or `bug`) issue.
- Multiple PRs → file an `epic` with one sub-issue per PR.

Issues link back to the spec path in their `## Context` section. Don't link the plan yet — it doesn't exist.

### 5. Write the plan (and discover contracts)

**REQUIRED SUB-SKILL:** Invoke `superpowers:writing-plans` to produce
`docs/superpowers/plans/YYYY-MM-DD-<slug>-plan.md`. The plan is HOW: ordered tasks, dependencies, review checkpoints,
PR-by-PR breakdown.

**During plan-writing, identify parallelism and define contracts.** For every pair of PRs that could run in parallel,
ask: what does each side need to know about the other's wire shape, API surface, or data layout to start work
without blocking? That's a contract. Document each in a `## Contracts` section of the plan:

| Name                       | Producer (issue) | Consumer (issue) | Shape                                                 | Realization              |
| -------------------------- | ---------------- | ---------------- | ----------------------------------------------------- | ------------------------ |
| `profile-storage-layout`   | #22              | #24, #25         | path `~/.config/triagent/<profile>/sessions/<id>/...` | data-only                |
| `profile-loader-signature` | #22              | #24, #25         | `profile.Resolve(name string) (*Profile, error)`      | pre-merge stub PR (#21)  |

Each contract names the wire / signature / layout the consumer can write code against today, without waiting for the
producer's implementation. "TBD" is not a contract — block on it until it's concrete.

**Realization strategy.** A contract row is conceptual; for parallel work to actually compile, the interface has to
exist as code or as data before either side starts. Pick one per row and put it in the Realization column:

- **Pre-merge stub PR** — file a tiny scaffold PR that exports the symbol the consumers need (Go interface or
  function signature with `panic("unimplemented")` body, TypeScript type, HTTP path constant). It targets the
  feature branch (in multi-PR features) or main (in single-PR features) and merges BEFORE the implementation PRs
  branch off. Producer and consumers then all branch from the post-stub state and import the real symbol. Best
  default for code-shaped contracts (Go signatures, TS types); costs one trivial extra PR; payoff is both sides
  compile from day one. Reference the stub PR's number in the Realization column once it's open.
- **Stub-on-producer-branch** — the producer's own PR opens with just the interface + panic-bodies; consumers branch
  from the producer's branch (not main) and rebase as the producer fills in the body. Avoids the extra PR but
  couples consumers to the producer's branch lifetime — rebase pain when the interface evolves. Use only when the
  interface and one implementation are inherently coupled (shared unexported package, sibling files in one module).
- **Data-only** — when the contract is a path layout, file format, wire protocol, or env-var name, no code stub is
  needed. Consumers write against the contract row directly — strings and paths are just strings and paths. Mark
  the row `data-only`.

Sequential work doesn't need contracts. Only document them where two workers genuinely run in parallel.

### 6. Refine issues if planning surfaced changes

Planning often surfaces scope changes. If the issues from step 4 no longer match the plan's breakdown, update them
**now**, before implementation starts.

**REQUIRED SUB-SKILL:** Re-invoke `writing-github-issues` Step 2B (update) for any issue whose acceptance criteria,
scope, or dependencies shifted. When several issues need diff-sized edits at once (common at epic scale), batch the
confirmation: present every affected issue's gap list + proposed body in a single chat turn and wait for one "yes
to all" or per-issue redirection, instead of round-tripping the user N times.

### 7. Initialize the orchestration state file

Before handing off, create `docs/superpowers/plans/YYYY-MM-DD-<slug>-state.md` from
`templates/feature-state.md`. This is the **orchestration state** — phases, PRs, worktrees, contract realization,
bubble-up log. It's what a future Claude session reads first to resume the work without a massive user prompt.

The state file is scratch (same lifecycle as the plan): tracked in git so it survives sessions / worktrees /
machines, and deleted in the orchestrator's last commit once every sub-issue is closed and the feature has shipped.
Update it as the work progresses — see `developing-a-feature` for the update choreography.

Commit the spec, the plan, and the state file together as the planning artifact set.

### 8. Hand off to implementation

Spec + plan + state file committed, issues aligned, contracts written → invoke `developing-a-feature` to start the
work. The state file is the entry-point artifact for every session that touches this feature afterward.

## Anti-patterns

- **Putting PR breakdown or parallelism contracts in the spec.** Spec is a durable ADR. PR breakdown is short-lived
  scaffolding; contracts are implementation coordination. Both belong in the plan.
- **Filing issues before the spec is reviewed.** Wastes time when scope shifts during review.
- **Writing a plan before deciding PR shape.** The plan's structure depends on whether you're shipping one PR or many.
- **Declaring "we'll parallelize this" without writing the contract.** "Two workers in parallel" turns into "two
  workers blocked on each other after day one" without explicit shapes.
- **Treating "no parallelism possible" as a defeat.** Sequential work is fine. The cost of inventing fake parallelism
  exceeds the wall-clock saved.

## Red flags

| Thought                                                            | Reality                                                                                              |
| ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------- |
| "The spec already covers ordering, we don't need a plan"           | Spec is WHAT/WHY. Plan is HOW. Different artifacts, different lifecycles.                            |
| "I'll just write the plan and figure out parallelism later"        | Parallelism not in the plan never happens. Identify it during plan-writing.                          |
| "Filing issues is busywork, I'll just start coding"                | Without issues, the work isn't reviewable in chunks; the PR will be one giant blob.                  |
| "The contract is obvious, no need to write it down"                | Two parallel workers reading the same "obvious" thing produce divergent implementations. Write.      |
| "I'll skip user review on the spec, it's just an internal doc"     | An unreviewed spec is a draft. Drafts don't get tickets filed against them.                          |
