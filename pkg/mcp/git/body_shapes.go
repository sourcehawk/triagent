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
2-4 sentences a no-context reader can parse. What this issue is about AND the user or operator problem it addresses — why a reviewer should care. No solution, no design.

## Acceptance Criteria
Bulleted, testable conditions a reviewer can check off when the change lands. Each bullet is a concrete observable outcome — a behaviour, a metric value, a UI state, a log line that should/shouldn't appear. If a bullet has no way to be verified, it doesn't belong here. Biggest / most user-visible first.

## Evidence
Citations from the investigation, one bullet per claim. Inline links to commits, log excerpts, runbook pages, dashboards. Evidence grounds the finding — it is not motivation. When a specific snippet of code or config is the point, paste the relevant lines inline in a fenced block; a bare file path forces the reviewer to go fetch context the agent already has.

## Out of scope
Include ONLY when a reviewer would reasonably assume the issue covers something it doesn't — a tempting adjacent change, a sibling repo, a related symptom that's deliberately being left alone. There must be clear value in pre-empting that assumption. Skip the section entirely otherwise; do not write "N/A" and do not pad with non-goals nobody would have inferred.

Rules:
- Plain-English title; a human-readable sentence, not a slug. Reviewers match issues across repos by eye.
- No approach / design / code paragraphs in the body. Implementation belongs in the PR description.
- Don't restate Evidence in the Description — Evidence grounds the finding; the Description names the problem.
- Don't paste the full investigation transcript; Evidence links to the artifacts a reviewer can pull on.
- File paths and line ranges listed without their content are noise — they read as filler and bury the meaningful context. Link them in Evidence; when the specific lines carry the point, paste the snippet inline.
`

// prBodyShape is the section layout + rules the draft_pr sub-agent
// follows when writing PR_BODY. The host appends "Fixes #N" and the
// 🤖 trailer; the agent's body covers only the sections below.
const prBodyShape = `BODY SHAPE — draft PR

## Description
Open with ` + "`Fixes #<num>`" + ` (the issue number from the linked issue URL) so GitHub links the PR and auto-closes the issue on merge. Then 2-4 sentences in human-friendly prose explaining the implementation: what was actually changed and the shape of the approach taken. The linked issue carries the problem statement and the "why" — a reviewer has it open in the next tab. This section's job is to orient them on the implementation they're about to read, not to re-summarise the issue.

## Changes
Over-arching behaviour changes, bulleted, biggest first. Do not list file-level renames or move-arounds unless they are genuinely the headline change. When a specific code change deserves a callout, paste the relevant snippet inline in a fenced block rather than pointing at a path — the diff already lists the files; what the body adds is the human-readable highlight.

## Testing
Freeform prose. What was tested, how, anything reviewers should poke at themselves. Describe what gives you confidence this is shippable. No checklists.

## Challenges
Optional. A hard problem the diff hides and how it was solved — the kind of thing a reviewer would otherwise have to reverse-engineer. Skip the section entirely when there is no story; do not write "N/A".

Rules:
- The PR explains the implementation; the issue explains the problem. Don't restate the issue body — the reviewer has read it.
- The Description's first token must be ` + "`Fixes #<num>`" + ` — GitHub's auto-close linkage depends on it and the host does not add it.
- Don't include the 🤖 trailer (the host adds it).
- File paths and line ranges listed without their content are noise. The diff already enumerates them; when a piece of code deserves a callout, paste the snippet so the reviewer can read it in context.
- Resist adding sections beyond these without a real reason. Shape consistency across PRs is the kindness that gets work merged.
`
