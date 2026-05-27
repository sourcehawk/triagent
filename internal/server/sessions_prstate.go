package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PRState is the lifecycle of a session's pull request on GitHub. We
// learn the state by listing the upstream sessions repo's PRs in a
// single `gh pr list` call and matching each to an investigation by
// its captured PushURL. "" is the default — we don't know yet, or
// the PR doesn't exist (deleted, wrong auth, etc.).
const (
	PRStateOpen    = "open"
	PRStateMerged  = "merged"
	PRStateClosed  = "closed"
	PRStateUnknown = ""
)

// prListEntry is the bare-minimum decoder of `gh pr list --json …`.
type prListEntry struct {
	URL      string  `json:"url"`
	State    string  `json:"state"`
	MergedAt *string `json:"mergedAt"`
	ClosedAt *string `json:"closedAt"`
}

// PRInfo is the normalised in-memory shape we hold per PR.
type PRInfo struct {
	URL      string
	State    string
	MergedAt *time.Time
	ClosedAt *time.Time
}

// fetchPRStatesFor runs `gh pr list` against repo via the provided
// runner. When labelFilter is non-empty (e.g. "label:triagent-proposal"),
// it's added as a --search arg so the result set stays bounded.
func fetchPRStatesFor(ctx context.Context, gh codefixGhRunner, repo, labelFilter string) (map[string]PRInfo, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo is empty")
	}
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{
		"pr", "list",
		"--repo", repo,
		"--state", "all",
		"--json", "url,state,mergedAt,closedAt",
		"--limit", "1000",
	}
	if labelFilter != "" {
		args = append(args, "--search", labelFilter)
	}
	out, _, err := gh.Run(listCtx, args...)
	if err != nil {
		return nil, fmt.Errorf("gh pr list %s: %w", repo, err)
	}
	var entries []prListEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}
	result := make(map[string]PRInfo, len(entries))
	for _, e := range entries {
		info := PRInfo{
			URL:   e.URL,
			State: normaliseGHState(e.State),
		}
		if e.MergedAt != nil {
			if t, err := time.Parse(time.RFC3339, *e.MergedAt); err == nil {
				tt := t.UTC()
				info.MergedAt = &tt
			}
		}
		if e.ClosedAt != nil {
			if t, err := time.Parse(time.RFC3339, *e.ClosedAt); err == nil {
				tt := t.UTC()
				info.ClosedAt = &tt
			}
		}
		result[canonicalURL(e.URL)] = info
	}
	return result, nil
}

// fetchPRStates is the backward-compatible wrapper used by
// Manager.RefreshPRStates (the per-investigation push-PR poller).
// It uses a process-local realCodefixGhRunner so the function
// signature stays unchanged for existing callers.
func fetchPRStates(ctx context.Context, repo string) (map[string]PRInfo, error) {
	return fetchPRStatesFor(ctx, realCodefixGhRunner{}, repo, "")
}

// normaliseGHState maps gh's UPPERCASE state strings to our lowercase
// constants. Unknown values map to "" so the consumer treats them as
// "we don't really know" and leaves the existing state alone.
func normaliseGHState(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "OPEN":
		return PRStateOpen
	case "MERGED":
		return PRStateMerged
	case "CLOSED":
		return PRStateClosed
	default:
		return PRStateUnknown
	}
}

// canonicalURL strips trailing slashes + lowercases the host so a tiny
// formatting difference (e.g. trailing /) between what the launcher
// stored and what gh returns doesn't cause a lookup miss. Path stays
// case-sensitive (GitHub treats `Org` and `org` as the same in URLs
// but the path segments after the host are exact-case in practice).
func canonicalURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, "/")
	// Lowercase only the scheme + host. Find the third "/" (after
	// "https://"), keep everything from there as-is.
	idx := strings.Index(u, "://")
	if idx < 0 {
		return strings.ToLower(u)
	}
	rest := u[idx+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return strings.ToLower(u)
	}
	return strings.ToLower(u[:idx+3+slash]) + rest[slash:]
}
