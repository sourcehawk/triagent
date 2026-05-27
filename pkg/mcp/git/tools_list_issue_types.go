package git

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listIssueTypesIn struct{}

type issueType struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
}

type listIssueTypesOut struct {
	Repo       string      `json:"repo"`
	Enabled    bool        `json:"enabled"`
	IssueTypes []issueType `json:"issue_types"`
	// Source tells the agent which probe path returned the list:
	// "repo" (REST repos/<o>/<n>/issue-types), "org" (REST
	// orgs/<o>/issue-types), or "graphql" (Repository.issueTypes).
	// Empty when Enabled=false.
	Source string `json:"source,omitempty"`
	// Hint is a one-line operator-facing string when Enabled is false
	// — points the agent at what to do next.
	Hint string `json:"hint,omitempty"`
}

// graphqlIssueTypesQuery fetches the issue types enabled on the
// specific repository. Repository.issueTypes returns the org's
// configured types filtered to those the repo can use, which is
// closer to the per-repo view operators see in the GitHub UI.
const graphqlIssueTypesQuery = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){issueTypes(first:50){nodes{name description color}}}}`

// listIssueTypes returns the GitHub Issue Types available on this
// repo. Tries three sources in order, accepting the first that
// returns a non-empty list and reporting which via the Source field:
//
//   1. REST repos/<owner>/<name>/issue-types — repo-scoped if the
//      endpoint exists.
//   2. GraphQL repository(owner,name).issueTypes — the canonical
//      repo-scoped view that matches what operators see in the UI.
//   3. REST orgs/<owner>/issue-types — the org-wide list as a final
//      fallback for orgs where the per-repo view isn't accessible.
//
// On every probe failing or returning empty, Enabled=false and the
// agent skips the issue_type input on create_github_issue.
func (s *Server) listIssueTypes(ctx context.Context, _ *mcp.CallToolRequest, _ listIssueTypesIn) (*mcp.CallToolResult, listIssueTypesOut, error) {
	// Always initialise IssueTypes as a non-nil empty slice so the
	// JSON output is `[]` rather than `null` on the disabled-org /
	// error paths — the MCP SDK's output schema requires an array.
	out := listIssueTypesOut{Repo: s.repoFull(), IssueTypes: []issueType{}}
	if s.gh == nil {
		return errorResult("gh runner not configured"), out, nil
	}

	// 1. Try the repo-scoped REST endpoint first.
	if types, ok := s.probeIssueTypesREST(ctx, fmt.Sprintf("repos/%s/issue-types", s.repoFull())); ok {
		out.Enabled = true
		out.Source = "repo"
		out.IssueTypes = types
		if len(types) == 0 {
			out.Hint = "Repo-scoped Issue Types endpoint returned an empty list."
		}
		return nil, out, nil
	}

	// 2. Try GraphQL on the Repository.
	if types, ok := s.probeIssueTypesGraphQL(ctx); ok {
		out.Enabled = true
		out.Source = "graphql"
		out.IssueTypes = types
		if len(types) == 0 {
			out.Hint = "GraphQL Repository.issueTypes returned an empty list."
		}
		return nil, out, nil
	}

	// 3. Fall back to the org-level REST endpoint.
	if types, ok := s.probeIssueTypesREST(ctx, fmt.Sprintf("orgs/%s/issue-types", s.owner)); ok {
		out.Enabled = true
		out.Source = "org"
		out.IssueTypes = types
		if len(types) == 0 {
			out.Hint = "Org-level Issue Types endpoint returned an empty list."
		}
		return nil, out, nil
	}

	// Every probe failed — likely the owner isn't an org, or none of
	// gh-auth scopes can see Issue Types. Surface a clear hint.
	out.Hint = "GitHub Issue Types weren't available via any of the probed surfaces (repo REST, GraphQL, org REST). Either Issue Types aren't enabled on this owner, or the gh auth lacks the scope needed to read them (read:org). Omit issue_type on create_github_issue."
	return nil, out, nil
}

// probeIssueTypesREST runs `gh api <path>` expecting an array of
// {name, description, color}. Returns (types, true) on success even
// when the array is empty — the agent uses the Source field to know
// which surface answered. Returns (_, false) on any error, so the
// caller can fall through.
func (s *Server) probeIssueTypesREST(ctx context.Context, path string) ([]issueType, bool) {
	stdout, _, err := s.gh.Run(ctx, "api", path)
	if err != nil {
		return nil, false
	}
	var raw []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return nil, false
	}
	types := make([]issueType, 0, len(raw))
	for _, r := range raw {
		types = append(types, issueType{
			Name:        r.Name,
			Description: r.Description,
			Color:       r.Color,
		})
	}
	return types, true
}

// probeIssueTypesGraphQL queries Repository.issueTypes via the
// GraphQL API — the path the GitHub UI uses for the per-repo Issue
// Types dropdown. Returns (types, true) on success including the
// empty case; (_, false) only when the field is missing or the
// query errors.
func (s *Server) probeIssueTypesGraphQL(ctx context.Context) ([]issueType, bool) {
	stdout, _, err := s.gh.Run(ctx, "api", "graphql",
		"-f", "query="+graphqlIssueTypesQuery,
		"-F", "owner="+s.owner,
		"-F", "name="+s.name,
	)
	if err != nil {
		return nil, false
	}
	var resp struct {
		Data struct {
			Repository struct {
				IssueTypes struct {
					Nodes []struct {
						Name        string `json:"name"`
						Description string `json:"description"`
						Color       string `json:"color"`
					} `json:"nodes"`
				} `json:"issueTypes"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return nil, false
	}
	if len(resp.Errors) > 0 {
		// Field-missing / schema mismatch error — older GitHub
		// instances won't have Repository.issueTypes.
		for _, e := range resp.Errors {
			if strings.Contains(strings.ToLower(e.Message), "field") &&
				strings.Contains(strings.ToLower(e.Message), "issuetypes") {
				return nil, false
			}
		}
		return nil, false
	}
	nodes := resp.Data.Repository.IssueTypes.Nodes
	types := make([]issueType, 0, len(nodes))
	for _, n := range nodes {
		types = append(types, issueType{
			Name:        n.Name,
			Description: n.Description,
			Color:       n.Color,
		})
	}
	return types, true
}
