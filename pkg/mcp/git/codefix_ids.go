package git

import (
	"crypto/sha1"
	"fmt"
	"strings"
)

// codefixProposalID returns the deterministic id for a proposal
// keyed on (repo, issue_number). create_github_issue and draft_pr
// both call this so a later draft_pr lands on the same on-disk
// proposal file as the earlier create_github_issue, naturally
// merging into a single sidenav row.
//
// Format: `prop-<repo-slug>-i<n>`, with a sha1 fallback when the
// preferred form would exceed the launcher's 64-char id regex
// (`^prop-[a-z0-9-]{6,64}$`). The fallback stays deterministic so
// dedup still works for repos with very long names.
func codefixProposalID(repo string, issueNumber int) string {
	slug := repoSlug(repo)
	id := fmt.Sprintf("prop-%s-i%d", slug, issueNumber)
	if len(id) <= 64 {
		return id
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s/%d", repo, issueNumber)))
	return fmt.Sprintf("prop-h%x-i%d", h[:8], issueNumber)
}

// repoSlug lower-cases the "owner/name" form into a hyphen-separated
// slug containing only [a-z0-9-]. Filters non-conforming characters
// rather than escaping them — the slug is for an id, not a URL.
func repoSlug(repo string) string {
	s := strings.ToLower(strings.ReplaceAll(repo, "/", "-"))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitiseAgentProse normalises agent-authored prose before it
// reaches a GitHub-visible surface (issue title/body, PR
// title/body). Today's only rule: replace U+2014 em dashes with
// regular hyphens. Operators consistently flag em dashes as a
// tell-tale "this was written by an LLM" marker; stripping them
// at the boundary is cheaper than asking every prompt to avoid
// the character.
func sanitiseAgentProse(s string) string {
	if s == "" {
		return s
	}
	return strings.ReplaceAll(s, "—", "-")
}
