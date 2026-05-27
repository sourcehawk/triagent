package git

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoTypesPayload is the canonical happy-path REST payload.
func repoTypesPayload() []byte {
	raw, _ := json.Marshal([]map[string]string{
		{"name": "Bug", "description": "Something is broken", "color": "red"},
		{"name": "Feature", "description": "New work", "color": "green"},
	})
	return raw
}

// graphqlTypesPayload builds the canonical GraphQL response with the
// given type names.
func graphqlTypesPayload(names ...string) []byte {
	type node struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	nodes := make([]node, len(names))
	for i, n := range names {
		nodes[i] = node{Name: n, Description: "x", Color: "blue"}
	}
	resp := map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"issueTypes": map[string]any{
					"nodes": nodes,
				},
			},
		},
	}
	out, _ := json.Marshal(resp)
	return out
}

// programmedGh routes each gh call by inspecting argv. Lets a test
// fail one probe and succeed another so we exercise the fall-through.
type programmedGh struct {
	calls    [][]string
	respond  func(args []string) (stdout []byte, stderr []byte, err error)
}

func (p *programmedGh) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	p.calls = append(p.calls, append([]string(nil), args...))
	return p.respond(args)
}

func TestListIssueTypes_RepoEndpointWins(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			if len(args) >= 2 && args[0] == "api" && args[1] == "repos/o/n/issue-types" {
				return repoTypesPayload(), nil, nil
			}
			return nil, []byte("unexpected call"), fmt.Errorf("unexpected")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.listIssueTypes(context.Background(), nil, listIssueTypesIn{})
	require.NoError(t, err)
	require.True(t, out.Enabled)
	require.Equal(t, "repo", out.Source)
	require.Len(t, out.IssueTypes, 2)
	require.Len(t, gh.calls, 1, "should not fall through when repo endpoint wins")
}

func TestListIssueTypes_GraphQLFallback(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			if len(args) >= 2 && args[0] == "api" && strings.HasPrefix(args[1], "repos/") {
				return nil, []byte("HTTP 404: Not Found"), fmt.Errorf("exit 1")
			}
			if len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
				return graphqlTypesPayload("Bug", "Feature", "Task"), nil, nil
			}
			return nil, []byte("unexpected"), fmt.Errorf("unexpected")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.listIssueTypes(context.Background(), nil, listIssueTypesIn{})
	require.NoError(t, err)
	require.True(t, out.Enabled)
	require.Equal(t, "graphql", out.Source)
	require.Len(t, out.IssueTypes, 3)
	require.Equal(t, "Bug", out.IssueTypes[0].Name)
}

func TestListIssueTypes_OrgFallback(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			if len(args) >= 2 && args[0] == "api" && strings.HasPrefix(args[1], "repos/") {
				return nil, []byte("404"), fmt.Errorf("exit 1")
			}
			if len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
				return nil, []byte("graphql 500"), fmt.Errorf("exit 1")
			}
			if len(args) >= 2 && args[0] == "api" && args[1] == "orgs/o/issue-types" {
				return repoTypesPayload(), nil, nil
			}
			return nil, []byte("unexpected"), fmt.Errorf("unexpected")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.listIssueTypes(context.Background(), nil, listIssueTypesIn{})
	require.NoError(t, err)
	require.True(t, out.Enabled)
	require.Equal(t, "org", out.Source)
	require.Len(t, out.IssueTypes, 2)
}

func TestListIssueTypes_AllProbesFail(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			return nil, []byte("404"), fmt.Errorf("exit 1")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.listIssueTypes(context.Background(), nil, listIssueTypesIn{})
	require.NoError(t, err)
	require.False(t, out.Enabled)
	require.NotEmpty(t, out.Hint, "operator-facing hint should explain the next step")
	require.NotNil(t, out.IssueTypes)
	require.Empty(t, out.IssueTypes)
	// All three surfaces probed.
	require.Len(t, gh.calls, 3)
}

// graphqlIssueAndTypesPayload mimics the GraphQL response returned by
// graphqlIssueAndTypesQuery: the new issue's node id alongside the
// repo's available Issue Types.
func graphqlIssueAndTypesPayload(issueNodeID string, types map[string]string) []byte {
	type tn struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	nodes := make([]tn, 0, len(types))
	for name, id := range types {
		nodes = append(nodes, tn{ID: id, Name: name})
	}
	resp := map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"issue":      map[string]any{"id": issueNodeID},
				"issueTypes": map[string]any{"nodes": nodes},
			},
		},
	}
	out, _ := json.Marshal(resp)
	return out
}

