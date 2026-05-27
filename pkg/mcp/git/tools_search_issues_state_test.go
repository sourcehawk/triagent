package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSearchIssues_StateAll_OmitsStateFlag — gh search issues doesn't
// accept --state=all, so when the agent asks for "all" we omit the
// flag (gh's default is "everything" for `search`).
func TestSearchIssues_StateAll_OmitsStateFlag(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("[]")}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x", State: "all"})
	require.NoError(t, err)
	require.Len(t, stub.calls, 1)
	for i, a := range stub.calls[0] {
		if a == "--state" {
			t.Fatalf("`--state` must not be present when State=\"all\" (gh rejects it). args=%v at index %d", stub.calls[0], i)
		}
	}
}

// TestSearchIssues_StateAll_CaseInsensitive — agent typo'd casing
// should still resolve to the omit-state behaviour.
func TestSearchIssues_StateAll_CaseInsensitive(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("[]")}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x", State: "ALL"})
	require.NoError(t, err)
	for _, a := range stub.calls[0] {
		if a == "--state" {
			t.Fatalf("`--state` must not be present when State=\"ALL\"")
		}
	}
}

// TestSearchIssues_InvalidStateRejectedClearly — anything outside
// {open,closed,all} must error early with a clear message mentioning
// the valid set, instead of forwarding to gh and surfacing its raw
// "unknown option" error.
func TestSearchIssues_InvalidStateRejectedClearly(t *testing.T) {
	t.Parallel()
	stub := &stubGh{}
	s := &Server{owner: "o", name: "n", gh: stub}
	res, _, _ := s.searchIssues(context.Background(), nil, searchIssuesIn{Query: "x", State: "bogus"})
	require.NotNil(t, res)
	require.True(t, res.IsError, "invalid state must surface as IsError")
	msg := errorText(res)
	require.Contains(t, msg, "state")
	require.Contains(t, msg, "open")
	require.Contains(t, msg, "closed")
	require.Contains(t, msg, "all")
	require.Contains(t, msg, "bogus", "echo the agent's input so the agent can self-correct")
	// gh must NOT have been called.
	require.Empty(t, stub.calls, "no gh invocation on invalid state")
}
