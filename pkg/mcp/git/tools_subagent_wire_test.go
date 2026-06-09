package git

import (
	"context"
	"path/filepath"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wireSession connects an in-memory client to the server's MCP impl so a
// tool call goes through the go-sdk's output-schema validation — the layer
// the handler unit tests bypass by calling the method directly.
func wireSession(t *testing.T, s *Server) *sdkmcp.ClientSession {
	t.Helper()
	s.impl = sdkmcp.NewServer(&sdkmcp.Implementation{Name: "wire-test", Version: "v0"}, nil)
	s.register()

	srvT, cliT := sdkmcp.NewInMemoryTransports()
	srvSess, err := s.impl.Connect(context.Background(), srvT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srvSess.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "wire-test", Version: "v0"}, nil)
	cliSess, err := client.Connect(context.Background(), cliT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cliSess.Close() })
	return cliSess
}

// TestWire_AnalyzeChange_CitationsSoftFail_ReturnsEmptyArrayNotNull is the
// faithful reproduction of the reported bug: a sub-agent that emits no
// citations block soft-fails, leaving Citations a nil slice. Marshaled as
// "citations":null it trips the auto-generated output schema (citations is
// a non-nullable array), so the SDK fails the whole call with "validating
// tool output" instead of returning the sub-agent's prose. The tool must
// instead surface the prose with citations as an empty array.
func TestWire_AnalyzeChange_CitationsSoftFail_ReturnsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	cacheDir, _ := initFixtureRepo(t, "o", "n")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	runFixtureGit(t, "", "init", "--bare", bareDir)
	runFixtureGit(t, filepath.Join(cacheDir, "o", "n"), "remote", "add", "origin", bareDir)
	runFixtureGit(t, filepath.Join(cacheDir, "o", "n"), "push", "origin", "main")
	runFixtureGit(t, filepath.Join(cacheDir, "o", "n"), "fetch", "--prune", "--tags")

	s := &Server{
		owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")},
		runSubAgent: func(_ context.Context, _, _, _, _ string) (subagent.Result, error) {
			// No <<<CITATIONS>>> block on either call → citations runner
			// soft-fails after the corrective retry.
			return subagent.Result{Summary: "the change is unrelated."}, nil
		},
	}
	cliSess := wireSession(t, s)

	res, err := cliSess.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "analyze_change",
		Arguments: map[string]any{"ref": "v0.2.0", "question": "what changed?"},
	})
	require.NoError(t, err, "soft-failed citations must not trip output-schema validation")
	require.False(t, res.IsError, "soft-fail should surface prose, not a tool error")

	structured, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok, "structured content should decode to an object")
	cits, present := structured["citations"]
	require.True(t, present, "citations key must be present")
	require.NotNil(t, cits, "citations must be [] not null on soft-fail")
	assert.Empty(t, cits, "soft-fail yields zero citations")
}

// TestWire_AnalyzeChange_MissingArgs_NilCitationsDoesNotBreakSchema covers
// the handler's early-return path: with empty arguments it returns before
// any clone, so no fixture is needed. The returned output struct still has
// a nil Citations slice, and — because the handler reports failures via the
// result (not the Go error return) — the SDK marshals and validates it.
// Pre-fix that validation fails on "citations":null and the handler's
// "ref and question are required" message is lost.
func TestWire_AnalyzeChange_MissingArgs_NilCitationsDoesNotBreakSchema(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{response: []byte("main\n")}}
	cliSess := wireSession(t, s)

	res, err := cliSess.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "analyze_change",
		Arguments: map[string]any{"ref": "", "question": ""},
	})
	require.NoError(t, err, "nil Citations on the early-return path must not trip output-schema validation")
	require.True(t, res.IsError, "missing args must surface as a tool error")
	require.NotEmpty(t, res.Content)
}

// TestWire_DraftPR_MissingIssueURL_NilCitationsDoesNotBreakSchema covers
// draft_pr's early-return path (its draftPROut also carries a non-nullable
// citations array). An invalid issue_url returns before the worktree is
// built, with a nil Citations slice pre-fix.
func TestWire_DraftPR_MissingIssueURL_NilCitationsDoesNotBreakSchema(t *testing.T) {
	t.Parallel()
	s := &Server{owner: "o", name: "n", gh: &stubGh{response: []byte("main\n")}}
	cliSess := wireSession(t, s)

	res, err := cliSess.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "draft_pr",
		Arguments: map[string]any{"issue_url": ""},
	})
	require.NoError(t, err, "nil Citations on draft_pr's early return must not trip output-schema validation")
	require.True(t, res.IsError, "missing issue_url must surface as a tool error")
	require.NotEmpty(t, res.Content)
}
