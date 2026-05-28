<!--
Ready-for-review PR description template. See
.claude/skills/opening-a-pull-request/SKILL.md for the choreography.

This replaces the draft body when marking the PR ready (or is the
opening body for PRs that go straight to ready).
-->

## Description

<!--
Open with ONE of these as the FIRST line of this section, depending
on what should happen to the linked issue on merge:
  - `Fixes #<num>` — bug-fix issue; GitHub auto-closes on merge to main.
  - `Closes #<num>` — feature/task issue; same auto-close semantics,
    neutral phrasing.
  - `Towards #<num>` — the PR contributes to the issue but does NOT
    auto-close it; the issue stays open. Used for sub-PRs into a
    feature branch (where the orchestrator closes the sub-issue
    manually after self-merge) and for any other "in progress on
    this, not finishing it" case.
Omit the line entirely if there is no tracking issue.

Then follow with a short, human-readable summary accessible to a dev
coming in without context. Not overly technical. Two-to-four
sentences answering: what does this PR achieve, and why now. Lean on
Changes (below) for the "what specifically".
-->

## Changes

<!--
Over-arching changes that affect behavior or user-visible surface.
Don't list "renamed foo to bar" or file-level diffs unless they are
genuinely the headline change. Bullets, one line each, lead with the
biggest.
-->

## Challenges

<!--
If relevant. Briefly explain a hard problem this PR ran into and how
it was solved — the kind of thing a reviewer would otherwise have to
reverse-engineer from the diff. Skip the section entirely if there is
no story to tell here.
-->

## Related

<!--
Any directly-related PRs or issues OTHER than the tracking issue
(which is already linked via Fixes/Closes in the Description). Skip
the section entirely if there is nothing else to link.
-->

## Testing

<!--
Freeform prose. What was tested, how, anything reviewers should poke
at themselves. Describe what gives you confidence this is shippable.
No checklists.
-->
