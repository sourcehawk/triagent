package git

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type draftPRIn struct {
	IssueURL string `json:"issue_url" jsonschema:"the GH issue URL the PR will reference. Must be in this repo."`
	// ExtraPrompt is appended verbatim to the sub-agent prompt — see
	// schema for the intended-use rules.
	ExtraPrompt string `json:"extra_prompt,omitempty" jsonschema:"OPTIONAL — typically empty or 1-2 sentences. The sub-agent operates in a fresh worktree of THIS repo only; the worktree IS its sandbox. It cannot read code or files in any other repo, cannot run commands against other repos, cannot dereference 'verify this by reading X/Y/main.go' in a sibling. The sub-agent also reads the full issue body via 'gh issue view' — so DO NOT (a) restate the issue's Description / Acceptance Criteria / Evidence here (context waste; symptom that the issue itself is under-specified), (b) reference any file path / repo / commit outside this MCP's scoped repo, (c) embed cross-repo guardrails ('do not touch repo X') which are redundant since the sub-agent has no access regardless. Legitimate uses: scope narrowing within this repo ('only the BPMN parser, not DMN'); retry context ('prior draft_pr errored host-side, re-execute from issue body'); operator clarification that arrived after filing. If the change depends on knowledge from a sibling repo, file that prerequisite as a separate issue against the sibling's own triagent-git-<alias> MCP and link via cross_repo_refs — do not paper over the dependency in extra_prompt."`
	BaseRef     string `json:"base_ref,omitempty" jsonschema:"git ref the PR targets (default 'main')"`
}

type draftPROut struct {
	// ProposalID is minted up front so the launcher can persist a
	// CodefixProposalPayload keyed by it (and so the CodefixProposalCard
	// can render even before pr_state arrives). Kept stable for the
	// lifetime of one draft_pr call — caller never receives partial
	// payloads without it.
	ProposalID  string `json:"proposal_id"`
	Repo        string `json:"repo"`
	IssueURL    string `json:"issue_url"`
	IssueNumber int    `json:"issue_number"`
	PRNumber    int    `json:"pr_number"`
	PRURL       string `json:"pr_url"`
	BranchName  string `json:"branch_name"`
	// ResolvedBaseRef is the actual ref the worktree was forked from and
	// against which the post-run commit-list check measured. Surfaced so
	// the parent agent can tell when its input ("main") was rewritten
	// internally to "origin/main" for currency.
	ResolvedBaseRef     string               `json:"resolved_base_ref,omitempty"`
	Summary             string               `json:"summary"`
	Citations           []citations.Citation `json:"citations"`
	CitationsParseError string               `json:"citations_parse_error,omitempty"`
	PromptSent          string               `json:"prompt_sent"`
	TimedOut            bool                 `json:"timed_out,omitempty"`
}

// proposal IDs are now deterministic on (repo, issue_number) via
// codefixProposalID() in codefix_ids.go — kept identical between
// create_github_issue and draft_pr so a later draft_pr lands on the
// same file as the earlier create_github_issue.

const (
	defaultDraftPRTimeoutRequest = 30 * time.Minute
)

var thisRepoIssuePattern = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/issues/(\d+)$`)
var prURLPattern = regexp.MustCompile(`https://github\.com/[^/]+/[^/]+/pull/(\d+)`)

// statusMarkerPattern matches the sub-agent's narration markers.
// Kept in sync with mcp/internal/subagent/status_marker.go — the
// duplication is intentional (cross-package import isn't worth it
// for a tiny regex with no shared state).
var statusMarkerPattern = regexp.MustCompile(`<<<STATUS\s+message="[^"]*"\s*>>>`)

// prTitleMarkerPattern + prBodyMarkerPattern extract the structured
// PR title and body the sub-agent emits at the end of its prose. The
// markers exist precisely so the agent's conversational chatter ("I'll
// fix the typo...") doesn't leak into PR titles and bodies — we use
// what's between the markers, not the raw prose. Multi-line by design;
// the (?s) flag makes . match newlines.
var prTitleMarkerPattern = regexp.MustCompile(`(?s)<<<PR_TITLE\s*\n(.*?)\nPR_TITLE>>>`)
var prBodyMarkerPattern = regexp.MustCompile(`(?s)<<<PR_BODY\s*\n(.*?)\nPR_BODY>>>`)

