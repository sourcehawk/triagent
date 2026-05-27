package git

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
)

// GenerateOptions configures a single architecture-summary generation run.
// Required: GitCacheDir, RepoPath, Owner, Name, Kind. Description and
// Focus are optional but high-leverage when present — see
// ArchitectureSummaryPromptArgs.Description for the rationale.
type GenerateOptions struct {
	GitCacheDir string // root for cache files; SummaryPath(GitCacheDir, …) is where output lands
	RepoPath    string // absolute path to the cloned repo (sub-agent's working dir)
	Owner       string // GitHub owner
	Name        string // GitHub repo name
	Kind        string // "freeform" for v1
	// Description is the operator-authored connection-time description.
	// Surfaced to the sub-agent so it can self-allocate emphasis based
	// on what the repo is for (replaces per-kind prompt templates).
	Description string
	Focus       string // optional operator hint, narrower than Description
	// OperatorEditsDiff is a unified diff (git format) representing the
	// operator's hand-edits to the prior cached summary, computed by the
	// launcher before invoking us. When non-empty, surfaced to the
	// sub-agent as advisory context — the agent decides per-hunk whether
	// the edit still applies and incorporates it into the fresh output
	// when so. Empty means no prior edits or no prior baseline.
	OperatorEditsDiff string
	Model        string                // model name written to frontmatter (informational)
	Timeout      time.Duration         // sub-agent timeout; 0 → 15 * time.Minute
	ClaudeBinary string                // ClaudeBinary path; empty falls back to $PATH lookup at sub-agent launch time. Only consulted when Runner is nil.
	Now          func() time.Time      // injected clock for tests; nil → time.Now
	Runner       summarySubAgentRunner // injected sub-agent runner; nil → real subagent.Run
}

// GenerateResult is what the generator reports back to the caller.
type GenerateResult struct {
	Path        string
	GeneratedAt time.Time
	ByteCount   int
	TimedOut    bool
}

// summarySubAgentRunner is the test seam for the sub-agent runner. Production
// wires it to subagent.Run; tests pass a stub that returns canned output.
type summarySubAgentRunner interface {
	Run(ctx context.Context, opts subagent.Options) (subagent.Result, error)
}

// realSubAgentRunner adapts subagent.Run to the summarySubAgentRunner interface.
type realSubAgentRunner struct{ ClaudeBinary string }

func (r realSubAgentRunner) Run(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
	if opts.ClaudeBinary == "" {
		opts.ClaudeBinary = r.ClaudeBinary
	}
	return subagent.Run(ctx, opts)
}

// DefaultGenerateTimeout is the v1 cap. Architecture extraction is repo-wide
// work, meaningfully heavier than analyze_change's single-commit scope.
const DefaultGenerateTimeout = 15 * time.Minute

// GenerateArchitectureSummary runs the sub-agent, writes the cache file, and
// returns metadata. It does NOT return the sub-agent's error to the caller —
// failures are captured in the cache file's `error:` frontmatter so callers
// (CLI, launcher worker) treat all generation outcomes uniformly. Returns
// non-nil error only for unrecoverable problems (cache-write failure, etc).
func GenerateArchitectureSummary(ctx context.Context, opts GenerateOptions) (GenerateResult, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultGenerateTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = realSubAgentRunner{ClaudeBinary: opts.ClaudeBinary}
	}

	prompt := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo:              opts.Owner + "/" + opts.Name,
		Kind:              opts.Kind,
		Description:       opts.Description,
		Focus:             opts.Focus,
		OperatorEditsDiff: opts.OperatorEditsDiff,
	})

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, err := runner.Run(subCtx, subagent.Options{
		WorkingDir:   opts.RepoPath,
		AllowedTools: "Read,Glob,Grep,Bash(git *)",
		Prompt:       prompt,
		Timeout:      timeout,
	})

	path := SummaryPath(opts.GitCacheDir, opts.Owner, opts.Name)
	when := now().UTC()

	if err != nil {
		timedOut := errors.Is(err, context.DeadlineExceeded)
		errMsg := err.Error()
		if timedOut {
			errMsg = fmt.Sprintf("sub-agent timeout after %s", timeout)
		}
		// Don't clobber a real cache file with an error-variant write.
		// If the existing file holds AI content or operator hand-edits
		// (anything with an empty Error frontmatter), the regen failure
		// shouldn't destroy it — the operator can retry, and the SSE
		// phase=error event already surfaces the failure to the UI.
		// We only write the error variant when there's nothing worth
		// preserving: file absent, or already an error variant from a
		// prior failed run (overwriting the old error message with the
		// new one is fine).
		if existing, readErr := ReadSummary(path); readErr == nil && existing.Frontmatter.Error == "" {
			return GenerateResult{Path: path, GeneratedAt: when, TimedOut: timedOut}, nil
		}
		errFile := &SummaryFile{
			Frontmatter: SummaryFrontmatter{
				GeneratedAt: when,
				Kind:        opts.Kind,
				Focus:       opts.Focus,
				Model:       opts.Model,
				Error:       errMsg,
			},
			Body: fmt.Sprintf("> Generation failed: %s\n", errMsg),
		}
		if writeErr := WriteSummary(path, errFile); writeErr != nil {
			return GenerateResult{}, fmt.Errorf("write error variant: %w", writeErr)
		}
		return GenerateResult{Path: path, GeneratedAt: when, TimedOut: timedOut}, nil
	}

	// The sub-agent's raw response can include preamble before its first
	// H1 ("I have enough context, let me produce the summary…") and
	// trailing commentary after the last section ("let me know if you'd
	// like more detail"). Both pollute the cached body. The prompt asks
	// the agent to wrap the real body in BEGIN_SUMMARY/SUMMARY>>>
	// sentinels; extractSummaryBody pops the content between them and
	// degrades gracefully when the agent ignored the instruction.
	body := extractSummaryBody(res.Summary)
	out := &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: when,
			Kind:        opts.Kind,
			Focus:       opts.Focus,
			Model:       opts.Model,
			ByteCount:   len(body),
		},
		Body: body,
	}
	if err := WriteSummary(path, out); err != nil {
		return GenerateResult{}, fmt.Errorf("write summary: %w", err)
	}
	// Snapshot the raw body to the baseline path so the next regen
	// has a reference for diffing operator hand-edits. Best-effort:
	// a failed baseline write doesn't fail the run — the active file
	// is what matters; a missing baseline just means the next regen
	// can't surface operator edits as context.
	baselinePath := BaselinePath(opts.GitCacheDir, opts.Owner, opts.Name)
	_ = WriteBaselineBody(baselinePath, body)
	return GenerateResult{Path: path, GeneratedAt: when, ByteCount: len(body)}, nil
}
