package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createIssueIn struct {
	Title         string   `json:"title" jsonschema:"plain-English issue title — a human-readable sentence the reviewer reads in their inbox, not a slug. Under 70 chars. Example: 'Job timeout regression on the BPMN parser'."`
	BodyMarkdown  string   `json:"body_markdown" jsonschema:"full issue body as markdown — citations rendered inline as links. See the tool description for the required body shape (Description / Why / What & Impact / Evidence / Out of scope)."`
	Labels        []string `json:"labels,omitempty" jsonschema:"defaults to [\"triagent-proposal\"]; pass extra labels to add"`
	IssueType     string   `json:"issue_type,omitempty" jsonschema:"GitHub Issue Type to assign (e.g. 'Bug', 'Feature'). Call list_issue_types first to see what's available on this repo — when issue_types.enabled=false, omit this field entirely."`
	CrossRepoRefs []string `json:"cross_repo_refs,omitempty" jsonschema:"URLs of sibling issues filed in other linked repos for the same investigation"`
}

type createIssueOut struct {
	// ProposalID lets the launcher persist this issue into the
	// codefix activity panel keyed on (repo, issue_number). Stays
	// stable if a later draft_pr opens a PR for the same issue —
	// both code paths use codefixProposalID(repo, n) so the row
	// upgrades from issue-only to issue+PR in place.
	ProposalID string `json:"proposal_id"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	// IssueTypeApplied is the canonical name of the Issue Type that
	// was set on the new issue. Populated only when issue_type was
	// passed AND the GraphQL assign succeeded; empty otherwise. The
	// agent should read this back to verify (some gh auths don't
	// have the scope needed to mutate issue types — the issue still
	// gets created, but untyped).
	IssueTypeApplied string `json:"issue_type_applied,omitempty"`
	// IssueTypeError carries a human-readable warning when an
	// issue_type was requested but couldn't be applied (unknown
	// name, missing GraphQL scope, etc.). The issue itself is
	// created either way.
	IssueTypeError string `json:"issue_type_error,omitempty"`
}

const triagentProposalLabel = "triagent-proposal"

var issueURLPattern = regexp.MustCompile(`https://github\.com/[^/]+/[^/]+/issues/(\d+)`)

func (s *Server) createGithubIssue(ctx context.Context, _ *mcp.CallToolRequest, in createIssueIn) (*mcp.CallToolResult, createIssueOut, error) {
	title := sanitiseAgentProse(strings.TrimSpace(in.Title))
	if title == "" {
		return errorResult("title is required"), createIssueOut{Repo: s.repoFull()}, nil
	}
	if strings.TrimSpace(in.BodyMarkdown) == "" {
		return errorResult("body_markdown is required"), createIssueOut{Repo: s.repoFull()}, nil
	}
	body := sanitiseAgentProse(in.BodyMarkdown)
	if len(in.CrossRepoRefs) > 0 {
		var sb strings.Builder
		sb.WriteString(body)
		sb.WriteString("\n\n---\n## Related issues\n")
		for _, u := range in.CrossRepoRefs {
			sb.WriteString("- ")
			sb.WriteString(u)
			sb.WriteString("\n")
		}
		body = sb.String()
	}
	tmp, err := os.CreateTemp("", "c1-issue-body-*.md")
	if err != nil {
		return errorResult(fmt.Sprintf("create tempfile: %v", err)), createIssueOut{Repo: s.repoFull()}, nil
	}
	bodyPath := tmp.Name()
	defer func() { _ = os.Remove(bodyPath) }()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return errorResult(fmt.Sprintf("write tempfile: %v", err)), createIssueOut{Repo: s.repoFull()}, nil
	}
	if err := tmp.Close(); err != nil {
		return errorResult(fmt.Sprintf("close tempfile: %v", err)), createIssueOut{Repo: s.repoFull()}, nil
	}

	args := []string{
		"issue", "create",
		"--repo", s.repoFull(),
		"--title", title,
		"--body-file", bodyPath,
		"--label", triagentProposalLabel,
	}
	// gh CLI does not expose a `--type` flag on `gh issue create`
	// (verified against 2.88.1). GitHub Issue Types are a
	// server-side feature reachable only via the REST + GraphQL
	// APIs today. We create the issue first, then assign the type
	// via the updateIssueIssueType GraphQL mutation — same surface
	// list_issue_types already uses.
	requestedType := strings.TrimSpace(in.IssueType)
	seen := map[string]bool{triagentProposalLabel: true}
	for _, l := range in.Labels {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		args = append(args, "--label", l)
	}
	stdout, stderr, err := s.gh.Run(ctx, args...)
	if err != nil {
		return errorResult(fmt.Sprintf("gh issue create: %v (%s)", err, trimTo(string(stderr), 400))), createIssueOut{Repo: s.repoFull()}, nil
	}
	out := createIssueOut{Repo: s.repoFull()}
	if m := issueURLPattern.FindStringSubmatch(string(stdout)); len(m) >= 2 {
		out.URL = m[0]
		out.Number, _ = strconv.Atoi(m[1])
	} else {
		return errorResult(fmt.Sprintf("could not parse issue URL from gh output: %q", string(stdout))), out, nil
	}
	out.ProposalID = codefixProposalID(out.Repo, out.Number)
	if requestedType != "" {
		applied, assignErr := s.assignIssueType(ctx, out.Number, requestedType)
		if assignErr != nil {
			out.IssueTypeError = assignErr.Error()
		} else {
			out.IssueTypeApplied = applied
		}
	}
	return nil, out, nil
}

