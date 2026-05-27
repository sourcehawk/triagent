package preflight

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sourcehawk/triagent/internal/profile"
)

func TestWriteMCPConfig_PromAttachedWhenProfileHasService(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	prof := &profile.Profile{
		Defaults: profile.Defaults{
			Prometheus: profile.Prometheus{Service: "prometheus", Namespace: "monitoring", Port: 9090},
		},
	}
	in := mcpConfigInputs{
		Dir:            dir,
		MCPBin:         "/usr/bin/triagent-mcp",
		Profile:        prof,
		TraceID:        "inv-1",
		TelemetryURL:   "http://127.0.0.1:8080/api/internal/tool-events",
		TelemetryToken: "tok",
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

func TestWriteMCPConfig_PromSkippedWhenProfileEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	prof := &profile.Profile{}
	in := mcpConfigInputs{Dir: dir, MCPBin: "/usr/bin/triagent-mcp", Profile: prof, TraceID: "inv-2"}
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
	prof := &profile.Profile{
		Defaults: profile.Defaults{
			Prometheus: profile.Prometheus{Service: "prometheus", Namespace: "monitoring", Port: 9090},
		},
	}
	in := mcpConfigInputs{
		Dir: dir, MCPBin: "/usr/bin/triagent-mcp", Profile: prof, TraceID: "inv-3",
		TelemetryURL:   "http://127.0.0.1:8080/api/internal/tool-events",
		TelemetryToken: "tok",
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
