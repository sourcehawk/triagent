package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/charmbracelet/log"
)

const (
	// codefixLabelName is what triagent-mcp/git's create_github_issue tool
	// always attaches. Real PRs on the c1-investigation-* poller's
	// fan-out depend on it too — the `--search "label:triagent-proposal"`
	// filter is how we keep the result set bounded per linked repo.
	codefixLabelName        = "triagent-proposal"
	codefixLabelColor       = "ec4899" // pink-500
	codefixLabelDescription = "A proposal made by the c1 investigation agent"
)

// ensureCodefixLabels iterates each linked repo and ensures the
// triagent-proposal label exists. create_github_issue always attaches the
// label; gh issue create fails outright if it's missing, so we
// proactively create it on every linked repo at launcher startup.
//
// Best-effort: per-repo failures (gh not authed, network blip, no
// write access, racing label-create) are logged and otherwise ignored.
// The codefix capability gate already keeps the button disabled when
// gh is unauthed, so this never blocks the operator's main flow.
//
// Called once asynchronously from server.New after the apiHandlers
// are constructed; the goroutine ends when the slice is exhausted.
func (a *apiHandlers) ensureCodefixLabels(ctx context.Context, linked []repos.LinkedRepo) {
	if !a.capabilities.GH.Authenticated {
		return
	}
	for _, r := range linked {
		if err := a.ensureCodefixLabelForRepo(ctx, r.Key()); err != nil {
			log.Warn("ensure triagent-proposal label", "repo", r.Key(), "err", err)
		}
	}
}

// ensureCodefixLabelForRepo checks one repo and creates the label if
// missing. Uses a direct existence probe via `gh api repos/<r>/labels/<name>`
// — 200 means the label exists, 404 means it doesn't (create it),
// anything else is a real error. We avoid `gh label list --search`
// because that quietly returns empty stdout (not `[]`) on repos
// where the search yields no hits, which trips JSON parsing.
//
// Returns nil when the label already exists or was created
// successfully; an error on any gh call failure other than the
// expected 404.
func (a *apiHandlers) ensureCodefixLabelForRepo(ctx context.Context, repo string) error {
	if a.codefixGh == nil {
		return fmt.Errorf("gh runner not configured")
	}
	_, stderr, err := a.codefixGh.Run(ctx, "api",
		fmt.Sprintf("repos/%s/labels/%s", repo, codefixLabelName),
	)
	if err == nil {
		// 200 OK — label already exists.
		return nil
	}
	errMsg := strings.ToLower(string(stderr))
	if !strings.Contains(errMsg, "not found") && !strings.Contains(errMsg, "404") {
		// Some other failure (auth, network, repo gone). Surface it
		// so the operator sees what's wrong.
		return fmt.Errorf("gh api labels probe: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	// 404 — label doesn't exist on this repo. Create it.
	_, stderr, err = a.codefixGh.Run(ctx, "label", "create", codefixLabelName,
		"--repo", repo,
		"--color", codefixLabelColor,
		"--description", codefixLabelDescription,
	)
	if err != nil {
		return fmt.Errorf("gh label create: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	log.Info("created triagent-proposal label", "repo", repo)
	return nil
}
