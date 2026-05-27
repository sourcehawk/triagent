package git

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// getArchitectureSummaryIn is empty by design — the triagent-git server is bound to
// one repo via --repo, so there are no per-call args. Output carries the
// cache file's frontmatter and body, or a hint when no cache exists yet.
type getArchitectureSummaryIn struct{}

// getArchitectureSummaryOut is the JSON payload returned by get_repo_architecture_summary.
type getArchitectureSummaryOut struct {
	Exists      bool      `json:"exists"`
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Focus       string    `json:"focus,omitempty"`
	Content     string    `json:"content,omitempty"`
	ByteCount   int       `json:"byte_count,omitempty"`
	Hint        string    `json:"hint,omitempty"`
	// Error is non-empty when the cached summary represents a failed generation
	// (sub-agent timeout, clone error, etc). Even when set, Exists remains true —
	// the cache file exists; Content carries the human-readable failure note.
	// Agents should branch on Error before reading Content.
	Error string `json:"error,omitempty"`
}

// missingHint is returned to the agent when no cache file exists yet. Tells
// the agent (a) where the fallback orientation lives — the LinkedRepos
// section of the system prompt — and (b) that auto-generation may still be
// running. Worded so the agent doesn't conclude "no information" and skip
// to expensive sub-agent calls.
const missingHint = "No cached architecture summary for this repo yet. Use the brief description in the **Linked repositories** section of the system prompt as orientation, then fall through to commit_summary / search_log / diff_summary if you need more detail. The operator can trigger generation from the repo page; auto-gen on connection may still be running."

func (s *Server) getRepoArchitectureSummary(_ context.Context, _ *mcp.CallToolRequest, _ getArchitectureSummaryIn) (*mcp.CallToolResult, getArchitectureSummaryOut, error) {
	path := SummaryPath(s.cacheDir, s.owner, s.name)
	sum, err := ReadSummary(path)
	if errors.Is(err, ErrSummaryNotFound) {
		return nil, getArchitectureSummaryOut{Exists: false, Hint: missingHint}, nil
	}
	if err != nil {
		return errorResult(err.Error()), getArchitectureSummaryOut{}, nil
	}
	return nil, getArchitectureSummaryOut{
		Exists:      true,
		GeneratedAt: sum.Frontmatter.GeneratedAt,
		Kind:        sum.Frontmatter.Kind,
		Focus:       sum.Frontmatter.Focus,
		Content:     sum.Body,
		ByteCount:   sum.Frontmatter.ByteCount,
		Error:       sum.Frontmatter.Error,
	}, nil
}
