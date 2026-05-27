// Package git implements the triagent-mcp `git` MCP server: a per-linked-repo
// surface over a local cache clone of a GitHub repository. Two tool tiers
// share the surface:
//
//   - Discovery tools — cheap deterministic git/gh calls (latest_tags,
//     commit_summary, diff_summary, search_log) the parent investigation
//     agent uses to surface candidate commits/tags with low context cost.
//   - Sub-agent tools — analyze_change, correlate_with_findings — spawn an
//     ephemeral `claude` subprocess against the cloned repo to do real
//     code reading and return a focused summary. The sub-agent's own
//     internal events are forwarded as nested telemetry so the launcher's
//     activity panel renders them as collapsed children of the parent.
//
// One triagent-mcp process per linked repo. The repo cache lives outside the
// session under $XDG_CACHE_HOME/triagent-mcp/git/<owner>/<name> so repeat
// investigations against the same repo skip the clone cost.
package git

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the git MCP server.
type Options struct {
	// Repo is the GitHub repo identifier in `owner/name` form.
	Repo string
	// CacheDir is the parent directory for cached clones. When empty, the
	// server resolves $XDG_CACHE_HOME/triagent-mcp/git (or ~/.cache/triagent-mcp/git).
	CacheDir string
	// ClaudeBinary is the path to the `claude` CLI used by sub-agent
	// tools. Empty falls back to looking up `claude` on $PATH at call
	// time.
	ClaudeBinary string
	// FilterPrereleases hides semver-shaped prerelease tags
	// (`MAJOR.MINOR.PATCH-<suffix>`) from latest_tags by default.
	// `-SNAPSHOT` suffixes are always kept as useful "what's bleeding edge"
	// references. Per-call agents can override via the tool's
	// `include_prereleases` input.
	FilterPrereleases bool
	// CodefixTimeoutMinutes overrides the default draft_pr wall-clock
	// cap (30 min). 0 = use default.
	CodefixTimeoutMinutes int
}

// Server holds the MCP server and its repo configuration.
type Server struct {
	impl              *mcp.Server
	owner             string
	name              string
	cacheDir          string
	claudeBin         string
	filterPrereleases bool
	// codefixTimeoutMinutes is the per-repo override for draft_pr.
	// 0 means use the hardcoded default (30 min).
	codefixTimeoutMinutes int

	// gh is the seam used by GitHub CLI tools. The default (set in New)
	// calls the real `gh` binary; tests replace it with a stub.
	gh ghRunner

	// runSubAgent is the seam used by sub-agent tool tests. The default
	// (set in New) calls subagent.Run; tests replace it with a stub that
	// bypasses subprocess spawning. resumeSessionID, when non-empty, is
	// passed to claude as --resume so the citation-correction retry
	// continues the prior conversation instead of re-paying the cost of
	// orientation + tool inventory + repo skim that the first turn did.
	runSubAgent func(ctx context.Context, repoPath, prompt, parentToolID, resumeSessionID string) (subagent.Result, error)

	// runDraftPRSubAgent is the seam for draft_pr. Broader Bash(*)
	// allowlist + deny-list for git push / gh pr create. Tests inject a
	// stub here; production points to runDraftPRSubAgentReal. The
	// resumeSessionID parameter is empty for the first invocation and
	// carries the prior call's session_id for retries — keeps the
	// citation-correction retry on the same Claude conversation rather
	// than spawning a fresh session that loses all context.
	runDraftPRSubAgent func(ctx context.Context, repoPath, prompt, parentToolID, resumeSessionID string) (subagent.Result, error)

	// runRecoverySubAgent is the seam for the commit-or-cleanup retry
	// fired when the main sub-agent leaves the worktree dirty without
	// committing. Narrower allowlist (only git invocations + rm), short
	// timeout. resumeSessionID resumes the main sub-agent's session so
	// the recovery agent has memory of which files were edited and why.
	// Tests stub here; production points to runRecoverySubAgentReal.
	runRecoverySubAgent func(ctx context.Context, repoPath, prompt, parentToolID, resumeSessionID string) (subagent.Result, error)

	// worktreeRootOverride lets tests scope worktree dirs to t.TempDir()
	// so parallel tests don't share state via the global tmpfs path.
	// Production sets this empty; the Server's worktreeRoot() method
	// then falls back to $XDG_RUNTIME_DIR/triagent-mcp-git/codefix.
	worktreeRootOverride string

	// Serialises clone/fetch so concurrent first-tool-call invocations
	// don't race on the cache directory.
	cloneOnce sync.Mutex

	// defaultBranchOnce serializes the first `gh repo view` probe so
	// concurrent first-tool-call invocations don't each shell out.
	// defaultBranchName is the cached result — either the resolved name
	// or "main" on probe failure. Both are written exactly once inside
	// the Once.Do closure.
	defaultBranchOnce sync.Once
	defaultBranchName string
}

