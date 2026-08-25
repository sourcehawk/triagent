package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/stretchr/testify/require"
)

// TestRun_DefaultTimeoutIs5Min confirms the package default. The
// previous 90s default was too tight for analyze_change /
// correlate_with_findings on large repos and is wildly too tight for
// draft_pr; 5 min is the new floor (per-call overrides go higher).
func TestRun_DefaultTimeoutIs5Min(t *testing.T) {
	t.Parallel()
	require.Equal(t, 5*time.Minute, defaultTimeout)
}

func TestRun_RequiresAllowedTools(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Options{
		Prompt:     "ignored",
		WorkingDir: t.TempDir(),
	})
	require.Error(t, err, "expected error when AllowedTools is empty")
	require.ErrorContains(t, err, "AllowedTools is required")
}

// TestRun_DisallowedToolsOptional confirms DisallowedTools is optional.
// AllowedTools stays mandatory; DisallowedTools empty is fine. We rely
// on Run failing fast (subprocess exec error) when the binary doesn't
// exist — we just need to confirm the validation path doesn't reject
// missing DisallowedTools.
func TestRun_DisallowedToolsOptional(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Options{
		ClaudeBinary: "/nonexistent/binary/that/does/not/exist",
		AllowedTools: "Read",
		Prompt:       "x",
		WorkingDir:   t.TempDir(),
		// DisallowedTools intentionally omitted.
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "DisallowedTools")
}

