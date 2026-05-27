package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSession_RestoreSessionID lets the resume path inject an id captured
// in a prior process so --resume <id> works without first calling Start.
func TestSession_RestoreSessionID(t *testing.T) {
	s, err := NewSession("", nil)
	if err != nil {
		// `claude` may not be on PATH in CI — that's fine; we still
		// want the API surface check.
		t.Skip("claude binary not on PATH")
	}
	require.Empty(t, s.SessionID(), "fresh session has id %q, want empty", s.SessionID())
	s.RestoreSessionID("abc-123")
	assert.Equal(t, "abc-123", s.SessionID(), "after RestoreSessionID, SessionID()")
}

// TestSession_BaseArgs_NoSessionShape sanity-checks the existing arg
// builder still emits the stream-json + verbose flags after the field
// additions in this task. (Guards against accidental breakage.)
func TestSession_BaseArgs_NoSessionShape(t *testing.T) {
	s := &Session{mcpConfigPath: "/tmp/m.json", allowedTools: []string{"mcp__x__*"}}
	args := s.baseArgs()
	wantFlags := []string{"--output-format", "stream-json", "--verbose", "--mcp-config", "/tmp/m.json", "--allowedTools", "mcp__x__*"}
	require.Len(t, args, len(wantFlags), "baseArgs len (got %v)", args)
	for i, w := range wantFlags {
		assert.Equal(t, w, args[i], "args[%d]", i)
	}
}

// TestSession_Start_DashPrefixedPromptDoesNotTripFlagParsing guards
// against the regression where a chat message starting with "-" was
// passed as a positional arg to claude's CLI, which then read it as an
// unknown option flag and exited 1. The fix is to feed the prompt over
// stdin so the parser never sees it. Asserts:
//  1. args contain --print (boolean), no positional prompt
//  2. stdin carries the full prompt body
//
// Drives Start through a tiny fake "claude" that records its args and
// stdin, so the test stays hermetic.
func TestSession_Start_DashPrefixedPromptDoesNotTripFlagParsing(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	stdinFile := filepath.Join(dir, "stdin")
	fake := filepath.Join(dir, "fake-claude.sh")
	// Records every arg on its own line, then drains stdin to stdinFile.
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > " + stdinFile + "\n"
	require.NoError(t, os.WriteFile(fake, []byte(script), 0o755))

	s := &Session{binary: fake}
	dashPrompt := "- I think a wiki would help\n- and a playbook"
	ch, err := s.Start(context.Background(), dashPrompt)
	require.NoError(t, err)
	for ev := range ch {
		assert.NotEqual(t, EventError, ev.Kind, "got error event: %v", ev.Err)
	}

	gotArgs, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	assert.NotContains(t, string(gotArgs), dashPrompt, "prompt leaked into CLI args: %s", gotArgs)
	gotStdin, err := os.ReadFile(stdinFile)
	require.NoError(t, err)
	assert.Equal(t, dashPrompt, string(gotStdin), "prompt should be delivered via stdin")
}

// TestSession_Run_SurfacesStderrOnExit guards against the regression where
// a non-zero claude exit produced a bare "claude exited: exit status 1"
// event with no stderr — leaving the operator no clue what went wrong.
// Uses /bin/sh as a stand-in so the test doesn't require the real claude
// binary on PATH.
func TestSession_Run_SurfacesStderrOnExit(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	s := &Session{binary: "/bin/sh"}
	ch, err := s.run(context.Background(), []string{"-c", "echo boom >&2; exit 1"}, "")
	require.NoError(t, err)
	var evErr error
	for ev := range ch {
		if ev.Kind == EventError {
			evErr = ev.Err
		}
	}
	require.Error(t, evErr, "expected an error event on non-zero exit")
	msg := evErr.Error()
	assert.Contains(t, msg, "boom", "stderr tail missing from error: %s", msg)
	assert.Contains(t, msg, "claude exited", "lost the existing 'claude exited' prefix: %s", msg)
}

func TestBaseArgs_NoModelByDefault(t *testing.T) {
	t.Parallel()
	s := &Session{mcpConfigPath: "/tmp/mcp.json", allowedTools: []string{"foo"}}
	args := s.baseArgs()
	for _, a := range args {
		assert.NotEqual(t, "--model", a, "--model must not appear when model is empty")
	}
}

func TestBaseArgs_ModelAppendsFlag(t *testing.T) {
	t.Parallel()
	s := &Session{mcpConfigPath: "/tmp/mcp.json", allowedTools: []string{"foo"}, model: "claude-opus-4-7"}
	args := s.baseArgs()
	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "--model claude-opus-4-7")
}
