package cloud

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecCLINeverUsesShell is the source-level half of the no-shell guarantee:
// the exec core must never construct a shell command. There is no "-c" string,
// no "sh -c", no "bash -c" anywhere in harness.go.
func TestExecCLINeverUsesShell(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("harness.go")
	require.NoError(t, err)
	for _, banned := range []string{`"-c"`, "sh -c", "bash -c", `"sh"`, `"bash"`} {
		assert.False(t, bytes.Contains(src, []byte(banned)),
			"harness.go must never construct a shell command; found %q", banned)
	}
}

// TestExecCLIMetacharactersAreInert is the behavioural half: shell
// metacharacters handed to a binary as argv tokens are literal arguments, never
// interpreted. Running /bin/echo with metacharacter tokens prints them verbatim
// and spawns no second process.
func TestExecCLIMetacharactersAreInert(t *testing.T) {
	t.Parallel()
	argv := []string{";", "echo", "pwned", "|", "$(whoami)", "&&", "`id`"}
	r, err := execCLI(context.Background(), "/bin/echo", argv, nil, 4096)
	require.NoError(t, err)
	got := strings.TrimRight(r.Stdout, "\n")
	want := strings.Join(argv, " ")
	require.Equal(t, want, got, "metacharacters were not inert")
	assert.False(t, strings.Contains(r.Stdout, "pwned\n") && got != want,
		"a second process appears to have run")
}

// TestExecCLITruncates caps output at the byte limit and flags truncation.
func TestExecCLITruncates(t *testing.T) {
	t.Parallel()
	r, err := execCLI(context.Background(), "/bin/echo", []string{strings.Repeat("x", 100)}, nil, 10)
	require.NoError(t, err)
	assert.True(t, r.Truncated, "expected Truncated, got %+v", r)
	assert.LessOrEqual(t, len(r.Stdout), 10, "output exceeded limit")
}

// TestExecCLIMinimalEnv confirms the subprocess runs with the caller's explicit
// env, not the parent process environment, so a poisoned PATH cannot redirect
// the resolved binary and ambient secrets do not leak in.
func TestExecCLIMinimalEnv(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_HARNESS_LEAK_CANARY", "should-not-appear")
	r, err := execCLI(context.Background(), "/usr/bin/env", nil, []string{"FOO=bar"}, 4096)
	require.NoError(t, err)
	assert.NotContains(t, r.Stdout, "TRIAGENT_CLOUD_HARNESS_LEAK_CANARY",
		"subprocess inherited the parent environment; env must be explicit")
	assert.Contains(t, r.Stdout, "FOO=bar", "explicit env not applied")
}
