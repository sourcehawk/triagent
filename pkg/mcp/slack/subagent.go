package slack

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
)

// runSubAgentReal is the production wiring for Server.runSubAgent. Tests
// replace s.runSubAgent with a stub that bypasses subprocess spawning;
// production points it here.
//
// The sub-agent runs in workingDir = the per-channel cache directory the
// caller resolved (Store.Dir() for the channel the agent named) with
// `Read,Glob,Grep` allowed — no Bash, no network. Slack content can be
// sensitive and the sub-agent has no business shelling out from this
// surface.
//
// Convention: err is non-nil ONLY when no useful summary was captured.
// Graceful timeouts (TimedOut=true with a partial Summary streamed before
// the deadline) return nil err so consumers can surface the partial
// without juggling a value-with-error tuple.
func (s *Server) runSubAgentReal(ctx context.Context, prompt, parentToolID, workingDir, resumeSessionID string) (subAgentResult, error) {
	res, err := subagent.Run(ctx, subagent.Options{
		ClaudeBinary:    s.claudeBin,
		WorkingDir:      workingDir,
		AllowedTools:    "Read,Glob,Grep",
		Prompt:          prompt,
		Timeout:         s.subAgentTimeout,
		ParentToolID:    parentToolID,
		ResumeSessionID: resumeSessionID,
	})
	//goland:noinspection ALL
	summary, timedOut, sessionID := res.Summary, res.TimedOut, res.SessionID
	if err != nil && summary == "" {
		return subAgentResult{}, err
	}
	return subAgentResult{Summary: summary, TimedOut: timedOut, SessionID: sessionID}, nil
}
