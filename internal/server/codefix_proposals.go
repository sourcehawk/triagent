package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CodefixProposalPayload is the persisted shape returned by GET
// /api/codefix-proposals/{id} and rendered by the
// CodefixProposalCard. Mirrors the wiki-proposal pattern.
type CodefixProposalPayload struct {
	ProposalID  string `json:"proposal_id"`
	Repo        string `json:"repo"` // owner/name
	IssueURL    string `json:"issue_url"`
	IssueNumber int    `json:"issue_number"`
	PRURL       string `json:"pr_url"`
	PRNumber    int    `json:"pr_number"`
	BranchName  string `json:"branch_name"`
	Summary     string `json:"summary"`
	// InvestigationID points back at the investigation whose
	// draft_pr tool emitted this proposal. Required for the repos
	// page's codefix activity panel ("which investigation opened
	// this PR?") and for backfilled entries reconstructed from
	// events.jsonl. Empty for proposals predating the field.
	InvestigationID string `json:"investigation_id,omitempty"`
	// CreatedAt is the time the proposal was first persisted.
	// RFC3339; omitted for entries predating the field. The repos
	// activity panel sorts by this descending.
	CreatedAt string `json:"created_at,omitempty"`
}

// codefixDiscardLedger is the outcome marker we persist when an
// operator discards a proposal via POST /discard. The ledger's
// existence guards against double-discard (the second call reads
// the ledger, returns 409).
type codefixDiscardLedger struct {
	ProposalID  string `json:"proposal_id"`
	DiscardedAt string `json:"discarded_at"`
}

func codefixProposalPath(proposalsDir, id string) string {
	return filepath.Join(proposalsDir, id+".json")
}

func codefixDiscardPath(proposalsDir, id string) string {
	return filepath.Join(proposalsDir, id+".discarded.json")
}

