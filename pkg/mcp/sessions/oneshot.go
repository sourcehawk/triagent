package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
)

// OneShotDraftOptions configures a RunOneShotDraft call.
type OneShotDraftOptions struct {
	MetadataPath string
	EventsPath   string
	OutPath      string
	ClaudeBinary string
}

// RunOneShotDraft drives the same sub-agent the propose_session_draft MCP
// tool drives, but without an MCP transport. The launcher's
// defaultSessionDrafter shells out to `triagent-mcp sessions-draft` which wraps
// this. Mirrors how the wiki promote/delete CLI helpers wrap shared code.
//
// Investigation transcripts (events.jsonl) can be tens of thousands of
// lines for a long session, so we lift the timeout to 5 minutes (subagent's
// default is 90s) and pass the broader Read,Glob,Grep,Write,Edit tool set
// the wiki drafter uses — the agent benefits from being able to grep
// through the transcript rather than reading the whole file. WorkingDir
// is the proposals dir so the agent has a stable cwd if it ever resolves
// a relative path.
func RunOneShotDraft(ctx context.Context, opts OneShotDraftOptions) error {
	if opts.MetadataPath == "" || opts.EventsPath == "" || opts.OutPath == "" {
		return errors.New("metadata, events, out all required")
	}
	proposalsDir := filepath.Dir(opts.OutPath)
	if err := os.MkdirAll(proposalsDir, 0o700); err != nil {
		return err
	}
	prompt := fmt.Sprintf(draftPromptTemplate,
		opts.OutPath,      // %[1]q — primary write target
		opts.OutPath,      // %[2]q — repeated for the imperative reminder
		opts.MetadataPath, // %[3]s
		opts.EventsPath,   // %[4]s
	)
	res, err := subagent.Run(ctx, subagent.Options{
		ClaudeBinary: opts.ClaudeBinary,
		WorkingDir:   proposalsDir,
		AllowedTools: "Read,Glob,Grep,Write,Edit",
		Prompt:       prompt,
		Timeout:      5 * time.Minute,
	})
	if err != nil {
		// Surface the sub-agent's last words so the operator can see
		// *why* it failed (e.g. "I couldn't find the events file"),
		// not just the bare exec error.
		return fmt.Errorf("%w (agent summary: %s)", err, summaryTail(res.Summary))
	}
	if _, statErr := os.Stat(opts.OutPath); statErr != nil {
		return fmt.Errorf("drafter ran but did not write %s: %w (agent summary: %s)", opts.OutPath, statErr, summaryTail(res.Summary))
	}
	return nil
}

// summaryTail returns the trailing portion of the sub-agent's accumulated
// text output, capped so an unwieldy response doesn't bloat the error.
// Empty becomes "(empty)" so the error never reads "agent summary: " with
// nothing after it.
func summaryTail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	const maxLen = 1200
	if len(s) <= maxLen {
		return s
	}
	return "…" + s[len(s)-maxLen:]
}
