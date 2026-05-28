// Command claude-stub stands in for the `claude` CLI on $PATH during the
// e2e suite. The launcher exec's it with the same argv the real CLI accepts
// (--output-format stream-json, --mcp-config, --allowedTools, --model,
// --append-system-prompt, --resume, --print) and feeds the prompt over
// stdin. The stub replays a JSONL script from disk, emits stream-json on
// stdout, and records everything it saw to a trace file the test asserts
// against.
//
// The stub is deliberately dumb: it does not reason, it does not call MCP
// servers itself, it just replays the scripted events. The k8s round-trip
// flow (#17) wires the real MCP path; here the action loop only needs to
// model the vocabulary.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "claude-stub:", err)
		os.Exit(1)
	}
}

// run is the testable entry point. It resolves the role + script, opens the
// trace, records the invocation context, and replays the script. Returning
// an error (rather than calling os.Exit) lets the action loop signal a
// scripted exit code through a sentinel.
func run(argv []string, stdin *os.File, stdout *os.File) error {
	role := detectRole(argv)

	tr, err := newTrace(role)
	if err != nil {
		return err
	}
	defer tr.Close()

	flags := parseArgs(argv)
	tr.record(map[string]any{
		"event":        "args",
		"argv":         argv,
		"systemPrompt": flags.systemPrompt,
		"allowedTools": flags.allowedTools,
		"mcpConfig":    flags.mcpConfig,
		"model":        flags.model,
		"resume":       flags.resume,
	})

	scriptPath, err := scriptFile()
	if err != nil {
		return err
	}
	actions, err := loadScript(scriptPath)
	if err != nil {
		return err
	}

	in := bufio.NewReader(stdin)
	out := bufio.NewWriter(stdout)
	defer func() { _ = out.Flush() }()

	code, err := replay(actions, in, out, tr, replayDeps{mcpConfigPath: flags.mcpConfig})
	if err != nil {
		return err
	}
	if err := out.Flush(); err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// detectRole picks the stub's role from its argv. The contract:
//
//  1. A populated MCP config (at least one server) with no role marker in
//     the system prompt → "main" — the live investigation session.
//  2. An empty / no-servers MCP config → "subagent".
//  3. A role marker ("ROLE:") in the --append-system-prompt body → "subagent".
//
// Every caller in this suite resolves to "main"; the subagent branches are
// pinned by a unit test so the contract can't drift before the auto-mode
// spec adds the per-role dispatch table.
func detectRole(argv []string) string {
	flags := parseArgs(argv)
	if flags.mcpConfig != "" {
		if emptyMCPConfig(flags.mcpConfig) {
			return "subagent"
		}
	}
	if strings.Contains(flags.systemPrompt, "ROLE:") {
		return "subagent"
	}
	return "main"
}

// cliFlags is the parsed view of the argv the launcher passes. The system
// prompt is the literal --append-system-prompt value (the real CLI takes
// the prompt body directly as an argument, not a file path).
type cliFlags struct {
	mcpConfig    string
	systemPrompt string
	allowedTools []string
	model        string
	resume       string
}

// parseArgs reads the subset of the claude CLI argv the launcher emits (see
// internal/claude/session.go::baseArgs). Repeated --allowedTools flags
// accumulate; unknown flags are ignored.
func parseArgs(argv []string) cliFlags {
	var f cliFlags
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--mcp-config":
			if i+1 < len(argv) {
				f.mcpConfig = argv[i+1]
				i++
			}
		case "--append-system-prompt":
			if i+1 < len(argv) {
				f.systemPrompt = argv[i+1]
				i++
			}
		case "--allowedTools":
			if i+1 < len(argv) {
				f.allowedTools = append(f.allowedTools, argv[i+1])
				i++
			}
		case "--model":
			if i+1 < len(argv) {
				f.model = argv[i+1]
				i++
			}
		case "--resume":
			if i+1 < len(argv) {
				f.resume = argv[i+1]
				i++
			}
		}
	}
	return f
}

// emptyMCPConfig reports whether the MCP config file has no servers. An
// unreadable or malformed file is treated as non-empty so a transient read
// error doesn't silently flip a main session to a subagent.
func emptyMCPConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	return len(cfg.MCPServers) == 0
}

// scriptFile resolves the JSONL script the stub replays. CLAUDE_STUB_SCRIPT
// is the per-test directory under fixtures/stub-scripts/<test>/; the stub
// reads main.jsonl from it.
func scriptFile() (string, error) {
	dir := os.Getenv("CLAUDE_STUB_SCRIPT")
	if dir == "" {
		return "", fmt.Errorf("CLAUDE_STUB_SCRIPT is unset")
	}
	return filepath.Join(dir, "main.jsonl"), nil
}