func writeCodefixProposal(proposalsDir string, p CodefixProposalPayload) error {
	if err := os.MkdirAll(proposalsDir, 0o700); err != nil {
		return fmt.Errorf("mkdir codefix proposals: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(codefixProposalPath(proposalsDir, p.ProposalID), data, 0o600)
}

func readCodefixProposal(proposalsDir, id string) (CodefixProposalPayload, error) {
	data, err := os.ReadFile(codefixProposalPath(proposalsDir, id))
	if err != nil {
		return CodefixProposalPayload{}, err
	}
	var p CodefixProposalPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return CodefixProposalPayload{}, err
	}
	return p, nil
}

func writeCodefixDiscardLedger(proposalsDir, id string, l codefixDiscardLedger) error {
	if err := os.MkdirAll(proposalsDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(codefixDiscardPath(proposalsDir, id), data, 0o600)
}

func readCodefixDiscardLedger(proposalsDir, id string) (codefixDiscardLedger, bool, error) {
	data, err := os.ReadFile(codefixDiscardPath(proposalsDir, id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return codefixDiscardLedger{}, false, nil
		}
		return codefixDiscardLedger{}, false, err
	}
	var l codefixDiscardLedger
	if err := json.Unmarshal(data, &l); err != nil {
		return codefixDiscardLedger{}, false, err
	}
	return l, true, nil
}

// listCodefixProposals reads every <id>.json file in proposalsDir and
// returns the parsed payloads. Files with a sibling <id>.discarded.json
// ledger are filtered out — discarded proposals should not appear in
// the repos activity panel. Returns nil (no error) when the directory
// does not exist.
func listCodefixProposals(proposalsDir string) ([]CodefixProposalPayload, error) {
	entries, err := os.ReadDir(proposalsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]CodefixProposalPayload, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".discarded.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !codefixProposalIDPattern.MatchString(id) {
			// Tolerate stray files (e.g. operator-pasted backups) by
			// skipping. The "listing returns what the get handler can
			// honour" invariant means we apply the same id check.
			continue
		}
		if _, discarded, _ := readCodefixDiscardLedger(proposalsDir, id); discarded {
			continue
		}
		p, err := readCodefixProposal(proposalsDir, id)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// codefixDraftPRResult is the subset of the triagent-git draft_pr tool's
// JSON output the launcher cares about for persistence. Extra fields
// (citations, prompt_sent, etc.) are intentionally discarded.
type codefixDraftPRResult struct {
	ProposalID  string `json:"proposal_id"`
	Repo        string `json:"repo"`
	IssueURL    string `json:"issue_url"`
	IssueNumber int    `json:"issue_number"`
	PRURL       string `json:"pr_url"`
	PRNumber    int    `json:"pr_number"`
	BranchName  string `json:"branch_name"`
	Summary     string `json:"summary"`
}

// parseDraftPRToolResult parses the JSON body of a draft_pr tool
// result envelope into a CodefixProposalPayload. Returns (payload,
// true) when the body looks like a complete proposal (has both a PR
// URL and either a proposal_id we can use or enough data to mint
// one); (zero, false) otherwise — the launcher silently drops
// non-PR results (early validation failures inside draftPR also use
// this shape but omit pr_url / pr_number).
func parseDraftPRToolResult(jsonBody string) (codefixDraftPRResult, bool) {
	var r codefixDraftPRResult
	if err := json.Unmarshal([]byte(jsonBody), &r); err != nil {
		return codefixDraftPRResult{}, false
	}
	// A "proposal" only exists once a PR was actually opened. Empty
	// pr_url means an early error path in draftPR (validation, push
	// failure) where there's nothing to track.
	if r.PRURL == "" || r.PRNumber == 0 {
		return codefixDraftPRResult{}, false
	}
	return r, true
}

// codefixCreateIssueResult is the subset of the triagent-git
// create_github_issue tool output the launcher persists. PRURL /
// PRNumber stay empty — a later draft_pr that lands on the same
// proposal_id fills them in via mergeCodefixProposal.
type codefixCreateIssueResult struct {
	ProposalID  string `json:"proposal_id"`
	Repo        string `json:"repo"`
	URL         string `json:"url"`
	Number      int    `json:"number"`
}

// parseCreateIssueToolResult parses the JSON body of a
// create_github_issue tool result. Returns (result, true) when an
// issue was actually created (url + number non-zero), (zero, false)
// otherwise — early error paths inside the tool emit the same shape
// but without those fields.
func parseCreateIssueToolResult(jsonBody string) (codefixCreateIssueResult, bool) {
	var r codefixCreateIssueResult
	if err := json.Unmarshal([]byte(jsonBody), &r); err != nil {
		return codefixCreateIssueResult{}, false
	}
	if r.URL == "" || r.Number == 0 {
		return codefixCreateIssueResult{}, false
	}
	return r, true
}

// mergeCodefixProposal reads any existing on-disk proposal for the
// given id and overlays it with the partial payload, preserving
// non-empty fields from disk that aren't being overwritten. Used so
// a later draft_pr lifts an issue-only row to an issue+PR row
// without losing the original CreatedAt / InvestigationID, and a
// re-run of create_github_issue (which can't really happen for the
// same issue number) wouldn't clobber PR fields.
func mergeCodefixProposal(proposalsDir string, incoming CodefixProposalPayload) CodefixProposalPayload {
	existing, err := readCodefixProposal(proposalsDir, incoming.ProposalID)
	if err != nil {
		// No existing file (or unreadable) — incoming is canonical.
		return incoming
	}
	out := existing
	if incoming.Repo != "" {
		out.Repo = incoming.Repo
	}
	if incoming.IssueURL != "" {
		out.IssueURL = incoming.IssueURL
	}
	if incoming.IssueNumber != 0 {
		out.IssueNumber = incoming.IssueNumber
	}
	if incoming.PRURL != "" {
		out.PRURL = incoming.PRURL
	}
	if incoming.PRNumber != 0 {
		out.PRNumber = incoming.PRNumber
	}
	if incoming.BranchName != "" {
		out.BranchName = incoming.BranchName
	}
	if incoming.Summary != "" {
		out.Summary = incoming.Summary
	}
	if incoming.InvestigationID != "" {
		out.InvestigationID = incoming.InvestigationID
	}
	// CreatedAt: keep the existing (earliest) timestamp. New writes
	// only set CreatedAt when the proposal didn't exist before.
	if out.CreatedAt == "" {
		out.CreatedAt = incoming.CreatedAt
	}
	return out
}
