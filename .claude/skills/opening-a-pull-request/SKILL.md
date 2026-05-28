---
name: opening-a-pull-request
description:
  PR description shape for orchestrated work. Used at Phase 3 (draft PR for the per-repo plan) and Phase 5 (finalize
  before marking ready).
---

# opening-a-pull-request

## When to invoke

Two moments:

- **Phase 3**: each per-repo planning subagent opens a **draft PR** with the plan committed. The PR description gets the
  Phase 3 shape (below).
- **Phase 5**: the orchestrator (or a per-repo subagent) finalizes each draft PR before marking it ready. The PR
  description is updated to the Phase 5 shape.

## Templates

Two templates carry the shape and the per-section guidance:

- `templates/pull-request-draft.md`: Phase 3 draft PR body.
- `templates/pull-request-ready.md`: Phase 5 ready-for-review PR body.

Copy the appropriate template into a staging file (e.g. `/tmp/pr-body.md`), fill in the frontmatter and each section per
its `<!-- -->` guidance, then **submit** to strip + validate + stage in one step:

- Phase 3: `make submit-pr-draft SRC=/tmp/pr-body.md`
- Phase 5: `make submit-pr-ready SRC=/tmp/pr-body.md`

Each writes the cleaned body to `/tmp/pr-body.md.cleaned` only if validation passes. Pass that to
`gh pr create --body-file` (Phase 3) or `gh pr edit --body-file` (Phase 5).

The draft PR is the **stable surface** the implementation phase pushes to. It exists from Phase 3 onward and gives
humans early visibility into per-repo direction. At Phase 5, the body is rewritten from the ready template.

## PR title

Set the title once at Phase 3 (when `gh pr create` runs) in a shape that reads correctly through every phase, so
Phase 5 needs no rename:

```
<feature-id>: <repo>
```

The slug + repo name is enough for a human scanning sibling PRs to match them across repos. The PR's lifecycle state
(draft vs ready) is shown by GitHub's UI and stated explicitly in the body's `**Status:**` line; the title does not
need to encode it.

**Do not suffix the title with phase-specific wording** (`plan`, `draft`, `scaffolding`, `wip`, etc.). The PR carries
the plan through Phase 3, the impl through Phase 4, and human review through Phase 5; a single title that spans all
three states avoids a Phase-5 rename and avoids carrying stale Phase-3 wording into the merged record. If a Phase 3
draft slipped out with a phase suffix, rename it in the same orchestrator turn that flips the PR to ready.

## Steps (Phase 5 finalization)

1. **Rewrite the PR body from `templates/pull-request-ready.md`.** Fill in each section; leave the `<!-- -->` guidance
   in place. `make submit-pr-ready` strips comments + validates atomically (same pattern as the brief and spec submits).
   Do **not** hand-strip and skip the submit; you'll lose the validation pass. Note that the ready body carries **no
   `**Plan:**` line**: the plan is scaffolding for Phases 3–5, not part of the merged record. Step 5 below deletes it;
   this step removes the reference.
2. **Confirm sibling PR links.** All draft PRs from Phase 3 are now ready (or marked); the list should be complete.
3. **Confirm contract claims.** For each Contracts row in the spec that this PR touches, verify the implementation
   honors the documented shape. If it diverges, you have a Phase 6 ADR to write, and the divergence belongs in the PR
   description.
4. **Record merge order on the tracking issue.** Producer-before-consumer for any contract. The merge order goes into
   the tracking issue body so reviewers don't have to reason it out.
5. **Delete the per-repo plan from the feature branch.** The plan (`docs/superpowers/plans/<feature-id>.md` in the
   target repo) was the load-bearing artifact for Phase 3 review, Phase 4 implementation, and the Phase 5 reviewer pass.
   By the time you reach this step, all three have settled; keeping the plan committed past readiness pollutes the
   target repo with a planning artifact that will never be referenced again. Dispatch a one-shot cleanup subagent into
   that repo's worktree (on the feature branch) to run:

   ```
   git rm docs/superpowers/plans/<feature-id>.md
   git commit -m "chore(<feature-id>): remove plan, implementation complete"
   git push
   ```

   The orchestrator cannot edit subordinate repos directly; this is a dispatch, not an inline shell call. After the
   subagent returns, confirm the file is gone from the PR's head
   (`gh pr view <num> --json files --jq '.files[].path' | grep -v "plans/<feature-id>.md"`) before moving on.

6. **Mark the PR ready for review.** `gh pr ready <num>`.
7. **Update the state file:** rename `draft_prs` → `prs` if not already done. The schema's `draft_prs` and `prs` maps
   are mutually exclusive (`internal/state/state.go:148-151`; both being set is a validation failure), so the rename is
   the signal that the PR has crossed from "in flight" to "ready." Do **not** invent a `triage_notes` or any other key
   for readiness timestamps; unknown top-level fields are hard-rejected. If a timestamp matters, put it in the PR body
   or chat. `artifacts.p3_plans[<repo>]` keeps its path even though the file is now gone; the path is the historical
   pointer for the archive; git history (the add commit + the remove commit) is where the content lives.

