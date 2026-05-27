package git

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubGh records each call and returns canned data per (subcmd, repo).
type stubGh struct {
	calls    [][]string
	response []byte
	stderr   []byte
	err      error
}

func (s *stubGh) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, append([]string(nil), args...))
	return s.response, s.stderr, s.err
}

func TestSearchIssues_PassesQueryAndStateAndLimit(t *testing.T) {
	t.Parallel()
	payload := []map[string]any{
		{"number": 42, "title": "shard rebalance hangs", "state": "OPEN", "url": "https://github.com/o/n/issues/42", "updatedAt": "2026-05-09T10:00:00Z", "body": "details..."},
	}
	raw, _ := json.Marshal(payload)
	stub := &stubGh{response: raw}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, out, err := s.searchIssues(context.Background(), nil, searchIssuesIn{
		Query: "shard rebalance",
		State: "open",
		Limit: 5,
	})
	require.NoError(t, err)
	require.Len(t, stub.calls, 1)
	args := stub.calls[0]
	require.Equal(t, "search", args[0])
	require.Equal(t, "issues", args[1])
	require.Contains(t, args, "--repo")
	require.Contains(t, args, "o/n")
	require.Contains(t, args, "--state")
	require.Contains(t, args, "open")
	require.Contains(t, args, "--limit")
	require.Contains(t, args, "5")
	require.Equal(t, 1, len(out.Matches))
	require.Equal(t, 42, out.Matches[0].Number)
}

func TestSearchIssues_DefaultStateAndLimit(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("[]")}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x"})
	require.NoError(t, err)
	args := stub.calls[0]
	require.Contains(t, args, "open") // default state
	require.Contains(t, args, "10")   // default limit
}

func TestSearchIssues_LimitCappedAt30(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("[]")}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x", Limit: 999})
	require.NoError(t, err)
	require.Contains(t, stub.calls[0], "30")
}

func TestSearchIssues_TruncatesSnippet(t *testing.T) {
	t.Parallel()
	body := ""
	for i := 0; i < 1000; i++ {
		body += "x"
	}
	payload := []map[string]any{{"number": 1, "title": "t", "state": "OPEN", "url": "https://x/1", "updatedAt": "2026-05-09T10:00:00Z", "body": body}}
	raw, _ := json.Marshal(payload)
	stub := &stubGh{response: raw}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, out, err := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x"})
	require.NoError(t, err)
	require.LessOrEqual(t, len(out.Matches[0].Snippet), 200)
}

func TestSearchIssues_RequiresQuery(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{}}
	res, _, _ := s.searchIssues(context.Background(), nil, searchIssuesIn{})
	require.NotNil(t, res)
	require.True(t, res.IsError)
}

func TestSearchIssues_ReturnsErrorOnGhFailure(t *testing.T) {
	t.Parallel()
	stub := &stubGh{stderr: []byte("auth required"), err: fmt.Errorf("exit 4")}
	s := &Server{owner: "o", name: "n", gh: stub}
	res, _, _ := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x"})
	require.NotNil(t, res)
	require.True(t, res.IsError)
}
