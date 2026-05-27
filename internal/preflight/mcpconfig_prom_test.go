package preflight

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sourcehawk/triagent/internal/promforward"
)

// testTarget is the standard per-investigation Target used across prom tests.
var testTarget = &promforward.Target{Service: "prometheus", Namespace: "monitoring", Port: 9090}

func TestWriteMCPConfig_PromAttachedWhenTargetSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := mcpConfigInputs{
		Dir:            dir,
		MCPBin:         "/usr/bin/triagent-mcp",
		TraceID:        "inv-1",
		TelemetryURL:   "http://127.0.0.1:8080/api/internal/tool-events",
		TelemetryToken: "tok",
		PromTarget:     testTarget,
	}
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(body, &cfg))
	servers := cfg["mcpServers"].(map[string]any)
	require.Contains(t, servers, MCPAliasProm)
	prom := servers[MCPAliasProm].(map[string]any)
	env := prom["env"].(map[string]any)
	require.Equal(t,
		"http://127.0.0.1:8080/api/internal/prom/inv-1/endpoint",
		env["TRIAGENT_MCP_PROM_RESOLVER_URL"],
	)
	require.Equal(t, "tok", env["TRIAGENT_MCP_TELEMETRY_TOKEN"])
}

func TestWriteMCPConfig_PromSkippedWhenNoTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// PromTarget is nil — no target configured for this investigation.
	in := mcpConfigInputs{Dir: dir, MCPBin: "/usr/bin/triagent-mcp", TraceID: "inv-2"}
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(body, &cfg))
	servers := cfg["mcpServers"].(map[string]any)
	_, ok := servers[MCPAliasProm]
	require.False(t, ok)
}

func TestWriteMCPConfig_PromSkippedWhenTargetServiceEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := mcpConfigInputs{
		Dir:        dir,
		MCPBin:     "/usr/bin/triagent-mcp",
		TraceID:    "inv-empty-svc",
		PromTarget: &promforward.Target{Service: "", Namespace: "monitoring", Port: 9090},
	}
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(body, &cfg))
	servers := cfg["mcpServers"].(map[string]any)
	_, ok := servers[MCPAliasProm]
	require.False(t, ok)
}

func TestWriteMCPConfig_PromSkippedWhenDisabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// PromDisabled=true must suppress prom MCP even if a target is set.
	in := mcpConfigInputs{
		Dir:          dir,
		MCPBin:       "/usr/bin/triagent-mcp",
		TraceID:      "inv-disabled",
		PromTarget:   testTarget,
		PromDisabled: true,
	}
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(body, &cfg))
	servers := cfg["mcpServers"].(map[string]any)
	_, ok := servers[MCPAliasProm]
	require.False(t, ok)
}

func TestWriteMCPConfig_PromBearerEnvPassthrough(t *testing.T) {
	t.Setenv("TRIAGENT_MCP_PROM_BEARER", "secret-bearer")
	dir := t.TempDir()
	in := mcpConfigInputs{
		Dir:            dir,
		MCPBin:         "/usr/bin/triagent-mcp",
		TraceID:        "inv-3",
		TelemetryURL:   "http://127.0.0.1:8080/api/internal/tool-events",
		TelemetryToken: "tok",
		PromTarget:     testTarget,
	}
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(body, &cfg))
	prom := cfg["mcpServers"].(map[string]any)[MCPAliasProm].(map[string]any)
	env := prom["env"].(map[string]any)
	require.Equal(t, "secret-bearer", env["TRIAGENT_MCP_PROM_BEARER"])
}
