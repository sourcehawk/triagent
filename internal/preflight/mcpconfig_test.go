package preflight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// k8sArgs returns the args slice for the triagent-k8s server entry from a written
// mcp.json, joined into a single space-separated string for easy assertion.
func k8sArgsStr(t *testing.T, mcpPath string) string {
	t.Helper()
	servers := readMCPConfig(t, mcpPath)
	k8s, ok := servers[MCPAliasK8s]
	require.True(t, ok, "triagent-k8s server must be present")
	rawArgs, _ := k8s["args"].([]any)
	parts := make([]string, len(rawArgs))
	for i, a := range rawArgs {
		parts[i], _ = a.(string)
	}
	return strings.Join(parts, " ")
}

func TestK8sMCPGetsCrdsFileFromEmbeddedProfile(t *testing.T) {
	t.Parallel()
	// Build a profile that has an on-disk kinds.json so resolveKindsFile
	// injects --crds-file. A profile-specific config is needed;
	// now we create an equivalent temp-dir profile with a real kinds.json.
	dir := t.TempDir()
	profileYAML := `name: test-kinds
auth:
  kind: teleport
  teleport:
    proxy: test.example.com
    auth_connector: okta
playbooks:
  entrypoint: investigation
  closing: capture_offer
defaults:
  prometheus:
    port: 9090
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(profileYAML), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "k8s"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k8s", "kinds.json"), []byte(`[{"group":"example.io","kind":"Example"}]`), 0o600))
	prof, err := profile.LoadPath(dir)
	require.NoError(t, err)
	tmp := t.TempDir()
	path, err := writeMCPConfig(mcpConfigInputs{
		Dir:     tmp,
		MCPBin:  "/usr/local/bin/triagent-mcp",
		Profile: prof,
	})
	require.NoError(t, err)
	args := k8sArgsStr(t, path)
	assert.Contains(t, args, "--crds-file=", "profile with kinds.json must inject --crds-file arg")
	// For a filesystem-loaded profile, the kinds.json path is the profile dir's
	// actual path (not a session-dir copy). Both are valid; assert the arg exists.
	assert.Contains(t, args, "kinds.json", "--crds-file must point to a kinds.json")
}

func TestK8sMCPNoCrdsFileWhenProfileHasNoKinds(t *testing.T) {
	t.Parallel()
	prof, err := profile.LoadEmbedded("default")
	require.NoError(t, err)
	tmp := t.TempDir()
	path, err := writeMCPConfig(mcpConfigInputs{
		Dir:     tmp,
		MCPBin:  "/usr/local/bin/triagent-mcp",
		Profile: prof,
	})
	require.NoError(t, err)
	args := k8sArgsStr(t, path)
	assert.NotContains(t, args, "--crds-file", "default profile must not inject --crds-file")
}

func TestK8sMCPEnvCrdsFileTakesPrecedence(t *testing.T) {
	t.Setenv("TRIAGENT_MCP_CRDS_FILE", "/explicit/path/kinds.json")
	prof, err := profile.LoadEmbedded("default")
	require.NoError(t, err)
	tmp := t.TempDir()
	path, err := writeMCPConfig(mcpConfigInputs{
		Dir:     tmp,
		MCPBin:  "/usr/local/bin/triagent-mcp",
		Profile: prof,
	})
	require.NoError(t, err)
	args := k8sArgsStr(t, path)
	assert.Contains(t, args, "--crds-file=/explicit/path/kinds.json", "TRIAGENT_MCP_CRDS_FILE env must override profile kinds path")
	assert.NotContains(t, args, tmp, "session-dir path must NOT appear when env override is active")
}

// readMCPConfig parses the mcp.json file produced by writeMCPConfig and
// returns its decoded mcpServers map. Tests then inspect the keys and the
// per-server env block.
func readMCPConfig(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(body, &cfg))
	return cfg.MCPServers
}

func baseInputs(t *testing.T) mcpConfigInputs {
	t.Helper()
	return mcpConfigInputs{
		Dir:       t.TempDir(),
		MCPBin:    "/usr/local/bin/triagent-mcp",
		Namespace: "ns",
	}
}

// TestWriteMCPConfig_StrategiesGetsMCPConfigPath locks in that the
// strategies server's env block carries the path of the mcp.json being
// written, so its runDispatch can forward that path to subagent.Run.
// Without this, dispatched playbook_proposal / wiki_proposal sub-agents
// can't reach mcp__triagent-strategies__* / mcp__triagent-wiki__* tools.
func TestWriteMCPConfig_StrategiesGetsMCPConfigPath(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	strat, ok := servers[MCPAliasStrategies]
	require.True(t, ok, "strategies server must be present")
	env, _ := strat["env"].(map[string]any)
	require.NotNil(t, env)
	assert.Equal(t, path, env[EnvMCPConfigPath],
		"strategies env must point %s at the just-written mcp.json so dispatched sub-agents can attach parent MCPs", EnvMCPConfigPath)
}

func TestWriteMCPConfig_NoSlackInputs_OmitsSlackServer(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	assert.NotContains(t, servers, MCPAliasSlack, "slack server must not appear when no slack inputs are set")
}

func TestWriteMCPConfig_SlackToken_RegistersServer(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	in.SlackToken = "xoxp-test"
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	require.Contains(t, servers, MCPAliasSlack, "slack server must be registered whenever the token is set")

	slack := servers[MCPAliasSlack]
	args, _ := slack["args"].([]any)
	require.Len(t, args, 2)
	assert.Equal(t, "serve", args[0])
	assert.Equal(t, "--kind=slack", args[1])

	env, _ := slack["env"].(map[string]any)
	assert.Equal(t, "xoxp-test", env[EnvSlackToken])
	// Channel scope and since-unix are agent-supplied per-call now;
	// they must NOT appear in the MCP boot env.
	assert.NotContains(t, env, "TRIAGENT_MCP_SLACK_CHANNEL", "channel scope must not be passed at boot")
	assert.NotContains(t, env, "TRIAGENT_MCP_SLACK_SINCE", "since scope must not be passed at boot")
}

func TestWriteMCPConfig_NoSlackToken_OmitsServer(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	assert.NotContains(t, readMCPConfig(t, path), MCPAliasSlack, "no token means no slack server")
}

func TestWriteMCPConfig_NoIncidentioInputs_OmitsServer(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	assert.NotContains(t, servers, MCPAliasIncidentio)
}

func TestWriteMCPConfig_IncidentioToken_RegistersServer(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	in.IncidentioToken = "io-test"
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	require.Contains(t, servers, MCPAliasIncidentio)

	io := servers[MCPAliasIncidentio]
	args, _ := io["args"].([]any)
	require.Len(t, args, 2)
	assert.Equal(t, "serve", args[0])
	assert.Equal(t, "--kind=incidentio", args[1])

	env, _ := io["env"].(map[string]any)
	assert.Equal(t, "io-test", env[EnvIncidentioToken])
	assert.NotContains(t, env, "TRIAGENT_MCP_INCIDENTIO_INCIDENT", "incident scope must not be passed at boot")
}

func TestWriteMCPConfig_IncludesMeta(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	require.Contains(t, servers, MCPAliasMeta, "meta server must always be registered")

	meta := servers[MCPAliasMeta]
	args, _ := meta["args"].([]any)
	require.Len(t, args, 2)
	assert.Equal(t, "serve", args[0])
	assert.Equal(t, "--kind=meta", args[1])
}

func TestWriteMCPConfig_IncludesParallelBroker(t *testing.T) {
	t.Parallel()
	in := baseInputs(t)
	in.MCPBin = "/usr/local/bin/triagent-mcp"
	in.LinkedRepos = []repos.LinkedRepo{{Owner: "example-org", Name: "alerts"}}
	path, err := writeMCPConfig(in)
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	require.Contains(t, servers, MCPAliasParallel, "triagent-parallel broker must be registered")

	broker := servers[MCPAliasParallel]
	args, _ := broker["args"].([]any)
	require.Len(t, args, 2)
	assert.Equal(t, "serve", args[0])
	assert.Equal(t, "--kind=parallel", args[1])
	assert.Equal(t, "/usr/local/bin/triagent-mcp", broker["command"])

	env, _ := broker["env"].(map[string]any)
	require.NotNil(t, env)
	rawUpstreams, _ := env[EnvParallelUpstreams].(string)
	require.NotEmpty(t, rawUpstreams, "upstreams env must be set")

	var upstreams map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	require.NoError(t, json.Unmarshal([]byte(rawUpstreams), &upstreams))
	_, selfReferenced := upstreams[MCPAliasParallel]
	assert.False(t, selfReferenced, "broker must not list itself as an upstream")
	_, hasGit := upstreams["triagent-git-alerts"]
	assert.True(t, hasGit, "expected triagent-git-alerts among upstreams")
	_, hasK8s := upstreams[MCPAliasK8s]
	assert.True(t, hasK8s, "expected triagent-k8s among upstreams")
}

func TestWriteMCPConfig_RegistersTeleport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mcpPath, err := writeMCPConfig(mcpConfigInputs{
		Dir:            dir,
		MCPBin:         "/tmp/triagent-mcp",
		KubeconfigPath: "/tmp/kubeconfig",
	})
	require.NoError(t, err)

	body, err := os.ReadFile(mcpPath)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(body, &cfg))
	servers := cfg["mcpServers"].(map[string]any)

	tp, ok := servers[MCPAliasTeleport].(map[string]any)
	require.True(t, ok, "triagent-teleport server entry missing")
	assert.Equal(t, "/tmp/triagent-mcp", tp["command"])
	assert.Equal(t, []any{"serve", "--kind=teleport"}, tp["args"])

	env := tp["env"].(map[string]any)
	assert.Equal(t, "/tmp/kubeconfig", env["KUBECONFIG"], "teleport server needs the frozen kubeconfig")
}

func TestWriteMCPConfig_ExtraMCPs_SpawnMode_EmitsServerEntry(t *testing.T) {
	t.Setenv("FOO_URL", "http://localhost:9090")
	prof := &profile.Profile{
		ExtraMCPs: []profile.ExtraMCP{
			{Alias: "ref-mode", Description: "claude-configured"},
			{
				Alias:   "prom-spawn",
				Description: "spawn-mode prom",
				Command: "triagent-mcp",
				Args:    []string{"serve", "--kind=prom"},
				Env:     map[string]string{"FOO_URL": "${env:FOO_URL}"},
			},
		},
	}
	path, err := writeMCPConfig(mcpConfigInputs{
		Dir:     t.TempDir(),
		MCPBin:  "/bin/triagent-mcp",
		Profile: prof,
	})
	require.NoError(t, err)
	servers := readMCPConfig(t, path)
	assert.NotContains(t, servers, "ref-mode",
		"reference-mode entry should NOT spawn a server")
	require.Contains(t, servers, "prom-spawn",
		"spawn-mode entry should appear as a server")
	spawn := servers["prom-spawn"]
	assert.Equal(t, "triagent-mcp", spawn["command"])
	args := spawn["args"].([]any)
	assert.Equal(t, []any{"serve", "--kind=prom"}, args)
	env := spawn["env"].(map[string]any)
	assert.Equal(t, "http://localhost:9090", env["FOO_URL"],
		"${env:FOO_URL} should be interpolated at write time")
}

func TestWriteMCPConfig_ExtraMCPs_MissingEnv_Errors(t *testing.T) {
	prof := &profile.Profile{
		ExtraMCPs: []profile.ExtraMCP{{
			Alias:   "x",
			Command: "x",
			Env:     map[string]string{"VAR": "${env:UNDEFINED_TRIAGENT_VAR}"},
		}},
	}
	_, err := writeMCPConfig(mcpConfigInputs{
		Dir:     t.TempDir(),
		MCPBin:  "/bin/triagent-mcp",
		Profile: prof,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNDEFINED_TRIAGENT_VAR")
}

func TestWriteMCPConfig_KubeconfigInjectedIntoEveryServer(t *testing.T) {
	dir := t.TempDir()
	path, err := writeMCPConfig(mcpConfigInputs{
		Dir:            dir,
		MCPBin:         "/usr/local/bin/triagent-mcp",
		Namespace:      "ns-y",
		KubeconfigPath: "/tmp/kubeconfig",
		LinkedRepos:    nil,
		SlackToken:     "xoxb-test",
	})
	require.NoError(t, err, "writeMCPConfig")
	body, err := os.ReadFile(path)
	require.NoError(t, err, "read mcp.json")
	var cfg struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(body, &cfg), "parse mcp.json")
	for alias, srv := range cfg.MCPServers {
		assert.Equal(t, "/tmp/kubeconfig", srv.Env["KUBECONFIG"], "server %q env KUBECONFIG", alias)
	}
}
