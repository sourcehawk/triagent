package server

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// codefixGhRunner is the small interface the codefix handlers use
// to invoke gh. Production wraps exec.CommandContext; tests inject
// stubs.
type codefixGhRunner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, stderr []byte, err error)
}

type realCodefixGhRunner struct{}

func (realCodefixGhRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	var errBuf []byte
	if ee, ok := err.(*exec.ExitError); ok {
		errBuf = ee.Stderr
	}
	return out, errBuf, err
}

var codefixProposalIDPattern = regexp.MustCompile(`^prop-[a-z0-9-]{6,64}$`)

// handleGetCodefixProposal returns the persisted payload joined with
// in-memory PR state. PR state is stubbed as "" until Task 13 wires
// the poller fan-out.
//
// GET /api/codefix-proposals/{id}
func (a *apiHandlers) handleGetCodefixProposal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !codefixProposalIDPattern.MatchString(id) {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	proposals := resolveCodefixProposalsPath(a.opts.CodefixProposalsPath)
	p, err := readCodefixProposal(proposals, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type response struct {
		CodefixProposalPayload
		PRState string `json:"pr_state,omitempty"`
	}
	state := a.lookupCodefixPRState(p.PRURL)
	resp := response{CodefixProposalPayload: p, PRState: state}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleDiscardCodefixProposal closes the PR and deletes the remote
// branch. Idempotent — second call returns 409.
//
// POST /api/codefix-proposals/{id}/discard
func (a *apiHandlers) handleDiscardCodefixProposal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !codefixProposalIDPattern.MatchString(id) {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	proposals := resolveCodefixProposalsPath(a.opts.CodefixProposalsPath)
	if _, ok, _ := readCodefixDiscardLedger(proposals, id); ok {
		http.Error(w, "already discarded", http.StatusConflict)
		return
	}
	p, err := readCodefixProposal(proposals, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	closeArgs := []string{
		"pr", "close", fmt.Sprintf("%d", p.PRNumber),
		"--repo", p.Repo,
		"--comment", "Discarded from c1 investigation.",
	}
	// gh sometimes errors if PR was already closed externally;
	// proceed to branch deletion anyway.
	_, _, _ = a.codefixGh.Run(ctx, closeArgs...)

	refPath := strings.TrimPrefix(p.BranchName, "refs/heads/")
	delArgs := []string{
		"api", "-X", "DELETE",
		fmt.Sprintf("repos/%s/git/refs/heads/%s", p.Repo, refPath),
	}
	// Best-effort. Continue to ledger write — the operator's
	// intent is recorded even if remote cleanup glitched.
	_, _, _ = a.codefixGh.Run(ctx, delArgs...)

	if err := writeCodefixDiscardLedger(proposals, id, codefixDiscardLedger{
		ProposalID:  id,
		DiscardedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// lookupCodefixPRState returns "" when state is not yet known.
// Task 13 wires the prStateCache map; for now the field is nil and
// this returns "" unconditionally.
func (a *apiHandlers) lookupCodefixPRState(prURL string) string {
	if a.prStateCache == nil {
		return ""
	}
	if info, ok := a.prStateCache[prURL]; ok {
		return info.State
	}
	return ""
}

// isDraftPRToolName matches the per-repo draft_pr tool's wire-format
// name. Each linked repo gets its own triagent-git-<alias> MCP server, so
// the full tool name varies (e.g. `mcp__triagent-git-zeebe__draft_pr`).
// Mirrors the frontend isDraftPrToolName helper in
// investigate/frontend/lib/events.ts.
func isDraftPRToolName(name string) bool {
	return strings.HasPrefix(name, "mcp__triagent-git-") && strings.HasSuffix(name, "__draft_pr")
}

// isCreateGithubIssueToolName matches the per-repo issue-creation
// tool (e.g. `mcp__triagent-git-zeebe__create_github_issue`). The
// launcher persists every successful call so the repos sidenav
// surfaces standalone issues, not just issue+PR pairs.
func isCreateGithubIssueToolName(name string) bool {
	return strings.HasPrefix(name, "mcp__triagent-git-") && strings.HasSuffix(name, "__create_github_issue")
}

// persistCodefixProposal parses the draft_pr tool result body and
// writes a CodefixProposalPayload keyed by proposal_id. Best-effort:
// silently no-ops when the body can't be parsed (early validation
// path inside the tool, malformed JSON, etc.). Merges with any
// existing on-disk entry so a draft_pr that follows a prior
// create_github_issue upgrades the row in place rather than
// creating a duplicate. Also primes the codefixRepos tracking set
// so the PR-state poller picks up the repo on its next sweep.
func (a *apiHandlers) persistCodefixProposal(investigationID, resultJSON string) {
	r, ok := parseDraftPRToolResult(resultJSON)
	if !ok {
		return
	}
	id := r.ProposalID
	if !codefixProposalIDPattern.MatchString(id) {
		// Old MCPs predate the proposal_id field. Mint the same
		// deterministic id the MCPs now use so a future call to
		// the same issue lands on this file.
		id = deterministicCodefixProposalID(r.Repo, r.IssueNumber, r.PRNumber)
	}
	proposals := resolveCodefixProposalsPath(a.opts.CodefixProposalsPath)
	merged := mergeCodefixProposal(proposals, CodefixProposalPayload{
		ProposalID:      id,
		Repo:            r.Repo,
		IssueURL:        r.IssueURL,
		IssueNumber:     r.IssueNumber,
		PRURL:           r.PRURL,
		PRNumber:        r.PRNumber,
		BranchName:      r.BranchName,
		Summary:         r.Summary,
		InvestigationID: investigationID,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	})
	if err := writeCodefixProposal(proposals, merged); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: persist codefix proposal %s: %v\n", merged.ProposalID, err)
		return
	}
	a.trackCodefixRepo(merged.Repo)
}

// persistCodefixIssue is the create_github_issue end-event hook.
// Writes an issue-only proposal (empty PR fields) keyed on the same
// deterministic id format draft_pr uses, so a later draft_pr for
// the same issue merges in place.
func (a *apiHandlers) persistCodefixIssue(investigationID, resultJSON string) {
	r, ok := parseCreateIssueToolResult(resultJSON)
	if !ok {
		return
	}
	id := r.ProposalID
	if !codefixProposalIDPattern.MatchString(id) {
		id = deterministicCodefixProposalID(r.Repo, r.Number, 0)
	}
	proposals := resolveCodefixProposalsPath(a.opts.CodefixProposalsPath)
	merged := mergeCodefixProposal(proposals, CodefixProposalPayload{
		ProposalID:      id,
		Repo:            r.Repo,
		IssueURL:        r.URL,
		IssueNumber:     r.Number,
		InvestigationID: investigationID,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	})
	if err := writeCodefixProposal(proposals, merged); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: persist codefix issue %s: %v\n", merged.ProposalID, err)
		return
	}
	a.trackCodefixRepo(merged.Repo)
}

// deterministicCodefixProposalID mirrors the mcp/internal/git
// codefixProposalID helper. Keyed on (repo, issue_number) so
// create_github_issue and draft_pr land on the same on-disk file.
// Falls back to pr_number when issue_number is unknown (older
// draft_pr results that omit it) so backfill still produces a
// stable id.
func deterministicCodefixProposalID(repo string, issueNumber, prNumber int) string {
	key := issueNumber
	prefix := "i"
	if key == 0 {
		key = prNumber
		prefix = "pr"
	}
	safe := strings.ReplaceAll(strings.ToLower(repo), "/", "-")
	var b strings.Builder
	for _, r := range safe {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	id := fmt.Sprintf("prop-%s-%s%d", b.String(), prefix, key)
	if len(id) <= 64 {
		return id
	}
	// Pathological repo names: fall back to a short hash. Stays
	// deterministic so dedup still works.
	h := sha1.Sum([]byte(fmt.Sprintf("%s/%s%d", repo, prefix, key)))
	return fmt.Sprintf("prop-h%x-%s%d", h[:8], prefix, key)
}

// handleListCodefixProposals returns every persisted codefix
// proposal that has not been discarded. The repos page's sidenav
// reads this to render its activity panel. Joins on PR state from
// the in-memory poller cache so the UI can colour each row.
//
// GET /api/codefix-proposals
// Optional query: ?repo=owner/name filters to a single repo.
func (a *apiHandlers) handleListCodefixProposals(w http.ResponseWriter, r *http.Request) {
	repoFilter := strings.TrimSpace(r.URL.Query().Get("repo"))
	proposals := resolveCodefixProposalsPath(a.opts.CodefixProposalsPath)

	// Lazy backfill: walk every investigation's events.jsonl for
	// draft_pr tool_results that have not yet been persisted. Keeps
	// the panel useful after a `git pull` of shared upstream
	// investigations whose proposals were never written on this
	// host. Cheap when there's nothing new — we skip the parse for
	// any tool_id we already have on disk.
	if a.manager != nil {
		a.backfillCodefixProposalsLocked(proposals)
	}

	list, err := listCodefixProposals(proposals)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type wire struct {
		CodefixProposalPayload
		PRState string `json:"pr_state,omitempty"`
	}
	out := make([]wire, 0, len(list))
	for _, p := range list {
		if repoFilter != "" && !strings.EqualFold(p.Repo, repoFilter) {
			continue
		}
		out = append(out, wire{
			CodefixProposalPayload: p,
			PRState:                a.lookupCodefixPRState(p.PRURL),
		})
	}
	// Newest first, then by repo/pr_number as a stable tiebreaker.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].PRNumber > out[j].PRNumber
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"proposals": out})
}

// backfillCodefixProposalsLocked scans every investigation's
// events.jsonl for draft_pr and create_github_issue tool_results
// and persists any activity that isn't already on disk. Merges via
// mergeCodefixProposal so a transcript with both a
// create_github_issue and a later draft_pr for the same issue
// produces a single combined row. Best-effort: callers ignore
// errors.
func (a *apiHandlers) backfillCodefixProposalsLocked(proposalsDir string) {
	for _, dto := range a.manager.List() {
		// dto.SessionDir is the actual on-disk path; joining
		// sessionsRoot + dto.ID does NOT work in production because
		// session dirs are named with a timestamp slug
		// (session-YYYYMMDD-HHMMSS) while dto.ID is a random hex.
		// The two never align, so the old form silently skipped
		// every restored session — see TestBackfillCodefixProposals_UsesSessionDirNotID.
		if dto.SessionDir == "" {
			continue
		}
		events, err := loadEvents(dto.SessionDir)
		if err != nil {
			continue
		}
		// Pair tool_use → tool_result by ToolID. The wire order is
		// tool_use before tool_result for a given id; we walk once.
		useByID := make(map[string]EventEnvelope)
		for _, ev := range events {
			switch ev.Kind {
			case envKindToolUse:
				if isDraftPRToolName(ev.ToolName) || isCreateGithubIssueToolName(ev.ToolName) {
					useByID[ev.ToolID] = ev
				}
			case envKindToolResult:
				use, ok := useByID[ev.ToolID]
				if !ok {
					continue
				}
				ts := ev.Timestamp.UTC().Format(time.RFC3339)
				if isDraftPRToolName(use.ToolName) {
					a.backfillDraftPRResult(proposalsDir, dto.ID, use, ev, ts)
				} else if isCreateGithubIssueToolName(use.ToolName) {
					a.backfillCreateIssueResult(proposalsDir, dto.ID, ev, ts)
				}
			}
		}
	}
}

// backfillDraftPRResult writes (or merges) one draft_pr
// tool_result into the proposals dir, using the tool_result's
// timestamp so backfilled rows sort naturally alongside live ones.
func (a *apiHandlers) backfillDraftPRResult(
	proposalsDir string,
	investigationID string,
	use EventEnvelope,
	result EventEnvelope,
	createdAt string,
) {
	r, parsed := parseDraftPRToolResult(result.Text)
	if !parsed {
		return
	}
	id := r.ProposalID
	if id == "" || !codefixProposalIDPattern.MatchString(id) {
		id = deterministicCodefixProposalID(r.Repo, r.IssueNumber, r.PRNumber)
	}
	if r.IssueURL == "" {
		if v, ok := use.ToolInput["issue_url"].(string); ok {
			r.IssueURL = v
		}
	}
	merged := mergeCodefixProposal(proposalsDir, CodefixProposalPayload{
		ProposalID:      id,
		Repo:            r.Repo,
		IssueURL:        r.IssueURL,
		IssueNumber:     r.IssueNumber,
		PRURL:           r.PRURL,
		PRNumber:        r.PRNumber,
		BranchName:      r.BranchName,
		Summary:         r.Summary,
		InvestigationID: investigationID,
		CreatedAt:       createdAt,
	})
	if err := writeCodefixProposal(proposalsDir, merged); err == nil {
		a.trackCodefixRepo(merged.Repo)
	}
}

// backfillCreateIssueResult is the create_github_issue counterpart.
func (a *apiHandlers) backfillCreateIssueResult(
	proposalsDir string,
	investigationID string,
	result EventEnvelope,
	createdAt string,
) {
	r, parsed := parseCreateIssueToolResult(result.Text)
	if !parsed {
		return
	}
	id := r.ProposalID
	if id == "" || !codefixProposalIDPattern.MatchString(id) {
		id = deterministicCodefixProposalID(r.Repo, r.Number, 0)
	}
	merged := mergeCodefixProposal(proposalsDir, CodefixProposalPayload{
		ProposalID:      id,
		Repo:            r.Repo,
		IssueURL:        r.URL,
		IssueNumber:     r.Number,
		InvestigationID: investigationID,
		CreatedAt:       createdAt,
	})
	if err := writeCodefixProposal(proposalsDir, merged); err == nil {
		a.trackCodefixRepo(merged.Repo)
	}
}
