// Package sources holds per-kind poll adapters consumed by the watches
// package's poller. Each adapter is stateless; state lives in the
// per-watch WatermarkState the manager threads in/out.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/watches"
)

// GitHub polls issues using the `gh` CLI. Hand-injecting `run` keeps tests
// hermetic; the production wiring uses ghExec.
type GitHub struct {
	run func(ctx context.Context, args []string) ([]byte, error)
}

func NewGitHub() *GitHub { return &GitHub{run: ghExec} }

func (g *GitHub) Kind() watches.SourceKind { return watches.SourceGitHubIssues }

func (g *GitHub) Poll(ctx context.Context, w watches.Watch, wm watches.WatermarkState) ([]watches.Item, watches.WatermarkState, error) {
	// -X GET is required: gh api silently switches to POST when `-f` field
	// flags are present, which turns this into a "create issue" request and
	// the API rejects it with 422.
	args := []string{"api", "-X", "GET", fmt.Sprintf("repos/%s/%s/issues", w.Source.Owner, w.Source.Repo), "--paginate"}
	q := []string{}
	if len(w.Source.Labels) > 0 {
		// gh accepts a single comma-separated value; results are issues
		// with ANY of these labels. We post-filter for AND below.
		q = append(q, "-f", "labels="+strings.Join(w.Source.Labels, ","))
	}
	states := w.Source.States
	if len(states) == 0 {
		states = []string{"open"}
	}
	q = append(q, "-f", "state="+strings.Join(states, ","))
	if !wm.GitHubUpdatedAt.IsZero() {
		q = append(q, "-f", "since="+wm.GitHubUpdatedAt.Format(time.RFC3339))
	}
	args = append(args, q...)
	body, err := g.run(ctx, args)
	if err != nil {
		// Surface gh's stderr if the runner attached it. Bare
		// "exit status 1" tells nobody anything.
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, wm, fmt.Errorf("gh api: %s: %w", strings.TrimSpace(string(ee.Stderr)), err)
		}
		return nil, wm, fmt.Errorf("gh api: %w", err)
	}
	var raw []struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		Body      string    `json:"body"`
		User      struct{ Login string } `json:"user"`
		Labels    []struct{ Name string } `json:"labels"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
		PullRequest *struct{}  `json:"pull_request,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, wm, fmt.Errorf("parse gh response: %w", err)
	}
	out := []watches.Item{}
	maxTS := wm.GitHubUpdatedAt
	for _, r := range raw {
		if r.PullRequest != nil {
			continue // gh api returns PRs under /issues — drop them.
		}
		if !hasAllLabels(r.Labels, w.Source.Labels) {
			continue
		}
		if watches.HasSeenIssue(wm, r.Number) {
			continue
		}
		body := truncate(r.Body, 4*1024)
		out = append(out, watches.Item{
			ID:         newULID(),
			WatchID:    w.ID,
			SourceKind: string(watches.SourceGitHubIssues),
			CapturedAt: time.Now().UTC(),
			SourceRef: watches.SourceRef{
				Owner: w.Source.Owner, Repo: w.Source.Repo,
				IssueNumber: r.Number, UpdatedAt: r.UpdatedAt,
			},
			Snapshot: watches.Snapshot{
				Title: truncate(r.Title, 200), Body: body,
				Author: r.User.Login, Labels: labelNames(r.Labels), URL: r.HTMLURL,
			},
		})
		wm = watches.RememberSeenIssue(wm, r.Number)
		if r.UpdatedAt.After(maxTS) {
			maxTS = r.UpdatedAt
		}
	}
	wm.GitHubUpdatedAt = maxTS
	return out, wm, nil
}

func hasAllLabels(got []struct{ Name string }, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, g := range got {
		set[g.Name] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func labelNames(got []struct{ Name string }) []string {
	out := make([]string, 0, len(got))
	for _, g := range got {
		out = append(out, g.Name)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func ghExec(ctx context.Context, args []string) ([]byte, error) {
	// Leaving cmd.Stderr nil lets exec.Cmd.Output populate
	// (*ExitError).Stderr for us; Poll extracts that message.
	return exec.CommandContext(ctx, "gh", args...).Output()
}

// newULID delegates to a package-level var so tests can stub it.
var newULID = func() string {
	return fmt.Sprintf("itm-%d", time.Now().UnixNano())
}
