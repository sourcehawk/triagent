//go:build e2e

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// detectRole pins the role-detection contract the stub uses to pick its
// script. The auto-mode spec adds the sub-agent dispatch table later; this
// suite only exercises the main session, so every current caller resolves
// to "main". The subagent branches are pinned so the contract can't drift
// silently before that work lands.
func TestDetectRole(t *testing.T) {
	dir := t.TempDir()

	// A populated MCP config with at least one server + no role marker is
	// the main investigation session — the only role this suite drives.
	mainMCP := filepath.Join(dir, "main-mcp.json")
	if err := os.WriteFile(mainMCP, []byte(`{"mcpServers":{"triagent-k8s":{"command":"triagent-mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// An empty / no-servers MCP config marks a sub-agent dispatch.
	emptyMCP := filepath.Join(dir, "empty-mcp.json")
	if err := os.WriteFile(emptyMCP, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		argv []string
		want string
	}{
		{
			name: "populated mcp, no role marker -> main",
			argv: []string{"--output-format", "stream-json", "--mcp-config", mainMCP, "--append-system-prompt", "You are an incident investigator."},
			want: "main",
		},
		{
			name: "no mcp-config flag -> main",
			argv: []string{"--output-format", "stream-json", "--print"},
			want: "main",
		},
		{
			name: "empty mcp servers -> subagent",
			argv: []string{"--mcp-config", emptyMCP},
			want: "subagent",
		},
		{
			name: "role marker in system prompt -> subagent",
			argv: []string{"--mcp-config", mainMCP, "--append-system-prompt", "ROLE: subagent\nSummarise the evidence."},
			want: "subagent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectRole(tc.argv); got != tc.want {
				t.Fatalf("detectRole(%v) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}