// graphqlIssueAndTypesQuery fetches the new issue's GraphQL node id
// alongside the repo's enabled Issue Types in a single round trip.
// Both ids are required by updateIssueIssueType.
const graphqlIssueAndTypesQuery = `query($owner:String!,$name:String!,$num:Int!){repository(owner:$owner,name:$name){issue(number:$num){id} issueTypes(first:50){nodes{id name}}}}`

// graphqlAssignIssueTypeMutation flips the Issue Type on an existing
// issue. Available on github.com — GHES picks it up alongside the
// Issue Types feature when the org enables it. Caller surfaces any
// "Field 'updateIssueIssueType' doesn't exist" error as an "issue
// types not supported on this instance" hint.
const graphqlAssignIssueTypeMutation = `mutation($issueId:ID!,$issueTypeId:ID!){updateIssueIssueType(input:{issueId:$issueId,issueTypeId:$issueTypeId}){issue{issueType{name}}}}`

// assignIssueType applies a named Issue Type to the just-created
// issue. Returns the canonical Issue Type name on success. On
// failure, returns an error describing what went wrong — the
// caller surfaces it on the createIssueOut but doesn't fail the
// whole tool call.
func (s *Server) assignIssueType(ctx context.Context, issueNumber int, typeName string) (string, error) {
	if s.gh == nil {
		return "", fmt.Errorf("gh runner not configured")
	}
	stdout, _, err := s.gh.Run(ctx, "api", "graphql",
		"-f", "query="+graphqlIssueAndTypesQuery,
		"-F", "owner="+s.owner,
		"-F", "name="+s.name,
		"-F", fmt.Sprintf("num=%d", issueNumber),
	)
	if err != nil {
		return "", fmt.Errorf("lookup issue + types: %w", err)
	}
	var resp struct {
		Data struct {
			Repository struct {
				Issue struct {
					ID string `json:"id"`
				} `json:"issue"`
				IssueTypes struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"issueTypes"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if jerr := json.Unmarshal(stdout, &resp); jerr != nil {
		return "", fmt.Errorf("parse issue + types response: %w", jerr)
	}
	if len(resp.Errors) > 0 {
		return "", fmt.Errorf("graphql: %s", resp.Errors[0].Message)
	}
	issueID := resp.Data.Repository.Issue.ID
	if issueID == "" {
		return "", fmt.Errorf("could not resolve graphql id for issue #%d", issueNumber)
	}
	var (
		typeID         string
		canonicalName  string
		availableNames []string
	)
	lower := strings.ToLower(typeName)
	for _, n := range resp.Data.Repository.IssueTypes.Nodes {
		availableNames = append(availableNames, n.Name)
		if strings.EqualFold(n.Name, typeName) {
			typeID = n.ID
			canonicalName = n.Name
			break
		}
		// Fall back to a case-insensitive contains match so
		// "tech debt" and "tech-debt" both resolve to the same
		// type — GitHub's UI is lenient here, the agent shouldn't
		// be the strict one.
		if typeID == "" && strings.Contains(strings.ToLower(n.Name), lower) {
			typeID = n.ID
			canonicalName = n.Name
		}
	}
	if typeID == "" {
		return "", fmt.Errorf("issue type %q not found on %s (available: %s)", typeName, s.repoFull(), strings.Join(availableNames, ", "))
	}
	_, mutErrOut, mutErr := s.gh.Run(ctx, "api", "graphql",
		"-f", "query="+graphqlAssignIssueTypeMutation,
		"-F", "issueId="+issueID,
		"-F", "issueTypeId="+typeID,
	)
	if mutErr != nil {
		return "", fmt.Errorf("updateIssueIssueType: %v (%s)", mutErr, trimTo(string(mutErrOut), 400))
	}
	return canonicalName, nil
}
