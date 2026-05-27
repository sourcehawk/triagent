package git

// Rule-based body shape directives for the two artifacts the git MCP
// agents file on GitHub: tracking issues (create_github_issue) and
// draft PRs (draft_pr's sub-agent). The shape rules live inline in the
// tool prompt surface — the issue shape is concatenated into the
// create_github_issue tool description so the calling agent sees the
// rubric before constructing body_markdown; the PR shape is
// concatenated into the draft_pr sub-agent prompt so the spawned
// claude sees the rubric before writing PR_BODY.
//
// Adapted from the section-shape conventions in
// camunda/controller-team-orchestrator's writing-github-issues and
// opening-a-pull-request skills, scaled down: no templates, no scripts,
// no per-section guidance comments. Lead with the section list, follow
// with rules. The orchestrator's multi-repo scaffolding (Tracking,
// Repos involved, Merge order, Contracts honored) is dropped — each
// triagent-git-<alias> MCP is scoped to a single repo, so the
// cross-repo coordination layer is unnecessary.

// issueBodyShape is the section layout + rules the create_github_issue
// caller follows when building body_markdown.
const issueBodyShape = `BODY SHAPE — investigation-filed issue

## Description
2-3 sentences a no-context reader can parse. Elevator pitch — what is this issue about? No motivation, no deliverable.

## Why
The user or operator problem this fixes. Why a reviewer should care. No solution; no design.

## What & Impact
Bulleted user-visible behavior the change should produce, biggest first.
Close with one ` + "`Impact:`" + ` line naming who notices and how (which team, operator, downstream system). The Impact line is what makes the section checkable at done-time; without it, "What" is aspirational.

## Evidence
Citations from the investigation, one bullet per claim. Inline links to commits, log excerpts, runbook pages, dashboards. Evidence grounds the finding — it is not motivation.

## Out of scope
Optional. Explicit non-goals, one bullet each. Skip the section entirely when nothing tempting needs to be ruled out; do not write "N/A".

Rules:
- Plain-English title; a human-readable sentence, not a slug. Reviewers match issues across repos by eye.
- No approach / design / code paragraphs in the body. Implementation belongs in the PR description.
- "What" without an Impact closer is aspirational. Always name who notices.
- Don't restate Evidence under Why — Evidence grounds the finding; Why is the user problem.
- Don't paste the full investigation transcript; Evidence links to the artifacts a reviewer can pull on.
`

// prBodyShape is the section layout + rules the draft_pr sub-agent
// follows when writing PR_BODY. The host appends "Fixes #N" and the
// 🤖 trailer; the agent's body covers only the sections below.
const prBodyShape = `BODY SHAPE — draft PR

## Description
2-4 sentences accessible to a reviewer without context. What this PR achieves and why now. Lean on the linked issue for the deeper "why"; lean on Changes for the "what specifically".

## Changes
Over-arching behavior changes, bulleted, biggest first. Do not list file-level renames or move-arounds unless they are genuinely the headline change.

## Testing
Freeform prose. What was tested, how, anything reviewers should poke at themselves. Describe what gives you confidence this is shippable. No checklists.

## Challenges
Optional. A hard problem the diff hides and how it was solved — the kind of thing a reviewer would otherwise have to reverse-engineer. Skip the section entirely when there is no story; do not write "N/A".

Rules:
- Don't restate the issue body verbatim — the reviewer has read it.
- Don't include ` + "`Fixes #N`" + ` (the host adds it).
- Don't include the 🤖 trailer (the host adds it).
- Resist adding sections beyond these without a real reason. Shape consistency across PRs is the kindness that gets work merged.
`