// graphqlAssignTypePayload mimics the successful response of the
// updateIssueIssueType mutation. The mutation returns
// issue.issueType.name but the tool doesn't read it back (we use
// the canonical name we looked up locally), so we keep this minimal.
func graphqlAssignTypePayload(name string) []byte {
	out, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"updateIssueIssueType": map[string]any{
				"issue": map[string]any{
					"issueType": map[string]any{"name": name},
				},
			},
		},
	})
	return out
}

func TestCreateGithubIssue_AssignsIssueTypeViaGraphQL(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			switch {
			case args[0] == "issue" && args[1] == "create":
				// Critical: --type must NOT be passed to `gh issue
				// create`. The gh CLI does not expose a --type
				// flag (verified against 2.88.1) — the assertion
				// guards against a regression where someone "helps"
				// by re-adding it on a future gh release that
				// claims to support it. GraphQL is the canonical
				// path; keep it that way.
				for _, a := range args {
					if a == "--type" {
						return nil, []byte("unknown flag: --type\n"), fmt.Errorf("exit 1")
					}
				}
				return []byte("https://github.com/o/n/issues/7\n"), nil, nil
			case args[0] == "api" && args[1] == "graphql" && strings.Contains(joinArgs(args), "issueTypes(first:50)"):
				return graphqlIssueAndTypesPayload("ISSUE_NODE_7", map[string]string{
					"Bug":       "TYPE_BUG",
					"Tech-debt": "TYPE_DEBT",
				}), nil, nil
			case args[0] == "api" && args[1] == "graphql":
				return graphqlAssignTypePayload("Bug"), nil, nil
			}
			return nil, []byte("unexpected: " + joinArgs(args)), fmt.Errorf("unexpected")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:        "T",
		BodyMarkdown: "B",
		IssueType:    "Bug",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/issues/7", out.URL)
	require.Equal(t, "Bug", out.IssueTypeApplied)
	require.Empty(t, out.IssueTypeError)
	// 3 calls: issue create, GraphQL lookup, GraphQL assign mutation.
	require.Len(t, gh.calls, 3)
}

func TestCreateGithubIssue_OmitsTypeWhenEmpty(t *testing.T) {
	t.Parallel()
	stub := &recordingGh{stubGh: stubGh{response: []byte("https://github.com/o/n/issues/1\n")}}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, out, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:        "T",
		BodyMarkdown: "B",
	})
	require.NoError(t, err)
	require.NotContains(t, stub.calls[0], "--type", "no --type flag when IssueType is empty")
	require.Empty(t, out.IssueTypeApplied)
	require.Empty(t, out.IssueTypeError)
	require.Len(t, stub.calls, 1, "no GraphQL calls when no type is requested")
}

func TestCreateGithubIssue_UnknownTypeSurfacesAvailable(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			switch {
			case args[0] == "issue" && args[1] == "create":
				return []byte("https://github.com/o/n/issues/9\n"), nil, nil
			case args[0] == "api" && args[1] == "graphql":
				return graphqlIssueAndTypesPayload("ISSUE_NODE_9", map[string]string{
					"Bug":     "TYPE_BUG",
					"Feature": "TYPE_FEATURE",
				}), nil, nil
			}
			return nil, []byte("unexpected"), fmt.Errorf("unexpected")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:        "T",
		BodyMarkdown: "B",
		IssueType:    "Bogus",
	})
	require.NoError(t, err, "issue creation succeeds even on type-assign failure")
	require.Equal(t, 9, out.Number)
	require.Empty(t, out.IssueTypeApplied)
	require.Contains(t, out.IssueTypeError, "Bogus")
	require.Contains(t, out.IssueTypeError, "Bug")
	require.Contains(t, out.IssueTypeError, "Feature")
}

func TestCreateGithubIssue_CaseInsensitiveTypeMatch(t *testing.T) {
	t.Parallel()
	gh := &programmedGh{
		respond: func(args []string) ([]byte, []byte, error) {
			switch {
			case args[0] == "issue" && args[1] == "create":
				return []byte("https://github.com/o/n/issues/3\n"), nil, nil
			case args[0] == "api" && args[1] == "graphql" && strings.Contains(joinArgs(args), "issueTypes(first:50)"):
				return graphqlIssueAndTypesPayload("ISSUE_NODE_3", map[string]string{
					"Tech-debt": "TYPE_DEBT",
				}), nil, nil
			case args[0] == "api" && args[1] == "graphql":
				return graphqlAssignTypePayload("Tech-debt"), nil, nil
			}
			return nil, []byte("unexpected"), fmt.Errorf("unexpected")
		},
	}
	s := &Server{owner: "o", name: "n", gh: gh}
	_, out, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:        "T",
		BodyMarkdown: "B",
		IssueType:    "tech-debt", // case mismatch — must still resolve
	})
	require.NoError(t, err)
	require.Equal(t, "Tech-debt", out.IssueTypeApplied,
		"applied type uses the repo's canonical casing, not the agent's input")
}

