package server

import (
	"context"
	"strings"
	"time"
)

// refreshAllPRStates polls every repo we care about — the sessions
// repo (no label filter, all PRs) and every linked repo with an
// in-flight codefix proposal (filtered to label:triagent-proposal so the
// result set stays bounded). Emits codefix_pr_state envelopes on
// the global SSE bus for any URL whose state changed since the last
// snapshot.
//
// Best-effort: a failure on one repo doesn't block the others; the
// first error is returned, but the cache is still updated with
// whatever DID come back.
func (a *apiHandlers) refreshAllPRStates(ctx context.Context) error {
	repos := make(map[string]string, 1+len(a.codefixRepos))
	if a.opts.SessionsRepo != "" {
		repos[a.opts.SessionsRepo] = "" // no label filter — every PR
	}
	for r := range a.codefixRepos {
		repos[r] = "label:triagent-proposal"
	}
	var firstErr error
	next := make(map[string]PRInfo)
	for repo, label := range repos {
		m, err := fetchPRStatesFor(ctx, a.codefixGh, repo, label)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for k, v := range m {
			next[k] = v
		}
	}
	a.diffAndPublishCodefixPRStates(next)
	a.prStateCache = next
	return firstErr
}

// diffAndPublishCodefixPRStates emits a codefix_pr_state envelope
// for every URL whose state changed (or first appeared) since the
// last snapshot. Sessions-repo PR URLs are filtered out — they go
// through the existing per-investigation update path on
// Manager.RefreshPRStates.
func (a *apiHandlers) diffAndPublishCodefixPRStates(next map[string]PRInfo) {
	for url, info := range next {
		if !a.isCodefixURL(url) {
			continue
		}
		prev, ok := a.prStateCache[url]
		if ok &&
			prev.State == info.State &&
			equalTimePtr(prev.MergedAt, info.MergedAt) &&
			equalTimePtr(prev.ClosedAt, info.ClosedAt) {
			continue
		}
		a.manager.publishGlobalEvent(GlobalEventEnvelope{
			Kind: globalKindCodefixPRState,
			CodefixPRState: &CodefixPRStatePayload{
				URL:      url,
				State:    info.State,
				MergedAt: info.MergedAt,
				ClosedAt: info.ClosedAt,
			},
		})
	}
}

// isCodefixURL returns true when the URL belongs to one of the
// repos we're tracking codefix proposals for. Sessions-repo PR URLs
// don't match (different repo set).
func (a *apiHandlers) isCodefixURL(url string) bool {
	for repo := range a.codefixRepos {
		prefix := "https://github.com/" + repo + "/pull/"
		if strings.HasPrefix(url, prefix) {
			return true
		}
	}
	return false
}

// trackCodefixRepo adds owner/name to the polled set. Called when a
// new CodefixProposalPayload is persisted (future wiring point —
// today nothing calls this; tests populate the set directly).
func (a *apiHandlers) trackCodefixRepo(repo string) {
	if a.codefixRepos == nil {
		a.codefixRepos = map[string]struct{}{}
	}
	a.codefixRepos[repo] = struct{}{}
}

func equalTimePtr(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}