func (s *Server) draftPR(ctx context.Context, _ *mcp.CallToolRequest, in draftPRIn) (*mcp.CallToolResult, draftPROut, error) {
	out := draftPROut{
		Repo:      s.repoFull(),
		IssueURL:  strings.TrimSpace(in.IssueURL),
		Citations: []citations.Citation{},
	}

	issueOwner, issueRepo, issueNum, ok := parseThisRepoIssueURL(in.IssueURL)
	if !ok {
		return errorResult("issue_url is required and must be a github.com URL like https://github.com/<owner>/<name>/issues/<n>"), out, nil
	}
	if issueOwner != s.owner || issueRepo != s.name {
		return errorResult(fmt.Sprintf("issue_url is for %s/%s but this MCP serves %s — call the correct triagent-git-<alias>", issueOwner, issueRepo, s.repoFull())), out, nil
	}
	out.IssueNumber = issueNum
	// Now that we have the issue number, mint the deterministic
	// proposal id. Matches what create_github_issue would mint for
	// the same (repo, issue_number) so a later draft_pr lands on
	// the same on-disk file (issue-only → issue+PR).
	out.ProposalID = codefixProposalID(out.Repo, issueNum)
	baseRef := in.BaseRef
	if baseRef == "" {
		baseRef = "main"
	}

	repoDir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), out, nil
	}
	// Resolve the bare baseRef (e.g. "main") against the cache clone's
	// remote-tracking refs so the worktree forks from origin/<base> and
	// the post-run commit-list check measures against the same ref. Local
	// branch refs are never advanced by `git fetch`, so basing on them
	// would mean working against potentially-stale state. baseRef itself
	// stays bare for `gh pr create --base` — GitHub names the remote
	// branch by bare name.
	resolvedBaseRef, err := s.resolveRef(ctx, repoDir, baseRef)
	if err != nil {
		return errorResult(fmt.Sprintf("resolve base ref %q: %v", baseRef, err)), out, nil
	}
	out.ResolvedBaseRef = resolvedBaseRef

	// Check for an existing worktree for this issue (left over from a
	// prior draft_pr call against the same issue — either a deliberate
	// "iterate on the open PR" loop or a bail that retained the
	// worktree). If found, reuse: same branch name, same path, seed
	// sessionID from the persisted file so the sub-agent --resume's
	// the prior conversation. Otherwise mint fresh.
	wt, branch, isReuse := s.findExistingWorktreeForIssue(ctx, repoDir, issueNum)
	if !isReuse {
		branch = newWorktreeBranch(issueNum)
		wt, err = s.createWorktree(ctx, repoDir, branch, resolvedBaseRef)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
	}
	out.BranchName = branch
	cleanupWorktree := func() {
		_ = s.removeWorktree(context.Background(), repoDir, wt)
	}

	timeout := defaultDraftPRTimeoutRequest
	if s.codefixTimeoutMinutes > 0 {
		timeout = time.Duration(s.codefixTimeoutMinutes) * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parentID := telemetry.CurrentToolID(ctx)
	prompt := buildDraftPRPrompt(s.repoFull(), in.IssueURL, issueNum, baseRef, in.ExtraPrompt)
	out.PromptSent = prompt

	// sessionID threads claude's conversation id across sub-agent calls.
	// Three sources, in order of precedence:
	//   1. Within-call: the closure captures the id from the first
	//      sub-agent return for the citation retry (and the recovery
	//      sub-agent below).
	//   2. Cross-call (reuse path): seeded from the persisted
	//      .triagent-session-id file in the existing worktree so the FIRST
	//      sub-agent invocation already resumes the prior turn's
	//      Claude session — the operator's "iterate" loop doesn't
	//      re-pay the cost of orientation in the worktree.
	//   3. Empty (default): fresh session.
	// On every sub-agent return that carries a session id, persist it
	// to the worktree for the next iteration.
	sessionID := readPersistedSessionID(wt)
	adapter := func(ctx context.Context, p string) (citations.RawResult, error) {
		res, err := s.runDraftPRSubAgent(ctx, wt, p, parentID, sessionID)
		if err != nil {
			return citations.RawResult{}, err
		}
		if res.SessionID != "" {
			sessionID = res.SessionID
			writePersistedSessionID(wt, sessionID)
		}
		return citations.RawResult{Raw: res.Summary, TimedOut: res.TimedOut}, nil
	}
	v := &gitValidator{repo: s.repoFull(), repoDir: wt, ctx: runCtx}
	// Reminder appended to any citation-correction retry: keep the
	// side-effect contract intact. Without this, the generic "fix the
	// citations" retry tends to make the agent re-emit prose only and
	// silently lose the commit + structured markers from the first turn.
	reminder := "Reminder: keep the side-effect contract from your first turn intact." +
		" You must have already run `git add` + `git commit` for your edits to land —" +
		" the host verifies a commit exists on the branch when this call returns." +
		" Keep the <<<PR_TITLE>>> and <<<PR_BODY>>> blocks present in your re-emit;" +
		" the host needs them for the PR title and body."
	crun, runErr := citations.Run(runCtx, citations.RunInput{
		Run: adapter, Validator: v, Prompt: prompt, RetryReminder: reminder,
	})
	// Extract the structured PR title + body the sub-agent emits in
	// <<<PR_TITLE>>> / <<<PR_BODY>>> markers — these are what we use
	// for the actual PR. The agent's raw prose is conversational
	// ("I'll fix the typo...") and shouldn't become a PR title.
	prTitleAuthored := sanitiseAgentProse(extractPRTitle(crun.Prose))
	prBodyAuthored := sanitiseAgentProse(extractPRBody(crun.Prose))

	// Strip ALL structured markers (STATUS, PR_TITLE, PR_BODY) from
	// the prose before anyone downstream reads it. The chat-side
	// CodefixProposalCard summary gets the cleaned natural prose
	// (the agent's one-line wrap-up); the structured fields are
	// already lifted out above. Em dashes are sanitised because the
	// summary surfaces in the GitHub UI when the agent reuses it on
	// the activity panel.
	cleanProse := sanitiseAgentProse(stripStructuredMarkers(crun.Prose))
	out.Summary = cleanProse
	out.Citations = crun.Citations
	out.CitationsParseError = crun.CitationsParseError
	out.TimedOut = crun.TimedOut

	if runErr != nil {
		cleanupWorktree()
		return errorResult(runErr.Error()), out, nil
	}

	// Verify at least one commit landed on the branch. Measure against the
	// resolved base (origin/<base>) so we count only sub-agent commits —
	// not commits the stale local base would surface.
	commitList, _ := gitOutput(ctx, wt, "log", resolvedBaseRef+".."+branch, "--oneline")
	if strings.TrimSpace(commitList) == "" {
		// No commits yet. Two sub-cases:
		//   (a) worktree clean — the sub-agent did nothing at all, bail.
		//   (b) worktree dirty — the sub-agent edited files but didn't
		//       commit OR cleanly abandon. Fire one focused recovery
		//       sub-agent to let it commit OR clean up, then re-verify.
		statusOut, _ := gitOutput(ctx, wt, "status", "--porcelain")
		if strings.TrimSpace(statusOut) == "" {
			cleanupWorktree()
			// If the sub-agent emitted a NEEDS_CLARIFICATION marker, this
			// is the "I'm blocked, please give me X and Y" path — surface
			// it as such so the operator knows what to provide on the
			// next iteration. Otherwise fall back to the rich generic
			// diagnostic that includes whatever signal the sub-agent did
			// emit (Summary / PR_TITLE / PR_BODY / citations error).
			if nc, ok := parseNeedsClarification(crun.Prose); ok {
				missing := strings.Join(nc.Missing, ", ")
				if missing == "" {
					missing = "(unspecified)"
				}
				return errorResult(fmt.Sprintf(
					"sub-agent needs clarification before it can proceed — missing: %s. Rationale: %s",
					missing, nc.Rationale,
				)), out, nil
			}
			return errorResult(
				"the sub-agent terminated without making any file changes — no PR was opened" +
					buildBailDiagnostic(out.Summary, prTitleAuthored, prBodyAuthored, out.CitationsParseError),
			), out, nil
		}
		// Recoverable: edits on disk, no commit. Re-fire scoped sub-agent.
		// Resume the main sub-agent's session so the recovery agent has
		// the prior turn's context (which files were edited, why, what
		// tests were run) instead of starting cold.
		recoveryPrompt := buildRecoveryPrompt(in.IssueURL, statusOut)
		recRes, recErr := s.runRecoverySubAgent(runCtx, wt, recoveryPrompt, parentID, sessionID)
		if recErr != nil {
			// Recovery transport failure with real sub-agent edits on
			// disk — preserve the worktree so the operator can salvage.
			// sweepStaleWorktrees reaps it after the cutoff if abandoned.
			return errorResult(fmt.Sprintf(
				"commit-or-cleanup recovery sub-agent failed: %v (uncommitted: %s; worktree retained at %s)%s",
				recErr, trimTo(statusOut, 400), wt, citationSuffix(out.CitationsParseError),
			)), out, nil
		}
		decision, _ := parseRecoveryDecision(recRes.Summary)
		// Trust-but-verify against the actual post-state.
		commitList, _ = gitOutput(ctx, wt, "log", resolvedBaseRef+".."+branch, "--oneline")
		statusOut2, _ := gitOutput(ctx, wt, "status", "--porcelain")
		committed := strings.TrimSpace(commitList) != ""
		clean := strings.TrimSpace(statusOut2) == ""
		switch {
		case committed:
			// Fall through to push + PR. Leftover dirty state, if any,
			// is the agent's problem — only committed content gets
			// pushed.
		case clean && decision.Action == "abandoned":
			cleanupWorktree()
			return errorResult(fmt.Sprintf(
				"sub-agent abandoned the codefix: %s — no PR was opened",
				decision.Reason,
			)), out, nil
		default:
			// Recovery left edits unresolved (still dirty, OR claimed
			// committed but didn't). Preserve the worktree so the
			// operator can inspect/salvage; sweepStaleWorktrees reaps
			// after the cutoff.
			diag := "sub-agent did not resolve the worktree after recovery retry"
			if !clean {
				diag += fmt.Sprintf(" (still dirty: %s)", trimTo(statusOut2, 400))
			}
			diag += fmt.Sprintf(" (worktree retained at %s)", wt)
			return errorResult(diag + citationSuffix(out.CitationsParseError)), out, nil
		}
	}

	// Citation envelope must parse + validate cleanly before we push or
	// open a PR. Reached here means commits landed (or recovery resolved
	// them) — the agent has work to ship. The previous order pushed
	// first and opened the PR before checking citations, leaving an
	// orphan PR + populated CitationsParseError in the same response;
	// the walker read citations:null as failure and retried draft_pr,
	// doubling the open PRs against the same issue. Bail now while
	// nothing has hit the remote. Worktree retained so the operator can
	// salvage the commit manually; the sweep reaps it after the cutoff.
	if out.CitationsParseError != "" {
		return errorResult(fmt.Sprintf(
			"citation envelope invalid after retry: %s — branch was NOT pushed and no PR was opened (worktree retained at %s)",
			out.CitationsParseError, wt,
		)), out, nil
	}

	// Push. On reuse, the branch already exists on origin from the prior
	// call's push — use --force-with-lease so the existing branch is
	// updated cleanly. The lease guards against clobbering work pushed
	// by someone else between iterations (rare for triagent-proposal/* but
	// possible if an operator manually committed). On a fresh call,
	// `-u` establishes upstream tracking as before.
	pushArgs := []string{"push"}
	if isReuse {
		pushArgs = append(pushArgs, "--force-with-lease", "origin", branch)
	} else {
		pushArgs = append(pushArgs, "-u", "origin", branch)
	}
	if err := runGit(ctx, wt, pushArgs...); err != nil {
		// Worktree retained on push failure for retry visibility.
		return errorResult(fmt.Sprintf("git push failed: %v", err)), out, nil
	}

	// On reuse, a PR for this branch may already exist (the prior call
	// opened it; this iteration is force-pushing updates). Detect with
	// `gh pr list --head <branch>` and skip create when found —
	// otherwise gh errors with "PR already exists". The remote diff
	// updates automatically from the force-push, so the existing PR
	// reflects the new state without further action.
	if isReuse {
		listArgs := []string{
			"pr", "list",
			"--repo", s.repoFull(),
			"--head", branch,
			"--state", "open",
			"--json", "url,number",
		}
		listOut, _, listErr := s.gh.Run(ctx, listArgs...)
		if listErr == nil {
			var existing []struct {
				URL    string `json:"url"`
				Number int    `json:"number"`
			}
			if jsonErr := json.Unmarshal(listOut, &existing); jsonErr == nil && len(existing) > 0 {
				out.PRURL = existing[0].URL
				out.PRNumber = existing[0].Number
				// Reuse path success — preserve the worktree so the
				// next iteration can find it (and the operator's
				// session id stays seeded). Sweep reaps after the
				// cutoff if the operator never returns.
				return nil, out, nil
			}
		}
	}

	// gh pr create — title and body come from the sub-agent's
	// structured markers when present; fall back to a sensible
	// default when missing (older agents, marker parse failure).
	title := fmt.Sprintf("triagent-proposal: fix #%d", issueNum)
	if prTitleAuthored != "" {
		title = "triagent-proposal: " + prTitleAuthored
	}
	bodyContent := prBodyAuthored
	if bodyContent == "" {
		// Fall back to the cleaned-prose first line so we still emit
		// something useful when the agent skipped the marker.
		bodyContent = firstLine(cleanProse)
	}
	body := fmt.Sprintf("%s\n\n---\n🤖 Drafted by triagent-proposal.\n", bodyContent)
	prArgs := []string{
		"pr", "create", "--draft",
		"--repo", s.repoFull(),
		"--base", baseRef,
		"--head", branch,
		"--title", title,
		"--body", body,
		// Mirror create_github_issue's label stamp so the codefix
		// activity panel + downstream filters see PRs and their
		// originating issues as one cohort. The launcher's
		// ensureCodefixLabels sweep seeds this label on every linked
		// repo at boot, so the flag is safe to pass unconditionally.
		"--label", triagentProposalLabel,
	}
	stdout, _, err := s.gh.Run(ctx, prArgs...)
	if err != nil {
		// One retry — gh sometimes flakes on first call after a push.
		var stderr []byte
		stdout, stderr, err = s.gh.Run(ctx, prArgs...)
		if err != nil {
			return errorResult(fmt.Sprintf("gh pr create failed (retried once): %v (%s) — branch %s exists on remote, file the PR manually", err, trimTo(string(stderr), 400), branch)), out, nil
		}
	}
	if m := prURLPattern.FindStringSubmatch(string(stdout)); len(m) >= 2 {
		out.PRURL = m[0]
		out.PRNumber, _ = strconv.Atoi(m[1])
	}

	// Cleanup decision on happy path:
	//   - fresh call: remove the worktree as before.
	//   - reuse call: preserve so the next iteration finds it (and the
	//     persisted session id stays seeded). sweepStaleWorktrees reaps
	//     after the cutoff if the operator never returns.
	if !isReuse {
		cleanupWorktree()
	}
	return nil, out, nil
}

