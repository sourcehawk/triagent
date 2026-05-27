package git

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchIssuesIn struct {
	Query string `json:"query" jsonschema:"free-text query — typically symptom + failing component, e.g. 'shard rebalance timeout'"`
	State string `json:"state,omitempty" jsonschema:"open | closed | all (default open)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results (default 10, cap 30)"`
}

type issueMatch struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updated_at"`
	Snippet   string `json:"snippet,omitempty"`
}

type searchIssuesOut struct {
	Repo    string       `json:"repo"`
	Query   string       `json:"query"`
	Matches []issueMatch `json:"matches"`
}

const (
	defaultIssueSearchLimit = 10
	maxIssueSearchLimit     = 30
	snippetMaxLen           = 200
)

func (s *Server) searchIssues(ctx context.Context, _ *mcp.CallToolRequest, in searchIssuesIn) (*mcp.CallToolResult, searchIssuesOut, error) {
	if in.Query == "" {
		return errorResult("query is required"), searchIssuesOut{Repo: s.repoFull()}, nil
	}
	state := strings.ToLower(strings.TrimSpace(in.State))
	if state == "" {
		state = "open"
	}
	// `gh search issues` only accepts {open,closed} on --state. "all"
	// is an agent-natural way to ask for "everything" — translate it
	// to omitting --state (gh's default for the search subcommand is
	// no state filter, which matches the agent's intent). Anything
	// else: reject up-front with a clear error citing the valid set
	// so the agent can self-correct rather than getting gh's raw
	// "invalid argument for --state flag".
	switch state {
	case "open", "closed", "all":
		// ok
	default:
		return errorResult(fmt.Sprintf(
			"state must be one of open|closed|all (got %q)", in.State,
		)), searchIssuesOut{Repo: s.repoFull(), Query: in.Query}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultIssueSearchLimit
	}
	if limit > maxIssueSearchLimit {
		limit = maxIssueSearchLimit
	}
	args := []string{
		"search", "issues", in.Query,
		"--repo", s.repoFull(),
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,state,url,updatedAt,body",
	}
	// Pass --state only when filtering; "all" → omit the flag.
	if state != "all" {
		args = append(args, "--state", state)
	}
	stdout, stderr, err := s.gh.Run(ctx, args...)
	if err != nil {
		return errorResult(fmt.Sprintf("gh search issues: %v (%s)", err, trimTo(string(stderr), 400))), searchIssuesOut{Repo: s.repoFull(), Query: in.Query}, nil
	}
	var raw []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		State     string `json:"state"`
		URL       string `json:"url"`
		UpdatedAt string `json:"updatedAt"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return errorResult(fmt.Sprintf("parse gh output: %v", err)), searchIssuesOut{Repo: s.repoFull(), Query: in.Query}, nil
	}
	matches := make([]issueMatch, 0, len(raw))
	for _, r := range raw {
		snippet := r.Body
		if len(snippet) > snippetMaxLen {
			snippet = snippet[:snippetMaxLen]
		}
		matches = append(matches, issueMatch{
			Number:    r.Number,
			Title:     r.Title,
			State:     r.State,
			URL:       r.URL,
			UpdatedAt: r.UpdatedAt,
			Snippet:   snippet,
		})
	}
	return nil, searchIssuesOut{Repo: s.repoFull(), Query: in.Query, Matches: matches}, nil
}
