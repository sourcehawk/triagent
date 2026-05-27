package subagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeFakeClaudeWithStream writes a fake claude binary that emits a
// canned stream-json line (carrying the supplied session_id in a
// `system` event) and exits 0. Returns the script path.
func writeFakeClaudeWithStream(t *testing.T, sessionID string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	// The fake claude needs to:
	//   1. Drain stdin so the parent's strings.NewReader(opts.Prompt) closes cleanly.
	//   2. Echo argv to ./args (one arg per line) for the resumption-test to inspect.
	//   3. Emit one stream-json `system` event with the given session_id so
	//      Run() can extract it into Result.SessionID.
	script := "#!/usr/bin/env bash\nset -euo pipefail\ncat > /dev/null\nprintf '%s\\n' \"$@\" > \"" + dir + "/args\"\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"" + sessionID + "\"}'\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return bin
}

// readFakeClaudeArgsAt reads the recorded argv from a fake-claude run.
// Returns one element per argument (newline-split).
func readFakeClaudeArgsAt(t *testing.T, bin string) []string {
	t.Helper()
	argPath := filepath.Join(filepath.Dir(bin), "args")
	data, err := os.ReadFile(argPath)
	require.NoError(t, err, "fake claude args file missing")
	// Trim trailing newline.
	lines := []string{}
	cur := ""
	for _, b := range data {
		if b == '\n' {
			if cur != "" || len(lines) > 0 {
				lines = append(lines, cur)
				cur = ""
			}
		} else {
			cur += string(b)
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// TestRun_ExtractsSessionIDFromSystemEvent verifies that Run() captures
// the session_id from claude's first system event and surfaces it on
// Result.SessionID. Callers thread this back as Options.ResumeSessionID
// on subsequent runs to continue the same conversation rather than
// spinning up a fresh Claude session each time.
func TestRun_ExtractsSessionIDFromSystemEvent(t *testing.T) {
	t.Parallel()
	bin := writeFakeClaudeWithStream(t, "sess-abc-123")
	res, err := Run(context.Background(), Options{
		ClaudeBinary: bin,
		AllowedTools: "Read",
		Prompt:       "x",
		WorkingDir:   t.TempDir(),
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, "sess-abc-123", res.SessionID,
		"first sub-agent invocation must surface session_id from stream-json")
}

// TestRun_PassesResumeSessionIDToClaude verifies that when
// Options.ResumeSessionID is set, the spawned claude is invoked with
// `--resume <id>` so it continues the prior conversation. Without
// this, every retry is a fresh session and loses all context the
// prior turn built (file edits, tool calls, prior reasoning).
func TestRun_PassesResumeSessionIDToClaude(t *testing.T) {
	t.Parallel()
	bin := writeFakeClaudeWithStream(t, "sess-x")
	_, err := Run(context.Background(), Options{
		ClaudeBinary:     bin,
		AllowedTools:     "Read",
		Prompt:           "x",
		WorkingDir:       t.TempDir(),
		Timeout:          5 * time.Second,
		ResumeSessionID: "sess-prior-999",
	})
	require.NoError(t, err)
	args := readFakeClaudeArgsAt(t, bin)
	idx := indexOf(args, "--resume")
	require.GreaterOrEqual(t, idx, 0, "expected --resume flag in args; got %v", args)
	require.Equal(t, "sess-prior-999", args[idx+1],
		"--resume must carry the prior session_id verbatim")
}

// TestRun_OmitsResumeWhenSessionIDIsEmpty — back-compat. When no
// resume ID is given (the default for first-call sites), claude is
// invoked without --resume and starts a fresh session.
func TestRun_OmitsResumeWhenSessionIDIsEmpty(t *testing.T) {
	t.Parallel()
	bin := writeFakeClaudeWithStream(t, "sess-fresh")
	_, err := Run(context.Background(), Options{
		ClaudeBinary: bin,
		AllowedTools: "Read",
		Prompt:       "x",
		WorkingDir:   t.TempDir(),
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	args := readFakeClaudeArgsAt(t, bin)
	require.Less(t, indexOf(args, "--resume"), 0,
		"empty ResumeSessionID must NOT pass --resume; got args %v", args)
}