func parseThisRepoIssueURL(u string) (owner, repo string, num int, ok bool) {
	m := thisRepoIssuePattern.FindStringSubmatch(strings.TrimSpace(u))
	if len(m) != 4 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return "", "", 0, false
	}
	return m[1], m[2], n, true
}

// stripStructuredMarkers removes every structured marker block from
// the sub-agent's prose: <<<STATUS>>> narration, <<<PR_TITLE>>>,
// <<<PR_BODY>>>. Each lives in its own out-of-band channel; STATUS
// gets surfaced as telemetry, PR_TITLE/PR_BODY become the actual PR
// title/body. The cleaned prose is what the CodefixProposalCard
// summary displays — natural agent prose, no marker noise.
func stripStructuredMarkers(s string) string {
	s = statusMarkerPattern.ReplaceAllString(s, "")
	s = prTitleMarkerPattern.ReplaceAllString(s, "")
	s = prBodyMarkerPattern.ReplaceAllString(s, "")
	// Collapse runs of 3+ consecutive newlines into 2, and trim.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// extractPRTitle reads the <<<PR_TITLE\n...\nPR_TITLE>>> marker and
// returns the trimmed content. Empty string when the marker is
// missing — caller falls back to a default title.
func extractPRTitle(s string) string {
	if m := prTitleMarkerPattern.FindStringSubmatch(s); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// extractPRBody reads the <<<PR_BODY\n...\nPR_BODY>>> marker.
// Same fallback shape as extractPRTitle.
func extractPRBody(s string) string {
	if m := prBodyMarkerPattern.FindStringSubmatch(s); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

