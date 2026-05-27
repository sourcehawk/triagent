package git

import (
	"context"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
)

// runSubAgentReal is the production wiring for Server.runSubAgent. Tests
// replace s.runSubAgent with a stub that bypasses subprocess spawning;
// callers always go through s.runSubAgent so the stub takes effect.
// resumeSessionID continues the prior turn's claude conversation when
// non-empty — used by analyze_change / correlate_with_findings to keep
// the citation-correction retry on the same Claude session.
func (s *Server) runSubAgentReal(ctx context.Context, repoPath, prompt, parentToolID, resumeSessionID string) (subagent.Result, error) {
	return subagent.Run(ctx, subagent.Options{
		ClaudeBinary:    s.claudeBin,
		WorkingDir:      repoPath,
		AllowedTools:    "Read,Glob,Grep,Bash(git *),Bash(gh pr view *)",
		Prompt:          prompt,
		ParentToolID:    parentToolID,
		ResumeSessionID: resumeSessionID,
	})
}

// runDraftPRSubAgentReal is the production wiring for draft_pr.
// Broader Bash(*) allowlist (the worktree IS the sandbox), with
// explicit denies for git push and gh pr create — those belong to
// the host. Per-repo timeout override on Server.codefixTimeoutMinutes
// drives the wall-clock cap; 0 falls back to subagent.Run's default
// (which the draft_pr handler bumps explicitly via context.WithTimeout).
// resumeSessionID, when non-empty, continues the prior turn's claude
// conversation so the citation-correction retry doesn't lose context.
func (s *Server) runDraftPRSubAgentReal(ctx context.Context, repoPath, prompt, parentToolID, resumeSessionID string) (subagent.Result, error) {
	var timeout time.Duration
	if s.codefixTimeoutMinutes > 0 {
		timeout = time.Duration(s.codefixTimeoutMinutes) * time.Minute
	}
	return subagent.Run(ctx, subagent.Options{
		ClaudeBinary:    s.claudeBin,
		WorkingDir:      repoPath,
		AllowedTools:    "Read,Edit,Write,Glob,Grep,Bash(*)",
		DisallowedTools: "Bash(git push:*),Bash(git push),Bash(gh pr create:*),Bash(gh pr create)",
		Prompt:          prompt,
		ParentToolID:    parentToolID,
		Timeout:         timeout,
		ResumeSessionID: resumeSessionID,
	})
}

// recoveryTimeout caps the commit-or-cleanup retry at 2 min. The work is
// either `git add` + `git commit` OR `git restore` + `rm` plus a one-line
// decision marker; that should never take longer. A hung recovery agent
// blocking on something else is a clearer signal than waiting 30 min.
const recoveryTimeout = 2 * time.Minute

// runRecoverySubAgentReal is the production wiring for the
// commit-or-cleanup recovery sub-agent. Tightly scoped tool surface:
// the agent can `git add`, `git commit`, `git restore`, `git log` and
// `rm` files — but cannot edit/write new content (that was the previous
// turn's job) and cannot push or interact with GitHub.
// resumeSessionID resumes the main sub-agent's session so the recovery
// agent has memory of which files were edited and why — without it,
// the recovery agent starts cold and has to re-orient from the prompt
// alone.
func (s *Server) runRecoverySubAgentReal(ctx context.Context, repoPath, prompt, parentToolID, resumeSessionID string) (subagent.Result, error) {
	return subagent.Run(ctx, subagent.Options{
		ClaudeBinary:    s.claudeBin,
		WorkingDir:      repoPath,
		AllowedTools:    "Read,Bash(git add:*),Bash(git commit:*),Bash(git restore:*),Bash(git log:*),Bash(git status:*),Bash(rm:*)",
		Prompt:          prompt,
		ParentToolID:    parentToolID,
		Timeout:         recoveryTimeout,
		ResumeSessionID: resumeSessionID,
	})
}
