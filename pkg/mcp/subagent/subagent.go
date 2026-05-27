// Package subagent runs an ephemeral `claude` subprocess as a tightly-scoped
// helper for an MCP tool. Used by the git and wiki MCP servers; the contract
// is identical for both — only the AllowedTools whitelist differs.
//
// Isolation defaults: the sub-agent gets an empty MCP config (no parent
// MCPs reachable) and the TRIAGENT_MCP_TELEMETRY_* env vars are stripped so any
// MCP tools it might fall back to don't post directly to the launcher.
// Callers whose AllowedTools intentionally targets parent MCP tools (e.g.
// the strategies dispatch path, where the sub-agent must invoke
// mcp__triagent-strategies__playbook_proposal_draft) opt out of the empty
// config by setting Options.MCPConfigPath; AllowedTools remains the access
// boundary.
package subagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

// defaultTimeout is the wall-clock cap when Options.Timeout is zero.
// 5 min covers cold-clone fetches and large-diff analyses; sub-agent
// callers that legitimately need longer (draft_pr) override it.
const defaultTimeout = 5 * time.Minute

// Options configures a single Run call. Prompt is sent on stdin to claude.
type Options struct {
	// ClaudeBinary is the path to the `claude` CLI. Empty falls back to
	// looking up `claude` on $PATH.
	ClaudeBinary string
	// WorkingDir is the cwd for the spawned subprocess. The sub-agent's
	// Read/Write/Bash etc. resolve relative paths against this dir.
	WorkingDir string
	// AllowedTools is the comma-separated value passed to claude
	// --allowedTools. Required; if empty Run returns an error rather than
	// silently letting the sub-agent run with whatever the default is.
	AllowedTools string
	// DisallowedTools is the comma-separated value passed to
	// claude --disallowedTools. Optional — empty omits the flag entirely.
	// Useful for tools whose prompt instructs the sub-agent NOT to do X
	// when X is allowlisted by a broad Bash(*) (the deny-list is the
	// hard boundary; the prompt is defence in depth).
	DisallowedTools string
	// Prompt is sent on stdin via --print, which is how the existing git
	// runner feeds the prompt without command-line escaping issues.
	Prompt string
	// Timeout caps the subprocess wallclock. Zero falls back to defaultTimeout (5 min).
	Timeout time.Duration
	// ParentToolID is forwarded to telemetry so sub-events render as
	// nested children in the activity panel.
	ParentToolID string
	// ResumeSessionID, when non-empty, is passed to claude as
	// `--resume <id>` so the subprocess continues the prior conversation
	// instead of starting fresh. Used by draft_pr to thread the main
	// sub-agent's session through citation retries and the
	// commit-or-cleanup recovery sub-agent — without resumption every
	// retry loses the file edits, tool calls, and reasoning the prior
	// turn built. Empty (default) starts a fresh session.
	ResumeSessionID string
	// Model passes through as --model to the claude subprocess. Empty
	// inherits the parent. Used by callers (and per-profile defaults)
	// to route lighter structured-output work to Haiku.
	Model string
	// MCPConfigPath overrides the package default empty-isolation MCP
	// config. When set, the path is forwarded as --mcp-config so the
	// sub-agent attaches the listed MCP servers and can call their tools
	// (subject to AllowedTools). Used by the strategies dispatch path —
	// playbook_proposal / wiki_proposal sub-agents need to call back into
	// parent MCPs (mcp__triagent-strategies__*, mcp__triagent-wiki__*) to
	// do their work. Empty (default) keeps the isolation behaviour for
	// existing callers (git, wiki helper sub-agents, citation runner).
	MCPConfigPath string
}

