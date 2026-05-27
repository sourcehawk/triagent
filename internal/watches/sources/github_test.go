package sources

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/watches"
)

func TestGitHubPollNewIssues(t *testing.T) {
	fake := &fakeGH{response: []byte(`[
		{"number": 4421, "title": "Engine OOMs", "body": "...", "user": {"login": "alice"}, "labels": [{"name": "bug"}], "html_url": "https://github.com/example-org/example-repo/issues/4421", "updated_at": "2026-05-11T10:04:01Z"},
		{"number": 4422, "title": "Docs typo",   "body": "...", "user": {"login": "bob"},   "labels": [{"name": "bug"}], "html_url": "https://github.com/example-org/example-repo/issues/4422", "updated_at": "2026-05-11T10:04:18Z"}
	]`)}
	src := &GitHub{run: fake.run}
	w := watches.Watch{
		ID:     "w1",
		Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "example-org", Repo: "example-repo", Labels: []string{"bug"}, States: []string{"open"}},
	}
	items, wm, err := src.Poll(context.Background(), w, watches.WatermarkState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].SourceRef.IssueNumber != 4421 {
		t.Fatalf("first item issue=%d, want 4421", items[0].SourceRef.IssueNumber)
	}
	if items[0].Snapshot.URL != "https://github.com/example-org/example-repo/issues/4421" {
		t.Fatalf("URL not captured")
	}
	if !wm.GitHubUpdatedAt.Equal(time.Date(2026, 5, 11, 10, 4, 18, 0, time.UTC)) {
		t.Fatalf("watermark not advanced to max updated_at, got %v", wm.GitHubUpdatedAt)
	}
}

func TestGitHubDedupesBySeenIssueNumbers(t *testing.T) {
	fake := &fakeGH{response: []byte(`[
		{"number": 4421, "title": "x", "user": {"login": "a"}, "labels": [{"name":"bug"}], "html_url": "u", "updated_at": "2026-05-11T10:04:01Z"}
	]`)}
	src := &GitHub{run: fake.run}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}}
	wm := watches.WatermarkState{SeenIssueNumbers: []int{4421}}
	items, _, err := src.Poll(context.Background(), w, wm)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 (already seen), got %d", len(items))
	}
}

func TestGitHubANDLabelFilter(t *testing.T) {
	fake := &fakeGH{response: []byte(`[
		{"number": 4421, "title": "x", "user": {"login": "a"}, "labels": [{"name":"bug"}], "html_url": "u", "updated_at": "2026-05-11T10:04:01Z"},
		{"number": 4422, "title": "y", "user": {"login": "b"}, "labels": [{"name":"bug"},{"name":"kind/incident"}], "html_url": "u", "updated_at": "2026-05-11T10:04:02Z"}
	]`)}
	src := &GitHub{run: fake.run}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r", Labels: []string{"bug", "kind/incident"}}}
	items, _, _ := src.Poll(context.Background(), w, watches.WatermarkState{})
	if len(items) != 1 || items[0].SourceRef.IssueNumber != 4422 {
		t.Fatalf("AND filter should keep only 4422, got %+v", items)
	}
}

// TestGitHubForcesGETMethod guards against gh api defaulting to POST when
// `-f` field flags are present. Without `-X GET` the call is interpreted as
// "create an issue" and the API returns 422.
func TestGitHubForcesGETMethod(t *testing.T) {
	fake := &fakeGH{response: []byte(`[]`)}
	src := &GitHub{run: fake.run}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r", Labels: []string{"bug"}}}
	if _, _, err := src.Poll(context.Background(), w, watches.WatermarkState{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fake.gotArgs, " ")
	if !strings.Contains(joined, "-X GET") {
		t.Fatalf("expected -X GET in args, got %q", joined)
	}
}

// TestGitHubErrorIncludesStderr guards against gh api errors being reported
// as bare "exit status 1" with no context. The launcher operator needs to
// see the actual gh/API error to debug a misconfigured watch.
func TestGitHubErrorIncludesStderr(t *testing.T) {
	src := &GitHub{run: func(_ context.Context, _ []string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("gh: Invalid request.\nFor 'properties/labels', ... (HTTP 422)")}
	}}
	w := watches.Watch{ID: "w1", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}}
	_, _, err := src.Poll(context.Background(), w, watches.WatermarkState{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 422") {
		t.Fatalf("error should surface stderr, got: %v", err)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("error should still wrap *exec.ExitError, got: %v", err)
	}
}

type fakeGH struct {
	response []byte
	gotArgs  []string
}

func (f *fakeGH) run(_ context.Context, args []string) ([]byte, error) {
	f.gotArgs = args
	return f.response, nil
}