// TestRun_PassesDisallowedToolsToClaude verifies that when set, the
// value flows into the spawned claude args. Uses a fake claude binary
// that just records its argv to a file so we can inspect.
func TestRun_PassesDisallowedToolsToClaude(t *testing.T) {
	t.Parallel()
	fake := writeFakeClaude(t)
	_, err := Run(context.Background(), Options{
		ClaudeBinary:    fake,
		AllowedTools:    "Read,Bash(*)",
		DisallowedTools: "Bash(git push:*),Bash(gh pr create:*)",
		Prompt:          "x",
		WorkingDir:      t.TempDir(),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	args := readFakeClaudeArgs(t, fake)
	idx := indexOf(args, "--disallowedTools")
	require.GreaterOrEqual(t, idx, 0, "expected --disallowedTools in args; got %v", args)
	require.Equal(t, "Bash(git push:*),Bash(gh pr create:*)", args[idx+1])
}

// writeFakeClaude writes a tiny shell script into t.TempDir() that
// echoes its argv to a sibling 'args' file and exits 0. Returns the
// script path so callers can pass it as ClaudeBinary.
func writeFakeClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	argDump := filepath.Join(dir, "args")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\" >> " + argDump + "; done\n# Drain stdin so the parent's writer doesn't block.\ncat > /dev/null\nexit 0\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return bin
}

func readFakeClaudeArgs(t *testing.T, claudePath string) []string {
	t.Helper()
	dump := filepath.Join(filepath.Dir(claudePath), "args")
	data, err := os.ReadFile(dump)
	require.NoError(t, err)
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}

// TestRun_PassesModelFlagWhenSet verifies that when Model is set, the
// value flows into the spawned claude args as --model.
func TestRun_PassesModelFlagWhenSet(t *testing.T) {
	t.Parallel()
	fake := writeFakeClaude(t)
	_, err := Run(context.Background(), Options{
		ClaudeBinary: fake,
		AllowedTools: "Bash(echo)",
		Prompt:       "hi",
		Model:        "claude-haiku-4-5-20251001",
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	args := readFakeClaudeArgs(t, fake)
	idx := indexOf(args, "--model")
	require.GreaterOrEqual(t, idx, 0, "expected --model in args; got %v", args)
	require.Equal(t, "claude-haiku-4-5-20251001", args[idx+1])
}

// TestRun_OmitsModelFlagWhenEmpty verifies that when Model is empty,
// the --model flag is not passed to claude (it inherits the parent).
func TestRun_OmitsModelFlagWhenEmpty(t *testing.T) {
	t.Parallel()
	fake := writeFakeClaude(t)
	_, err := Run(context.Background(), Options{
		ClaudeBinary: fake,
		AllowedTools: "Bash(echo)",
		Prompt:       "hi",
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	args := readFakeClaudeArgs(t, fake)
	idx := indexOf(args, "--model")
	require.Equal(t, -1, idx, "expected --model to be absent when Model is empty; got %v", args)
}

// TestRun_UsesMCPConfigPathWhenSet covers the dispatch path where the
// sub-agent must be able to call back into parent MCPs (e.g. the
// playbook_proposal sub-agent calling
// mcp__triagent-strategies__playbook_proposal_draft). When the caller
// passes MCPConfigPath, the runner forwards a SANITIZED copy as
// --mcp-config — same server set as the source, but with telemetry env
// vars stripped from each server's env block so the sub-agent's spawned
// MCPs don't phone home directly with parent-trace attribution and end
// up as siblings of the dispatching tool in the activity panel.
func TestRun_UsesMCPConfigPathWhenSet(t *testing.T) {
	t.Parallel()
	fake := writeFakeClaude(t)
	cfgPath := filepath.Join(t.TempDir(), "parent-mcp.json")
	source := `{
  "mcpServers": {
    "triagent-strategies": {
      "command": "/usr/local/bin/triagent-mcp",
      "args": ["serve", "--kind=strategies"],
      "env": {
        "TRIAGENT_MCP_SESSION_DIR": "/some/dir",
        "TRIAGENT_MCP_TELEMETRY_URL": "http://launcher.local/api/internal/tool-events",
        "TRIAGENT_MCP_TELEMETRY_TOKEN": "secret",
        "TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX": "mcp__triagent-strategies__",
        "TRIAGENT_MCP_TRACE_ID": "trace-abc"
      }
    }
  }
}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(source), 0o600))

	_, err := Run(context.Background(), Options{
		ClaudeBinary:  fake,
		AllowedTools:  "Read",
		Prompt:        "x",
		MCPConfigPath: cfgPath,
		Timeout:       5 * time.Second,
	})
	require.NoError(t, err)
	args := readFakeClaudeArgs(t, fake)
	idx := indexOf(args, "--mcp-config")
	require.GreaterOrEqual(t, idx, 0, "expected --mcp-config in args; got %v", args)
	forwarded := args[idx+1]
	require.NotEqual(t, cfgPath, forwarded,
		"MCPConfigPath must be sanitized before forwarding so telemetry vars are stripped, not forwarded verbatim")

	body, err := os.ReadFile(forwarded)
	require.NoError(t, err)
	s := string(body)
	require.NotContains(t, s, "TRIAGENT_MCP_TELEMETRY_URL",
		"sanitized config must omit TELEMETRY_URL or sub-agent's MCPs will post tool events to the launcher with parent trace id and no parent_tool_id, rendering as flat siblings in the activity panel")
	require.NotContains(t, s, "TRIAGENT_MCP_TELEMETRY_TOKEN", "telemetry token must not leak to sub-agent MCPs")
	require.NotContains(t, s, "TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX", "telemetry tool prefix must be stripped")
	require.Contains(t, s, "TRIAGENT_MCP_SESSION_DIR", "non-telemetry env (SESSION_DIR) must pass through")
	require.Contains(t, s, "TRIAGENT_MCP_TRACE_ID", "TRACE_ID is not telemetry; preserved for cross-MCP correlation")
	require.Contains(t, s, "triagent-strategies", "sanitized config must keep the server entries")
}

// TestRun_UsesEmptyConfigWhenMCPConfigPathOmitted locks in that the
// existing isolation guarantee for non-dispatch callers (git, wiki,
// citation runner) is unchanged: an unset MCPConfigPath still produces
// the empty-mcp.json --mcp-config value.
func TestRun_UsesEmptyConfigWhenMCPConfigPathOmitted(t *testing.T) {
	t.Parallel()
	fake := writeFakeClaude(t)
	_, err := Run(context.Background(), Options{
		ClaudeBinary: fake,
		AllowedTools: "Read",
		Prompt:       "x",
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	args := readFakeClaudeArgs(t, fake)
	idx := indexOf(args, "--mcp-config")
	require.GreaterOrEqual(t, idx, 0, "expected --mcp-config in args; got %v", args)
	require.True(t, strings.HasSuffix(args[idx+1], "/empty-mcp.json"),
		"expected isolation empty-mcp.json fallback when MCPConfigPath is unset; got %q", args[idx+1])
}

// TestParseStatusMarker_WellFormed extracts the message and emits a
// status event. Multiple markers in one chunk all forward.
func TestParseStatusMarker_WellFormed(t *testing.T) {
	t.Parallel()
	var got []string
	fakeEmit := func(_, msg string, _ time.Time) {
		got = append(got, msg)
	}
	parser := newStatusMarkerParser("parent-id", fakeEmit)
	parser.feed(`some prose <<<STATUS message="reading job_timeout.go">>> more prose`)
	parser.feed(`<<<STATUS message="writing failing test">>>`)
	require.Equal(t, []string{"reading job_timeout.go", "writing failing test"}, got)
}

// TestParseStatusMarker_Malformed passes through silently. We don't
// crash on near-misses; the assistant text downstream still shows up
// as plain text.
func TestParseStatusMarker_Malformed(t *testing.T) {
	t.Parallel()
	var got []string
	fakeEmit := func(_, msg string, _ time.Time) { got = append(got, msg) }
	parser := newStatusMarkerParser("parent-id", fakeEmit)
	parser.feed(`<<<STATUS missing_message>>>`)
	parser.feed(`<<<STATUS message=>>>`)
	parser.feed(`<<<STATUS message=" `) // unterminated
	require.Empty(t, got)
}

// TestFeedAndStrip_RemovesMarkersFromText guards against the regression
// where status markers were emitted as styled status lines (intended)
// AND echoed verbatim in the assistant text (unintended), making each
// status appear twice in the activity panel.
func TestFeedAndStrip_RemovesMarkersFromText(t *testing.T) {
	t.Parallel()
	var got []string
	fakeEmit := func(_, msg string, _ time.Time) { got = append(got, msg) }
	parser := newStatusMarkerParser("parent-id", fakeEmit)

	out := parser.feedAndStrip(`reading the code <<<STATUS message="reading issue #695">>> then continuing`)
	require.Equal(t, []string{"reading issue #695"}, got)
	require.NotContains(t, out, "STATUS", "marker leaked into stripped text: %q", out)
	require.Contains(t, out, "reading the code")
	require.Contains(t, out, "then continuing")

	// Marker on its own line should not leave the keyword behind.
	out2 := parser.feedAndStrip("<<<STATUS message=\"writing failing test\">>>")
	require.NotContains(t, out2, "STATUS")
	require.Equal(t, []string{"reading issue #695", "writing failing test"}, got)
}

// TestFeedAndStrip_MalformedMarkersAreLeftAsIs ensures near-misses
// stay in the prose so the operator still sees what the sub-agent
// actually wrote (lets us spot prompt-discipline issues).
func TestFeedAndStrip_MalformedMarkersAreLeftAsIs(t *testing.T) {
	t.Parallel()
	parser := newStatusMarkerParser("parent-id", func(_, _ string, _ time.Time) {})
	out := parser.feedAndStrip(`hello <<<STATUS missing_message>>> world`)
	require.Contains(t, out, "missing_message", "malformed markers must pass through so operator sees them")
}

// TestStreamBlock_ParsesToolUseBlockID locks in that an Anthropic tool_use
// content block deserialises cleanly into streamBlock. The Messages API
// schema uses `id` on tool_use blocks (and `tool_use_id` on tool_result
// blocks); the two share streamBlock, so both fields must be bound.
//
// Without an `id` mapping, postNestedToolUse's empty-id guard bails for
// every tool_use block — the launcher receives only tool_result events
// for sub-agent tool calls and the chat-side WikiProposalCard /
// ProposalCard renderers (which match a tool_call envelope by name)
// never fire. The wiki/playbook proposal cards silently fail to appear.
func TestStreamBlock_ParsesToolUseBlockID(t *testing.T) {
	t.Parallel()
	raw := `{"type":"tool_use","id":"toolu_abc123","name":"mcp__triagent-wiki__propose_wiki_draft","input":{"slug":"x"}}`
	var b streamBlock
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	require.Equal(t, "tool_use", b.Type)
	require.Equal(t, "toolu_abc123", b.ID, "tool_use block's `id` field must bind to streamBlock.ID")
	require.Equal(t, "mcp__triagent-wiki__propose_wiki_draft", b.Name)
}

// TestStreamBlock_ParsesToolResultBlockToolUseID is the symmetric lock
// for tool_result blocks, which reference the original tool_use via
// `tool_use_id`. Same struct, different field — this regression test
// guards against breaking tool_result decoding while fixing tool_use.
func TestStreamBlock_ParsesToolResultBlockToolUseID(t *testing.T) {
	t.Parallel()
	raw := `{"type":"tool_result","tool_use_id":"toolu_abc123","content":"ok"}`
	var b streamBlock
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	require.Equal(t, "tool_result", b.Type)
	require.Equal(t, "toolu_abc123", b.ToolUseID)
}

// TestPostNestedToolUse_ForwardsBareToolName verifies the launcher
// receives the real MCP tool name for sub-agent tool_use events — not a
// "subagent.tool." prefixed variant. The chat-side proposal-card
// renderers (SessionView.tsx, gated on PROPOSE_WIKI_DRAFT_TOOL_NAME /
// PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME) match the tool name exactly; any
// prefix breaks the match and the card never renders. Nesting under the
// dispatching walk_playbook tool is already carried by ParentToolID, so
// the prefix served no consumer.
func TestPostNestedToolUse_ForwardsBareToolName(t *testing.T) {
	var got []telemetry.NestedEvent
	orig := nestedSender
	nestedSender = func(ev telemetry.NestedEvent) { got = append(got, ev) }
	t.Cleanup(func() { nestedSender = orig })

	postNestedToolUse("parent-tool-id", streamBlock{
		Type:  "tool_use",
		ID:    "toolu_abc123",
		Name:  "mcp__triagent-wiki__propose_wiki_draft",
		Input: map[string]any{"slug": "x"},
	})

	require.Len(t, got, 1, "expected one start event for a single tool_use block")
	ev := got[0]
	require.Equal(t, "start", ev.Phase)
	require.Equal(t, "parent-tool-id", ev.ParentToolID)
	require.Equal(t, "sub_toolu_abc123", ev.ToolID, "ToolID must prefix the block id with sub_ so start/end correlate with postNestedToolResult")
	require.Equal(t, "mcp__triagent-wiki__propose_wiki_draft", ev.ToolName,
		"ToolName must be the bare MCP tool name; the chat-side ProposalCard match is by exact name")
	require.Equal(t, map[string]any{"slug": "x"}, ev.Input)
}

// TestRelaySubEvents_TracksRequiredTerminalTools verifies the dispatch
// verification primitive: relaySubEvents reports which required terminal
// tools the sub-agent called with a SUCCESSFUL (non-error) result, by
// correlating tool_use ids with their tool_result blocks. A tool that
// errored doesn't count as a completed terminal.
func TestRelaySubEvents_TracksRequiredTerminalTools(t *testing.T) {
	t.Parallel()
	const submit = "mcp__triagent-strategies__playbook_proposal_draft"
	const decline = "mcp__triagent-strategies__decline_proposal"
	required := map[string]struct{}{submit: {}, decline: {}}

	cases := []struct {
		name   string
		stream string
		want   map[string]bool // tool -> successfully called
	}{
		{
			name: "successful terminal call counts",
			stream: line(map[string]any{"type": "assistant", "message": msg(toolUse(submit, "tu1"))}) +
				line(map[string]any{"type": "user", "message": msg(toolResult("tu1", false))}),
			want: map[string]bool{submit: true},
		},
		{
			name: "errored terminal call does not count",
			stream: line(map[string]any{"type": "assistant", "message": msg(toolUse(submit, "tu1"))}) +
				line(map[string]any{"type": "user", "message": msg(toolResult("tu1", true))}),
			want: map[string]bool{},
		},
		{
			name: "decline terminal counts",
			stream: line(map[string]any{"type": "assistant", "message": msg(toolUse(decline, "tu9"))}) +
				line(map[string]any{"type": "user", "message": msg(toolResult("tu9", false))}),
			want: map[string]bool{decline: true},
		},
		{
			name:   "non-terminal tool is ignored",
			stream: line(map[string]any{"type": "assistant", "message": msg(toolUse("Bash", "tu2"))}) + line(map[string]any{"type": "user", "message": msg(toolResult("tu2", false))}),
			want:   map[string]bool{},
		},
		{
			name:   "no terminal call leaves the set empty",
			stream: line(map[string]any{"type": "assistant", "message": msg(textBlock("just talking"))}),
			want:   map[string]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := relaySubEvents(strings.NewReader(tc.stream), "", required)
			require.NoError(t, err)
			for tool, wantCalled := range tc.want {
				require.Equal(t, wantCalled, res.terminalsCalled[tool], "terminal %q", tool)
			}
			// Nothing outside the wanted set is marked called.
			for tool, got := range res.terminalsCalled {
				if got && !tc.want[tool] {
					t.Errorf("unexpected terminal marked called: %q", tool)
				}
			}
		})
	}
}

func line(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b) + "\n"
}
func msg(blocks ...map[string]any) map[string]any {
	return map[string]any{"content": blocks}
}
func toolUse(name, id string) map[string]any {
	return map[string]any{"type": "tool_use", "name": name, "id": id}
}
func toolResult(toolUseID string, isError bool) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "is_error": isError}
}
func textBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

// The returned summary is the sub-agent's final answer (the stream's
// `result` event), not the concatenation of every interim assistant turn.
// Interim narration ("I'll inspect the ref…") still reaches the activity
// panel as nested events; it must not leak into the parent's tool result.
func TestRelaySubEvents_SummaryPrefersResultEventOverInterimText(t *testing.T) {
	t.Parallel()
	stream := line(map[string]any{"type": "assistant", "message": msg(textBlock("I'll inspect the ref first."))}) +
		line(map[string]any{"type": "assistant", "message": msg(toolUse("Bash", "tu1"))}) +
		line(map[string]any{"type": "user", "message": msg(toolResult("tu1", false))}) +
		line(map[string]any{"type": "assistant", "message": msg(textBlock("The final answer."))}) +
		line(map[string]any{"type": "result", "result": "The final answer."})
	res, err := relaySubEvents(strings.NewReader(stream), "", nil)
	require.NoError(t, err)
	require.Equal(t, "The final answer.", res.finalText)
}

// Without a result event (timeout, crash) the concatenated assistant text
// is the best available answer and is still returned.
func TestRelaySubEvents_SummaryFallsBackToAssistantTextWithoutResult(t *testing.T) {
	t.Parallel()
	stream := line(map[string]any{"type": "assistant", "message": msg(textBlock("Partial progress."))}) +
		line(map[string]any{"type": "assistant", "message": msg(textBlock("More partial progress."))})
	res, err := relaySubEvents(strings.NewReader(stream), "", nil)
	require.NoError(t, err)
	require.Equal(t, "Partial progress.\nMore partial progress.", res.finalText)
}