## Anti-patterns

- **Phase-specific suffix in PR titles** (`<feature-id>: <repo> plan`, `... draft`, `... wip`, `... scaffolding`). The
  title outlives the phase that named it. By Phase 5 the PR holds the implementation and the planning artifact is gone,
  so a "plan"-suffixed title misleads reviewers and ships stale wording into the merged record. Use the title shape
  `<feature-id>: <repo>` from Phase 3 onward; the body's `**Status:**` line and GitHub's draft/ready chip carry the
  lifecycle state.
- **Different PR description shapes across sibling PRs.** Humans skim multiple PRs in a row at this phase; consistency
  is the kindness that gets the feature merged.
- **Marking ready before the contracts section is filled in.** The contract claims are what cross-repo reviewers check.
  Missing claims = wasted reviewer time.
- **No merge order.** "Just figure it out from the PR titles". No. Producer-before-consumer is recorded explicitly on
  the tracking issue.
- **Linking to the brief instead of the spec.** Reviewers want the spec; the brief is a Phase 1 artifact, not a reviewer
  surface.
- **Publishing a PR description with `<!-- -->` guidance comments still in it.** `make submit-pr-draft` /
  `make submit-pr-ready` strip them; using `gh pr create --body-file` against the raw scratch file (not the `.cleaned`
  output) is how this happens. Always pass the `.cleaned` file.
- **Flipping ready with the plan still committed on the feature branch.** The plan is Phase 3–5 scaffolding, not part of
  the merged record. Leaving it on the branch means a planning artifact ships to `main`, where it'll be stale within
  weeks and will pollute every `grep` against the target repo's docs. Run the step-5 cleanup dispatch before
  `gh pr ready`, every time.
- **Deleting the plan from the orchestrator session via `rm` or `git rm`.** The orchestrator does not edit subordinate
  repos directly: same rule as Phase 3 and Phase 4. The plan removal is a one-shot cleanup subagent dispatched into the
  target repo's worktree, even though the operation is tiny.
- **Deleting the plan before the Phase 5 reviewer fan-out finishes.** The reviewers' job is to compare
  impl-vs-plan-vs-contract; they need the plan present on the branch. Removal happens _after_ triage settles and
  _before_ flipping ready, not earlier.

## Red flags: STOP before flipping ready

These thoughts mean the PR isn't actually ready to flip from draft:

| Thought                                                                                              | Reality                                                                                                                                                                                                                                                                        |
| ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| "The draft description is fine, no need to rewrite from the ready template"                          | Different shape, different audience. Draft is for "review the plan"; ready is for "review the impl + contract claims." Rewrite from `templates/pull-request-ready.md`.                                                                                                         |
| "Phase 5 reviewers haven't flagged anything yet, I can flip now"                                     | Flipping before the orchestrator-side reviewer pass _settles_ signals "ready for human review" while internal review is still in flight. Wait for explicit triage signoff per `multi-repo-feature-development.md` Phase 5 step 3.                                              |
| "I'll flip this one now and siblings catch up later"                                                 | Reviewers expect sibling PRs to be available together; partial readiness leaks merge order before it's recorded on the tracking issue. Flip all involved repos in the same orchestrator turn (or hold).                                                                        |
| "The contracts section has no changes since draft, skip it"                                          | The contracts section in the ready body is a _claim_: this PR honors the contracts it touches. Leaving it blank silently drops the claim, which is what cross-repo reviewers were going to check.                                                                              |
| "Marking ready now, will fix the body in a follow-up edit"                                           | The body is what the reviewer reads in the first 10 seconds. Fix it first, then `gh pr ready`.                                                                                                                                                                                 |
| "draft_prs is still set in the state file, but the PR is ready, I'll fix the state on the next gate" | The schema (`state.go:148-151`) rejects `draft_prs` and `prs` being set simultaneously. Rename in the same orchestrator turn that runs `gh pr ready`, or `make validate-state` fails.                                                                                          |
| "The plan file is harmless on the branch, I'll leave it for Phase 6 ADR extraction"                  | Phase 6 reads the **merged diff** and the spec, not the plan. The plan is Phase 3–5 scaffolding; carrying it past readiness pollutes the target repo with stale docs that future `grep`s have to wade through. Delete it via the step-5 cleanup dispatch before `gh pr ready`. |
| "I'll `git rm` the plan from this session, faster than dispatching a subagent for one file"          | The orchestrator does not edit subordinate repos directly. Same rule for one-file deletes as for full implementations: dispatch a worker subagent into the target repo's worktree. The one-file dispatch is cheap; the loophole it would open is expensive.                    |

All of these mean: do **not** run `gh pr ready` yet. Either rewrite the body, run the reviewer pass, wait for siblings,
or fix the state rename.
