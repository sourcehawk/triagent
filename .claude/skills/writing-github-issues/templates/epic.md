<!--
Epic body template. See
.claude/skills/writing-github-issues/SKILL.md for the choreography.

An epic is a parent issue that owns multiple feature-sized sub-issues.
File it after a brainstorming session whose outcome is "this spans
several feature-sized chunks". Sub-issues are filed separately (see
the feature template) and linked via GitHub's native sub-issue API.

Label: `epic`. Sub-issues carry their own labels (`feature`, `task`,
or `bug`) and link back to this issue as their parent.
-->

# <plain-english-title>

<!-- Title rule: a human-readable sentence a no-context reader can
parse. Example: "Run triagent against multiple deployment profiles in
one launcher". -->

## Problem

<!-- A few sentences. What capability is missing, what gap does this
close, and why does it matter now. No solution, no implementation
shape — those live in Design overview below. -->

## User stories

<!-- Optional. Bulleted user stories ("As <role>, I want <capability>,
so that <outcome>"). Include only when they sharpen the motivation
beyond what Problem already says; otherwise omit the whole section. -->

## In scope

<!-- Bullets naming concrete capabilities this epic delivers. Each
bullet is something that ends up as a sub-issue (feature/task) below. -->

## Out of scope

<!-- Bullets naming explicit non-goals. Crucial for an epic: future
readers and reviewers use this to justify scope decisions on the
sub-issues. -->

## Risks & mitigations

<!-- Bullets pairing risks to the chosen direction with how we plan
to mitigate. One per line, lead with the biggest. Examples: data
migrations, performance regressions, API-contract breaks, scope creep. -->

## Design overview

<!-- A few short paragraphs (or bullets) sketching the high-level
implementation direction surfaced by the brainstorm. NOT a detailed
spec — but keep it SELF-CONTAINED: capture what a reviewer needs
inline. Do NOT link the spec or plan file (`docs/superpowers/specs/...`,
`docs/superpowers/plans/...`) — issues are durable, those files move
and get deleted; the spec is referenced from the plan, not the issue.
Reviewers should be able to read this and understand the seams the
sub-issues will sit on. -->

## Sub-issues

<!-- Linked via GitHub's native sub-issue API after the children are
filed (see SKILL.md "Linking sub-issues"). GitHub renders them as a
checklist with progress; leave this section as a single placeholder
until the children are filed and linked. -->

_Sub-issues will be linked below as they're filed._
