---
name: opening-a-pull-request
description:
  Use when about to call `gh pr create` or `gh pr edit` against
  sourcehawk/triagent.
---

# opening-a-pull-request

## When to invoke

Two moments:

- **Opening a draft PR** when work is still in flight and you want an early surface for reviewers to flag direction
  issues. Body follows the draft shape (below).
- **Flipping to ready, or opening a PR straight to ready**, when the work is done. Body follows the ready shape.

## Templates

Two templates carry the shape and the per-section guidance:

- `templates/pull-request-draft.md`: draft PR body.
- `templates/pull-request-ready.md`: ready-for-review PR body.

Copy the appropriate template, fill in each section per its `<!-- -->` guidance, then pass the body to
`gh pr create` (opening) or `gh pr edit` (flipping or editing) via a `--body "$(cat <<'EOF' ... EOF)"` heredoc.
GitHub doesn't render HTML comments, so leaving the template guidance in place is harmless — don't burn a step
removing it.

## PR title

Set the title once when opening and don't rename it. Match the project's commit-message convention from `CLAUDE.md`:

```
<type>(<area>): <imperative summary>
```

Types: `feat`, `fix`, `refactor`, `test`, `chore`, `docs`. Area mirrors the module path (`mcp/strategies`,
`internal/server`, `frontend`, `watches`). When the PR bundles unrelated areas, lead with the headline change and
acknowledge the others in the body — don't try to encode both in the title.

**Do not suffix the title with lifecycle wording** (`wip`, `draft`, `plan`, `scaffolding`, etc.). GitHub's draft / ready
chip carries the lifecycle state. A single title that survives from open through merge avoids renames and avoids
shipping stale wording into the merged record.

## Linking the tracking issue

When the PR has a tracking issue, link it as the **first line of the body's opening section** — `## What lands here`
for draft, `## Description` for ready. The exact keyword depends on **whether the PR should close the issue on
merge**:

- `Fixes #<num>` — bug-fix issue; GitHub auto-closes the issue when the PR merges to the default branch.
- `Closes #<num>` — feature/task issue; same auto-close semantics, neutral phrasing.
- `Towards #<num>` — the PR contributes to the issue but should NOT close it on merge. GitHub creates the back-link
  but doesn't auto-close. Use when the issue will be closed by a sibling PR, by a later PR, or by the orchestrator
  manually (see below).

Which keyword belongs depends on **which branch the PR targets**:

- **PR targets `main` (the default branch)** — use `Fixes` / `Closes` if the merge should close the issue;
  use `Towards` if the issue should stay open.
- **PR targets a feature branch** (`feature/<slug>` in the multi-PR feature-branch model — see `developing-a-feature`)
  — use `Towards #<sub-issue>`. `Fixes` / `Closes` keywords only auto-trigger on merges to the default branch, so
  writing them on a feature-branch-bound PR creates a misleading promise that nothing will fulfill. The sub-issue is
  closed manually by the orchestrator after the self-merge. The integration PR (feature → main) gets `Closes #<epic>`
  because that PR does merge to main.

If there is no tracking issue, drop the line entirely and open the section with prose.

Don't carry the issue link as a bold-line metadata header at the top of the body — the keyword form is what GitHub
uses to thread the cross-link and it reads as a natural opener to the implementation summary.

Other related PRs / issues (siblings, follow-ups, prior art) belong under `## Related` — that section is **only** for
links that aren't the tracking issue, since the tracking issue is already cross-linked via the keyword above.

## Core principle: user-in-the-loop for every GitHub mutation

Don't run `gh pr create` or `gh pr edit` without an explicit confirmation **for the specific body about to land**.
Generic intent earlier ("yes please open a PR") is not standing consent for the body now.

Every confirmation shows the user:

- The exact target (`sourcehawk/triagent`, or `#<num>` for edits).
- The full proposed body.

Wait for an explicit "yes" before any `gh pr` call. Treat absence of objection as a no.

## Steps when flipping a draft to ready

1. **Rewrite the body from `templates/pull-request-ready.md`.** The shapes are different — the draft asks "review the
   direction"; the ready asks "review the implementation." Don't ship the draft body forward unchanged.
2. **Confirm both mutations in one prompt, body inline.** Marking the PR ready is a separate GitHub mutation from
   editing the body, and the user-in-the-loop rule applies to both. Phrase the confirmation as: "About to update
   #<num>'s body to the version below AND flip it from draft to ready. Confirm?" — then paste the body. Wait for
   an explicit yes. Splitting confirmation across two prompts is fine; running `gh pr ready` on the strength of the
   body confirmation is not.
3. **Run `gh pr edit <num> --body "$(cat <<'EOF' … EOF)"` then `gh pr ready <num>`** once the user confirms.

## Anti-patterns

- **Lifecycle suffix in PR titles** (`... wip`, `... draft`, `... scaffolding`). The title outlives the state that
  named it. The body and GitHub's chip carry lifecycle; the title doesn't need to.
- **Flipping ready with the draft body unchanged.** Different shape, different audience. Rewrite from the ready
  template.
- **Marking ready before the Testing section is filled in.** That section is what gives the reviewer confidence the
  PR is shippable; leaving it blank silently drops the claim.
- **Running `gh pr create` / `gh pr edit` on inferred consent.** Every body is a fresh confirmation. The cost of
  pausing is low; the cost of an unwanted public mutation is high.

## Red flags: STOP before flipping ready or publishing

These thoughts mean the PR isn't actually ready to publish or flip:

| Thought                                                            | Reality                                                                                                                                |
| ------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------- |
| "The draft description is fine, no need to rewrite"                | Different shape, different audience. Rewrite from `templates/pull-request-ready.md`.                                                   |
| "Marking ready now, will fix the body in a follow-up edit"         | The body is what the reviewer reads in the first 10 seconds. Fix it first, then `gh pr ready`.                                         |
| "The user said yes a turn ago, this is the same thing"             | Bodies change between turns. Confirm the exact body about to land.                                                                     |
| "I'll just append a note and they can edit later if needed"        | They shouldn't have to clean up after the agent. Confirm first.                                                                        |

All of these mean: rewrite the body from the right template, paste it inline in chat, and wait for an explicit yes.
