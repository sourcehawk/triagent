package git

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingGh extends stubGh by also capturing the file referenced by
// --body-file so the test can assert on the augmented body.
type recordingGh struct {
	stubGh
	bodyFileContents []byte
}

func (r *recordingGh) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	for i, a := range args {
		if a == "--body-file" && i+1 < len(args) {
			data, err := os.ReadFile(args[i+1])
			if err == nil {
				r.bodyFileContents = data
			}
		}
	}
	return r.stubGh.Run(ctx, args...)
}

func TestCreateGithubIssue_HappyPath(t *testing.T) {
	t.Parallel()
	stub := &recordingGh{stubGh: stubGh{response: []byte("https://github.com/o/n/issues/123\n")}}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, out, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:        "Job timeout regression",
		BodyMarkdown: "Detected via investigation [INC-42](https://...).",
	})
	require.NoError(t, err)
	require.Equal(t, "https://github.com/o/n/issues/123", out.URL)
	require.Equal(t, 123, out.Number)
	require.Equal(t, "o/n", out.Repo)
	args := stub.calls[0]
	require.Equal(t, "issue", args[0])
	require.Equal(t, "create", args[1])
	require.Contains(t, args, "--label")
	require.Contains(t, args, "triagent-proposal")
}

func TestCreateGithubIssue_AppendsCrossRepoRefs(t *testing.T) {
	t.Parallel()
	stub := &recordingGh{stubGh: stubGh{response: []byte("https://github.com/o/n/issues/2\n")}}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:         "T",
		BodyMarkdown:  "Body.",
		CrossRepoRefs: []string{"https://github.com/x/y/issues/1", "https://github.com/x/z/issues/3"},
	})
	require.NoError(t, err)
	body := string(stub.bodyFileContents)
	require.Contains(t, body, "## Related issues")
	require.Contains(t, body, "https://github.com/x/y/issues/1")
	require.Contains(t, body, "https://github.com/x/z/issues/3")
}

func TestCreateGithubIssue_AddsCallerLabelsOnTopOfC1Proposal(t *testing.T) {
	t.Parallel()
	stub := &recordingGh{stubGh: stubGh{response: []byte("https://github.com/o/n/issues/1\n")}}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title:        "T",
		BodyMarkdown: "Body.",
		Labels:       []string{"alert-rule", "docs"},
	})
	require.NoError(t, err)
	args := stub.calls[0]
	var labels []string
	for i, a := range args {
		if a == "--label" && i+1 < len(args) {
			labels = append(labels, args[i+1])
		}
	}
	require.ElementsMatch(t, []string{"triagent-proposal", "alert-rule", "docs"}, labels)
}

func TestCreateGithubIssue_DoesNotDoubleAddC1Proposal(t *testing.T) {
	// If the caller passes "triagent-proposal" explicitly, we shouldn't add it
	// a second time.
	t.Parallel()
	stub := &recordingGh{stubGh: stubGh{response: []byte("https://github.com/o/n/issues/1\n")}}
	s := &Server{owner: "o", name: "n", gh: stub}
	_, _, err := s.createGithubIssue(context.Background(), nil, createIssueIn{
		Title: "T", BodyMarkdown: "B", Labels: []string{"triagent-proposal", "extra"},
	})
	require.NoError(t, err)
	args := stub.calls[0]
	var n int
	for _, a := range args {
		if a == "triagent-proposal" {
			n++
		}
	}
	require.Equal(t, 1, n, "triagent-proposal label should appear exactly once in args")
}

func TestCreateGithubIssue_RequiresTitleAndBody(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{}}

	res, _, _ := s.createGithubIssue(context.Background(), nil, createIssueIn{Title: "", BodyMarkdown: "x"})
	require.True(t, res.IsError)

	res, _, _ = s.createGithubIssue(context.Background(), nil, createIssueIn{Title: "x", BodyMarkdown: ""})
	require.True(t, res.IsError)
}

func TestCreateGithubIssue_ParsesIssueNumberFromURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stdout string
		wantN  int
	}{
		{"https://github.com/o/n/issues/42\n", 42},
		{"https://github.com/o/n/issues/9999", 9999},
		{strings.Repeat("noise\n", 3) + "https://github.com/o/n/issues/7\n", 7},
	}
	for _, c := range cases {
		stub := &stubGh{response: []byte(c.stdout)}
		s := &Server{owner: "o", name: "n", gh: stub}
		_, out, _ := s.createGithubIssue(context.Background(), nil, createIssueIn{Title: "T", BodyMarkdown: "B"})
		require.Equal(t, c.wantN, out.Number, "stdout=%q", c.stdout)
	}
}

func TestCreateGithubIssue_GhFailureSurfacesAsError(t *testing.T) {
	t.Parallel()
	stub := &recordingGh{stubGh: stubGh{stderr: []byte("auth required"), err: fmt.Errorf("exit 4")}}
	s := &Server{owner: "o", name: "n", gh: stub}
	res, _, _ := s.createGithubIssue(context.Background(), nil, createIssueIn{Title: "T", BodyMarkdown: "B"})
	require.True(t, res.IsError)
}