// New constructs a Server. Repo is required; everything else has defaults.
func New(opts Options) (*Server, error) {
	owner, name, err := parseRepo(opts.Repo)
	if err != nil {
		return nil, err
	}
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		d, err := defaultCacheDir()
		if err != nil {
			return nil, fmt.Errorf("resolve cache dir: %w", err)
		}
		cacheDir = d
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-git",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:                  impl,
		owner:                 owner,
		name:                  name,
		cacheDir:              cacheDir,
		claudeBin:             opts.ClaudeBinary,
		filterPrereleases:     opts.FilterPrereleases,
		codefixTimeoutMinutes: opts.CodefixTimeoutMinutes,
		gh:                    realGhRunner{},
	}
	s.runSubAgent = s.runSubAgentReal
	s.runDraftPRSubAgent = s.runDraftPRSubAgentReal
	s.runRecoverySubAgent = s.runRecoverySubAgentReal
	s.register()
	// Best-effort cleanup of worktree directories left by previous
	// crashed processes. Runs in a goroutine so it doesn't block the
	// MCP from accepting requests; first cleanup may briefly race with
	// a fresh draft_pr but worktree creation tolerates a missing prune.
	go func() {
		if dir, err := s.EnsureClone(context.Background()); err == nil {
			_ = s.sweepStaleWorktrees(context.Background(), dir, time.Hour)
		}
	}()
	return s, nil
}

// Run serves MCP requests over stdio until the client disconnects or ctx is
// cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

func parseRepo(raw string) (owner, name string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("--repo is required (owner/name)")
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("--repo must be owner/name, got %q", raw)
	}
	return parts[0], parts[1], nil
}

// errorResult formats a tool-level error so the MCP protocol returns it as
// a tool failure rather than a transport error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// register wires every tool the git server exposes. Each handler is
// wrapped in telemetry.Wrap so the launcher's activity panel sees the
// real call boundaries.
func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "latest_tags",
		Description: "Recent tags on the linked repo with commit subjects. Use to spot newly-shipped releases that may correlate with an incident's symptom_window. Default limit 10.",
	}, telemetry.Wrap("latest_tags", s.latestTags))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "commit_summary",
		Description: "Metadata + file-stat summary of a single commit/ref. Use after latest_tags or search_log to inspect a candidate change without reading its diff.",
	}, telemetry.Wrap("commit_summary", s.commitSummary))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "diff_summary",
		Description: "Aggregate stats and commit list between two refs (e.g. previous_tag..tag). Use to compare what shipped between releases.",
	}, telemetry.Wrap("diff_summary", s.diffSummary))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "search_log",
		Description: "Search commit messages by grep pattern over a time window. Use to find changes mentioning a symptom keyword (e.g. 'timeout', 'incident', 'backpressure').",
	}, telemetry.Wrap("search_log", s.searchLog))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "analyze_change",
		Description: "Spawn a focused sub-agent to read the actual code change at a ref and answer a specific question about it. Use after commit_summary identifies a candidate but you need to know what the change actually does. Returns a natural-language summary; sub-agent's internal tool calls render as collapsed children in the activity panel.",
	}, telemetry.Wrap("analyze_change", s.analyzeChange))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "correlate_with_findings",
		Description: "Spawn a sub-agent to scan recent changes against a free-form description of incident findings and rank up to 5 candidates by likelihood of being the cause. Use early when 'something shipped recently' is a strong hypothesis but you don't have a specific ref yet.",
	}, telemetry.Wrap("correlate_with_findings", s.correlateWithFindings))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "get_repo_architecture_summary",
		Description: "Read the cached architecture summary for this repo. Cheap first-stop before commit_summary / search_log / analyze_change. Returns {exists, generated_at, content, hint, error}; the hint points at the LinkedRepos description fallback when no summary is yet cached.",
	}, telemetry.Wrap("get_repo_architecture_summary", s.getRepoArchitectureSummary))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "search_issues",
		Description: "Search issues in this repo by free-text query. Use before drafting a new GH issue from an investigation finding to spot duplicates. Default state=open, limit=10 (cap 30).",
	}, telemetry.Wrap("search_issues", s.searchIssues))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "list_issue_types",
		Description: "List the GitHub Issue Types configured on this repo's org. Call before create_github_issue so the agent can pick a sensible issue_type — when enabled=false the org hasn't opted in, and create_github_issue should be called without issue_type.",
	}, telemetry.Wrap("list_issue_types", s.listIssueTypes))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "create_github_issue",
		Description: "File a GitHub issue in this repo from an investigation finding. The triagent-proposal label is auto-attached for downstream tracking. Pass issue_type (after list_issue_types) when the repo's org has Issue Types enabled; pass cross_repo_refs when filing sibling issues across multiple linked repos for the same investigation.\n\n" + issueBodyShape + "\nScope rule: do NOT embed scope directives for the codefix sub-agent (\"don't bundle X\", \"don't touch repo Y\") inside the body; that guidance belongs in draft_pr's extra_prompt and is unnecessary across repos anyway since each triagent-git MCP is scoped to a single repo.",
	}, telemetry.Wrap("create_github_issue", s.createGithubIssue))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "draft_pr",
		Description: "Spawn a sub-agent to author a code/config/docs change for the linked GH issue and open a draft PR. Use after create_github_issue when the investigation has produced a bounded change opportunity. The sub-agent runs TDD where applicable; the host owns push and PR creation. Long-running (up to 30 min default) — emits status_update events for live progress. Single-repo scope: this MCP serves one linked repo; the sub-agent has no access to sibling repos. Sibling-repo work (e.g. an alerts-repo rule split AND a runbook expansion) requires a separate draft_pr call against each repo's own triagent-git-<alias> MCP — call them sequentially or in parallel as the operator prefers; do not bake cross-repo guardrails (\"don't touch repo X\") into extra_prompt since the sub-agent already can't.",
	}, telemetry.Wrap("draft_pr", s.draftPR))
}