// Result is the structured outcome of one Run call.
type Result struct {
	Summary    string `json:"summary"`
	PromptSent string `json:"prompt_sent,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
	// SessionID is the claude conversation id extracted from the first
	// stream-json system event. Callers thread it back as
	// Options.ResumeSessionID on subsequent runs to keep the same
	// conversation alive across retries.
	SessionID string `json:"session_id,omitempty"`
}

// Run spawns the sub-agent and returns its final answer. See package doc for
// isolation guarantees.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.AllowedTools == "" {
		return Result{}, fmt.Errorf("subagent.Run: AllowedTools is required")
	}
	bin := opts.ClaudeBinary
	if bin == "" {
		path, err := exec.LookPath("claude")
		if err != nil {
			return Result{}, fmt.Errorf("locate claude binary: %w (set ClaudeBinary or install Claude Code)", err)
		}
		bin = path
	}

	mcpCfg := opts.MCPConfigPath
	if mcpCfg == "" {
		emptyCfg, err := writeEmptyMCPConfig()
		if err != nil {
			return Result{}, fmt.Errorf("write empty mcp config: %w", err)
		}
		mcpCfg = emptyCfg
	} else {
		sanitized, err := sanitizeMCPConfig(mcpCfg)
		if err != nil {
			return Result{}, fmt.Errorf("sanitize mcp config: %w", err)
		}
		mcpCfg = sanitized
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--mcp-config", mcpCfg,
		"--allowedTools", opts.AllowedTools,
	}
	if opts.DisallowedTools != "" {
		args = append(args, "--disallowedTools", opts.DisallowedTools)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	}
	cmd := exec.CommandContext(subCtx, bin, args...)
	cmd.Dir = opts.WorkingDir
	cmd.Stdin = strings.NewReader(opts.Prompt)
	cmd.Env = filterEnv(os.Environ())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start claude: %w", err)
	}

	stderrCh := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(stderr)
		stderrCh <- string(b)
	}()

	finalText, sessionID, parseErr := relaySubEvents(stdout, opts.ParentToolID)

	waitErr := cmd.Wait()
	stderrTail := trimTo(<-stderrCh, 800)

	res := Result{
		Summary:    finalText,
		PromptSent: opts.Prompt,
		StderrTail: stderrTail,
		SessionID:  sessionID,
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if errIs(subCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}
	if waitErr != nil && !res.TimedOut {
		return res, fmt.Errorf("claude exited: %w (stderr: %s)", waitErr, stderrTail)
	}
	if parseErr != nil {
		return res, fmt.Errorf("parse claude stream: %w", parseErr)
	}
	return res, nil
}

// relaySubEvents reads stream-json lines from claude's stdout and forwards
// each event as a nested telemetry pair (start+end) attributed to
// parentToolID. Returns the final assistant/result text aggregated across
// `result` and `assistant` events, plus the session_id from the first
// system event (used by callers to thread session resumption across
// retries).
func relaySubEvents(r io.Reader, parentToolID string) (string, string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	parser := newStatusMarkerParser(parentToolID, func(parentID, msg string, _ time.Time) {
		telemetry.SendStatus(parentID, msg)
	})
	var finalText strings.Builder
	var sessionID string
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Non-JSON line — surface as a plain assistant nested event so
			// the operator still sees something rather than silent loss.
			postNested(parentToolID, "assistant", string(line))
			continue
		}
		// Capture session_id from any event that carries one (system init
		// is the canonical first source; subsequent events repeat it).
		if sessionID == "" && ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		switch ev.Type {
		case "assistant":
			text := concatAssistantText(ev.Message)
			if text != "" {
				// Emit status markers as telemetry AND strip them from
				// the chunk before it lands in the activity panel /
				// finalText — otherwise each status appears twice
				// (once as the styled status line, once verbatim in
				// the assistant text).
				text = parser.feedAndStrip(text)
				if strings.TrimSpace(text) != "" {
					postNested(parentToolID, "assistant", text)
					finalText.WriteString(text)
					finalText.WriteString("\n")
				}
			}
			// Tool uses inside the message render as their own nested events.
			for _, block := range ev.Message.Content {
				if block.Type == "tool_use" {
					postNestedToolUse(parentToolID, block)
				}
			}
		case "user":
			// Sub-agent's tool_result blocks live here; surface them as
			// nested tool_result events.
			for _, block := range ev.Message.Content {
				if block.Type == "tool_result" {
					postNestedToolResult(parentToolID, block)
				}
			}
		case "result":
			if ev.Result != "" {
				postNested(parentToolID, "result", ev.Result)
				if finalText.Len() == 0 {
					finalText.WriteString(ev.Result)
				}
			}
		case "system":
			// Skip system init noise to keep the activity panel readable —
			// session_id was captured above.
		}
	}
	if err := scanner.Err(); err != nil {
		return finalText.String(), sessionID, err
	}
	return strings.TrimSpace(finalText.String()), sessionID, nil
}

// streamEvent is the bare-minimum decoder of claude --output-format
// stream-json. We only inspect the fields we need to surface and finalise.
type streamEvent struct {
	Type      string        `json:"type"`
	Message   streamMessage `json:"message,omitempty"`
	Result    string        `json:"result,omitempty"`
	SessionID string        `json:"session_id,omitempty"`
}

type streamMessage struct {
	Content []streamBlock `json:"content"`
}

type streamBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"`
	// ID carries the tool-call id on tool_use blocks. The Messages API
	// puts the id under `id` on tool_use and under `tool_use_id` on
	// tool_result, so we bind both — they're distinct fields on the same
	// shared struct.
	ID        string         `json:"id,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

func concatAssistantText(m streamMessage) string {
	out := ""
	for _, b := range m.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// nestedSender is the package-private hook the postNested* helpers use
// to dispatch sub-agent telemetry. Defaults to telemetry.SendNested;
// tests swap this to capture events without standing up a fake HTTP
// receiver. Production callers never touch it.
var nestedSender = telemetry.SendNested

// postNested fires a synthetic start+end pair for a logical sub-event with
// the supplied free-form text in result. ToolName is "subagent.<kind>" so
// the activity panel can render appropriate icons.
func postNested(parentToolID, kind, text string) {
	if parentToolID == "" {
		return
	}
	id := telemetry.NewToolID()
	nestedSender(telemetry.NestedEvent{
		Phase:        "start",
		ParentToolID: parentToolID,
		ToolID:       id,
		ToolName:     "subagent." + kind,
	})
	nestedSender(telemetry.NestedEvent{
		Phase:        "end",
		ParentToolID: parentToolID,
		ToolID:       id,
		Result:       text,
	})
}

// postNestedToolUse forwards a sub-agent tool_use block as a Phase=start
// telemetry event under parentToolID. ToolName is the bare MCP tool name
// (e.g. mcp__triagent-wiki__propose_wiki_draft) — no "subagent.tool."
// prefix, because the chat-side ProposalCard / WikiProposalCard renderers
// match on exact name and the activity-panel nesting is already carried
// by ParentToolID.
func postNestedToolUse(parentToolID string, b streamBlock) {
	if parentToolID == "" || b.ID == "" {
		return
	}
	nestedSender(telemetry.NestedEvent{
		Phase:        "start",
		ParentToolID: parentToolID,
		ToolID:       "sub_" + b.ID,
		ToolName:     b.Name,
		Input:        b.Input,
	})
}

func postNestedToolResult(parentToolID string, b streamBlock) {
	if parentToolID == "" || b.ToolUseID == "" {
		return
	}
	resultText := stringifyToolResult(b.Content)
	errText := ""
	if b.IsError {
		errText = resultText
	}
	nestedSender(telemetry.NestedEvent{
		Phase:        "end",
		ParentToolID: parentToolID,
		ToolID:       "sub_" + b.ToolUseID,
		Result:       resultText,
		ErrorText:    errText,
	})
}

func stringifyToolResult(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		out := ""
		for _, b := range v {
			if m, ok := b.(map[string]any); ok {
				if t, _ := m["text"].(string); t != "" {
					out += t
				}
			}
		}
		return out
	default:
		if b, err := json.Marshal(c); err == nil {
			return string(b)
		}
	}
	return ""
}

// writeEmptyMCPConfig writes a minimal `{}`-mcpServers JSON file once per
// process lifetime under the OS temp dir; subsequent calls reuse it.
// emptyCfgMu serialises the read-check-write so concurrent first-callers
// don't race on emptyCfgPath. A plain Mutex (not sync.Once) is the right
// shape here because we tolerate the cached file going missing — e.g.
// long-running launcher process whose /tmp got swept — and re-create it
// on the next call.
var (
	emptyCfgMu   sync.Mutex
	emptyCfgPath string
)

func writeEmptyMCPConfig() (string, error) {
	emptyCfgMu.Lock()
	defer emptyCfgMu.Unlock()
	if emptyCfgPath != "" {
		if _, err := os.Stat(emptyCfgPath); err == nil {
			return emptyCfgPath, nil
		}
	}
	dir, err := os.MkdirTemp("", "triagent-mcp-subagent-empty-*")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "empty-mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		return "", err
	}
	emptyCfgPath = path
	return path, nil
}

// sanitizeMCPConfig produces a copy of srcPath with TRIAGENT_MCP_TELEMETRY_*
// env vars stripped from each server's env block. Without this, the sub-agent's
// claude attaches the same MCP servers the parent uses, those MCPs see the
// parent's TELEMETRY_URL / TELEMETRY_TOKEN / TELEMETRY_TOOL_PREFIX in their env
// and post tool_use/tool_result directly to the launcher with the parent trace
// id and no parent_tool_id — rendering as siblings of the dispatching tool in
// the activity panel instead of nested children. With telemetry stripped, only
// the runner's relaySubEvents path (which sets ParentToolID) reports sub-agent
// activity, and nesting works.
//
// TRIAGENT_MCP_TRACE_ID is intentionally NOT stripped: it's a correlation id
// (not a credential or sink), and telemetry is already disabled by removing
// TELEMETRY_URL.
//
// Cached by source path so repeated dispatches don't re-write the same file.
// Mirrors writeEmptyMCPConfig's shape; we tolerate the cached file going
// missing and re-create it on the next call.
var (
	sanitizedCfgMu    sync.Mutex
	sanitizedCfgPaths = map[string]string{}
)

func sanitizeMCPConfig(srcPath string) (string, error) {
	sanitizedCfgMu.Lock()
	defer sanitizedCfgMu.Unlock()
	if dst, ok := sanitizedCfgPaths[srcPath]; ok {
		if _, err := os.Stat(dst); err == nil {
			return dst, nil
		}
	}
	body, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("read mcp config %s: %w", srcPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", fmt.Errorf("parse mcp config %s: %w", srcPath, err)
	}
	if servers, ok := cfg["mcpServers"].(map[string]any); ok {
		for _, raw := range servers {
			srv, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			env, ok := srv["env"].(map[string]any)
			if !ok {
				continue
			}
			for k := range env {
				if strings.HasPrefix(k, "TRIAGENT_MCP_TELEMETRY_") {
					delete(env, k)
				}
			}
		}
	}
	dir, err := os.MkdirTemp("", "triagent-mcp-subagent-sanitized-*")
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "mcp-sanitized.json")
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, out, 0o600); err != nil {
		return "", err
	}
	sanitizedCfgPaths[srcPath] = dst
	return dst, nil
}

// filterEnv drops TRIAGENT_MCP_TELEMETRY_* so the sub-agent's potential MCP
// servers (none in our config, but defence in depth) can't post to the
// parent's telemetry endpoint.
func filterEnv(in []string) []string {
	out := make([]string, 0, len(in))
	for _, kv := range in {
		if strings.HasPrefix(kv, "TRIAGENT_MCP_TELEMETRY_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// trimTo returns s truncated to at most n bytes (rune-safe is unnecessary
// for stderr; we only show the tail in error strings).
func trimTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func errIs(got, want error) bool { return got == want }
