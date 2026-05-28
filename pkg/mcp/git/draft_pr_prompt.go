package git

import (
	"fmt"
	"strings"
)

// buildDraftPRPrompt assembles the prompt for the draft_pr sub-agent.
// The sub-agent runs in a pre-created worktree on a fresh branch off
// baseRef. Skill use/exclude lists are explicit so the sub-agent's
// using-superpowers bootstrap doesn't pull in worktree management or
// branch-finishing skills (which the host already owns).
func buildDraftPRPrompt(repoFull, issueURL string, issueNum int, baseRef, extraPrompt string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `You are drafting a PR for the GitHub issue: %s
Repository: %s
Base ref: %s
Working directory: the current directory IS a fresh git worktree on a new branch off %s. Make your changes here; the host will push and open the draft PR after you commit.

`, issueURL, repoFull, baseRef, baseRef)

	sb.WriteString(`HARD RULES:
- Do not run ` + "`git push`" + `. The host owns push.
- Do not run ` + "`gh pr create`" + `. The host opens the draft PR after you commit.
- All edits must stay within this working directory.
- Make exactly one logical change set. Do not refactor unrelated code.

BLOCKED PATH:
If you cannot proceed because the issue is under-specified, contradicts what you find in the code, or depends on a fact you cannot verify from this worktree alone (e.g. "I need to know whether some external service exposes a specific API or metric" — sibling repos are out of reach), do NOT guess and do NOT commit a stub. Instead: leave the worktree clean (revert any partial edits) and emit a structured clarification request as the final block of your reply, in addition to the normal OUTPUT CONTRACT blocks below:

  <<<NEEDS_CLARIFICATION
  {"missing":["<one short phrase per missing fact>","..."],"rationale":"<one sentence: why you cannot decide without these>"}
  NEEDS_CLARIFICATION>>>

The host surfaces this verbatim to the operator so they know what to provide on the next iteration. Use this only when actually blocked; do not use it to defer a decision you can make from the issue body + worktree.

WORKFLOW:
1. ` + "`gh issue view " + issueURL + "`" + ` to read the issue body and understand scope.
2. Orient in the codebase with Read/Glob/Grep. Read ` + "`AGENTS.md`" + ` and ` + "`CLAUDE.md`" + ` at the repo root if present — those carry the project's authoritative house rules, dev commands, and any explicit pre-commit checks the maintainers want run. If they exist, they override the generic guidance below.
3. Discover the repo's CI gates BEFORE writing code. Inspect ` + "`.github/workflows/*.yml`" + ` (or ` + "`.gitlab-ci.yml`" + `, ` + "`.circleci/config.yml`" + `, etc.) plus the entry points they invoke (Makefile targets, ` + "`package.json`" + ` scripts, ` + "`pyproject.toml`" + ` / ` + "`tox.ini`" + `, ` + "`go test`" + ` invocations). The set of commands CI runs on PRs is your verification target — that's the bar to meet, not an arbitrary battery of checks.
4. TDD is mandatory for any code-bearing change. Invoke ` + "`superpowers:test-driven-development`" + ` — write a failing test first, watch it fail for the right reason, then implement, then re-run until green. Only skip TDD when there is literally no behavior to test (README typo, comment fix, asset rename); a missing local test framework is NOT a reason to skip — set one up or write the test in whatever form the repo already uses.
5. Pre-commit verification: invoke ` + "`superpowers:verification-before-completion`" + `, scoped to (a) the commands AGENTS.md/CLAUDE.md explicitly names, or (b) the CI gates you discovered in step 3. Do NOT preemptively bail on warnings, lint findings, or test failures that are unrelated to your change or that aren't enforced by CI — let CI surface those. If a CI-enforced check fails because of your change, fix it; if it fails on unrelated pre-existing breakage, document it in the PR body's Testing section and commit anyway. If the repo has no discoverable CI and no AGENTS.md guidance, run the most obvious test command for the stack (` + "`go test ./...`" + `, ` + "`npm test`" + `, ` + "`pytest`" + `) on the packages you touched and move on — don't invent additional gates.
6. Final pass: invoke ` + "`simplify`" + ` to review the diff for premature abstractions, dead code, or unused additions.
7. ` + "`git add <changed files>`" + ` then ` + "`git commit -m \"<concise subject>\"`" + `. Commit subject should match the repo's existing style (look at recent commit messages with ` + "`git log --oneline -10`" + `).

TEST FIXTURE HYGIENE:
When you write test fixtures (alert-rule tests, table tests, YAML test data, etc.), the identifiers you put in them are NOT inherited from the issue body's Evidence section. Evidence cites real production state — pod names like ` + "`crossplane-59fd5b956b-4b2lc`" + `, real IPs, real hostnames — because the issue body's job is to ground the finding for human reviewers. Test fixtures are different: they're synthetic data the test framework injects to exercise the rule / function / matcher. Copying a real production pod identifier into a test fixture leaks investigation details into the test suite and reads like noise to reviewers.

Use clearly-synthetic identifiers in fixtures: ` + "`pod-1`" + `, ` + "`test-pod-a`" + `, ` + "`example-deployment`" + `, ` + "`container-x`" + `. Pick names that obviously couldn't be production. If the test rule keys off labels (namespace, container, etc.) those still need to match what the production rule's matcher expects — it's the *identifier values* (pod name suffixes, hash blobs, UID-like strings) that should be synthetic. Same rule for IPs (use ` + "`192.0.2.x`" + ` / TEST-NET-1), hostnames (use ` + "`example.test`" + `), and any other production-shaped tokens.

PROGRESS NARRATION:
On every phase transition, emit a line on its own in the form:
  <<<STATUS message="one-line description">>>
Examples: 'reading job_timeout.go', 'writing failing test', 'running pytest', 'verifying clean lint', 'committing'. Don't narrate every keystroke — one line per phase boundary.

Do NOT invoke these skills even if they appear applicable:
- superpowers:using-git-worktrees — the host already created this worktree.
- superpowers:writing-plans / superpowers:executing-plans / superpowers:subagent-driven-development — these are for multi-session human-in-the-loop work, not for a one-shot draft PR.
- superpowers:brainstorming — the GH issue body defines the scope; you are executing on it, not exploring requirements.
- superpowers:finishing-a-development-branch / superpowers:requesting-code-review — the host owns push, PR creation, and review delegation.

`)

	if strings.TrimSpace(extraPrompt) != "" {
		fmt.Fprintf(&sb, "Additional scope refinement from the operator:\n%s\n\n", extraPrompt)
	}

	fmt.Fprintf(&sb, `OUTPUT CONTRACT:

You MUST emit three labelled blocks at the end of your reply, in this order: PR_TITLE, PR_BODY, then CITATIONS. The host parses them out to construct the actual GitHub PR. The natural prose you write outside the blocks is what the operator sees in the chat-side summary card — keep it to one sentence describing what your commit changes (under 30 words).

The PR title — single line, imperative mood, NO leading "triagent-proposal:" prefix (the host adds it). Under 70 chars. Describes the change, not your reasoning or your conversational framing. Good: `+"`Fix typo: rbase → rebase in README`"+`. Bad: `+"`I'll fix the typo on line 238`"+`.

<<<PR_TITLE
Fix typo: rbase → rebase in README
PR_TITLE>>>

The PR body — markdown, multi-line. Follow the body shape below. Keep it short for trivial changes; expand for substantive ones. The Description MUST open with `+"`Fixes #%d`"+` so GitHub links the PR back to the issue and auto-closes it on merge — the host no longer prepends this for you.

`+prBodyShape+`
<<<PR_BODY
## Description
Fixes #%d. The README intro carried a typo where "rbase" should read "rebase". One-character fix.

## Changes
- Corrected the misspelled "rbase" on line 238 of README.

## Testing
Visual check of the rendered README in GitHub preview; no code paths affected.
PR_BODY>>>

Citations — every concrete claim in your prose marked [N], with the matching entries here. Cite only artifacts in repo %s. github_file paths are validated against your worktree's HEAD. Use an empty array [] if you have nothing to cite (rare — at minimum, cite the file you edited).

<<<CITATIONS
[
  {"kind":"github_file","repo":"%s","path":"<relpath>","ref":"HEAD","line_start":<n>,"line_end":<m>},
  {"kind":"github_pr","repo":"%s","pr_num":<n>}
]
CITATIONS>>>`, issueNum, issueNum, repoFull, repoFull, repoFull)

	return sb.String()
}
