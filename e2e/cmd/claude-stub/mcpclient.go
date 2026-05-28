package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// roundTripResult is one real MCP tool call's outcome, recorded to the trace
// for the test to assert against.
type roundTripResult struct {
	Tool       string          `json:"tool"`
	Server     string          `json:"server"`
	IsError    bool            `json:"isError"`
	Text       string          `json:"text,omitempty"`
	Structured json.RawMessage `json:"structured,omitempty"`
}

// mcpServerSpec is the slice of an mcp.json server entry the stub needs to
// spawn one upstream.
type mcpServerSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// mcpPool keeps one live MCP client session per server for the lifetime of a
// replay. Reusing the session across tool calls is what makes the k8s flow
// genuine: switch_context binds a snapshot in the k8s MCP process, and the
// subsequent list_namespaces / list_resources calls hit that same process and
// see the bound context — exactly as the real claude CLI drives one MCP server
// across a turn. A fresh process per call would lose the binding.
//
// This is also why the stub IS the MCP client (the role the real claude CLI
// plays): the tool call leaves this process, boots triagent-mcp --kind=k8s
// with the launcher-provided KUBECONFIG, hits envtest, and the result flows
// back here to be recorded.
type mcpPool struct {
	configPath string
	ctx        context.Context
	cancel     context.CancelFunc
	sessions   map[string]*sdkmcp.ClientSession
}

func newMCPPool(configPath string) *mcpPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &mcpPool{
		configPath: configPath,
		ctx:        ctx,
		cancel:     cancel,
		sessions:   map[string]*sdkmcp.ClientSession{},
	}
}

// closeAll tears down every spawned MCP child. Cancelling ctx stops the
// command transports; Close drains each session. Called once at replay end.
func (p *mcpPool) closeAll() {
	for _, s := range p.sessions {
		_ = s.Close()
	}
	p.cancel()
}

// session returns a connected client session for the named server, spawning
// and connecting it on first use. The spawned process gets exactly the env
// block from mcp.json (the launcher already put KUBECONFIG there), so no
// ambient env of this stub leaks in.
func (p *mcpPool) session(server string) (*sdkmcp.ClientSession, error) {
	if s, ok := p.sessions[server]; ok {
		return s, nil
	}
	spec, err := loadMCPServer(p.configPath, server)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(p.ctx, spec.Command, spec.Args...)
	if len(spec.Env) > 0 {
		env := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	// Surface the MCP child's stderr through the stub's stderr so a boot
	// failure (e.g. unreachable apiserver) shows up in the launcher log dump.
	cmd.Stderr = os.Stderr

	transport := &sdkmcp.CommandTransport{Command: cmd}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "claude-stub", Version: "0.1.0"}, nil)
	sess, err := client.Connect(p.ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect %q: %w", server, err)
	}
	p.sessions[server] = sess
	return sess, nil
}

// call performs one tool round-trip against the named server, reusing its live
// session. Returns the structured + text result for the trace.
func (p *mcpPool) call(server, tool string, args map[string]any) (roundTripResult, error) {
	sess, err := p.session(server)
	if err != nil {
		return roundTripResult{}, err
	}
	res, err := sess.CallTool(p.ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return roundTripResult{}, fmt.Errorf("call %s/%s: %w", server, tool, err)
	}
	out := roundTripResult{Tool: tool, Server: server, IsError: res.IsError, Text: firstText(res)}
	if res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			out.Structured = b
		}
	}
	return out, nil
}

// loadMCPServer reads the per-session mcp.json at configPath and returns the
// spec for the named server. A missing config or missing server is an error —
// the round-trip can't proceed without it, and silently degrading would let a
// broken wire path masquerade as a passing test.
func loadMCPServer(configPath, server string) (mcpServerSpec, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return mcpServerSpec{}, fmt.Errorf("read mcp config %q: %w", configPath, err)
	}
	var cfg struct {
		MCPServers map[string]mcpServerSpec `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return mcpServerSpec{}, fmt.Errorf("parse mcp config: %w", err)
	}
	spec, ok := cfg.MCPServers[server]
	if !ok {
		return mcpServerSpec{}, fmt.Errorf("mcp config has no server %q", server)
	}
	if spec.Command == "" {
		return mcpServerSpec{}, fmt.Errorf("mcp server %q has no command", server)
	}
	return spec, nil
}

// firstText returns the first text content block, or "".
func firstText(res *sdkmcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*sdkmcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}
