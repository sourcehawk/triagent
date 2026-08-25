package sessions

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowedTools_IncludesParallel(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		ExtraMCPs: []profile.ExtraMCP{{Alias: "example-docs", Description: "docs"}},
	}
	got := allowedTools(prof, nil, false, false)
	want := "mcp__" + preflight.MCPAliasParallel + "__*"
	require.Contains(t, got, want)
}

func TestAllowedTools_AlwaysIncludesCoreServers(t *testing.T) {
	t.Parallel()
	got := allowedTools(nil, nil, false, false)
	for _, alias := range []string{
		preflight.MCPAliasK8s,
		preflight.MCPAliasStrategies,
		preflight.MCPAliasMeta,
		preflight.MCPAliasParallel,
		preflight.MCPAliasWiki,
	} {
		require.Contains(t, got, "mcp__"+alias+"__*", "missing core server alias %s", alias)
	}
}

func TestAllowedTools_TeleportGatedOnAuthKind(t *testing.T) {
	t.Parallel()
	teleportGlob := "mcp__" + preflight.MCPAliasTeleport + "__*"
	// mcpconfig.go only wires the teleport MCP for a teleport deployment;
	// the allowlist must match so a kubeconfig deployment carries no glob
	// for tools that never exist.
	require.Contains(t,
		allowedTools(&profile.Profile{Auth: profile.Auth{Kind: "teleport"}}, nil, false, false),
		teleportGlob, "teleport deployment should allowlist the teleport MCP")
	require.NotContains(t,
		allowedTools(&profile.Profile{Auth: profile.Auth{Kind: "kubeconfig"}}, nil, false, false),
		teleportGlob, "kubeconfig deployment must not allowlist the teleport MCP")
}

func TestAllowedTools_IncludesExtraMCPs(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		ExtraMCPs: []profile.ExtraMCP{
			{Alias: "example-docs", Description: "docs"},
			{Alias: "other-mcp", Description: "other"},
			{
				Alias:        "prom-spawn",
				AllowedTools: []string{"mcp__prom-spawn__cpu_pressure"},
			},
		},
	}
	got := allowedTools(prof, nil, false, false)
	require.Contains(t, got, "mcp__example-docs__*", "expected example-docs in allowed tools")
	require.Contains(t, got, "mcp__other-mcp__*", "expected other-mcp in allowed tools")
	require.Contains(t, got, "mcp__prom-spawn__cpu_pressure", "expected narrow tool when AllowedTools set")
	require.NotContains(t, got, "mcp__prom-spawn__*", "wildcard must be absent when AllowedTools narrows")
}

func TestRenderSystemPromptAddendum_NoProfileHintAppendsNothingExtra(t *testing.T) {
	t.Parallel()
	got := renderSystemPromptAddendum(systemPromptAddendum, profile.NamespaceDerivation{}, nil)
	assert.Equal(t, systemPromptAddendum, got)
}

func TestK8sAuthGuidance_TeleportDeploymentNamesTeleportSequence(t *testing.T) {
	t.Parallel()
	// Front-loading the auth sequence into the system prompt steers the
	// agent to do it proactively instead of failing the first k8s call
	// and reacting. Verify the tool names are spelled exactly so a refactor
	// of any one of them surfaces the hint as out-of-date.
	got := k8sAuthGuidance(&profile.Profile{Auth: profile.Auth{Kind: "teleport"}})
	for _, want := range []string{
		"triagent-teleport.list_clusters",
		"triagent-teleport.login",
		"triagent-k8s.switch_context",
		"KubeContext",
	} {
		assert.Contains(t, got, want,
			"teleport-deployment guidance should mention %q so the agent knows the auth recipe up front", want)
	}
}

func TestK8sAuthGuidance_KubeconfigDeploymentNamesContextSequenceNotTeleport(t *testing.T) {
	t.Parallel()
	// A kubeconfig deployment has no Teleport MCP — the contexts already
	// live in the kubeconfig. Steering the agent at list_clusters/login
	// there sends it after tools that don't exist (the original bug).
	for _, prof := range []*profile.Profile{
		{Auth: profile.Auth{Kind: "kubeconfig"}},
		nil,
	} {
		got := k8sAuthGuidance(prof)
		assert.Contains(t, got, "triagent-k8s.list_contexts",
			"kubeconfig guidance should steer at list_contexts")
		assert.Contains(t, got, "triagent-k8s.switch_context",
			"kubeconfig guidance should steer at switch_context")
		assert.NotContains(t, got, "triagent-teleport",
			"kubeconfig guidance must not mention the Teleport MCP")
	}
}

func TestRenderSystemPromptAddendum_TemplateRendersOneHint(t *testing.T) {
	t.Parallel()
	cfg := profile.NamespaceDerivation{Template: "${project_id}-zeebe"}
	fields := map[string]string{"project_id": "saas"}
	got := renderSystemPromptAddendum(systemPromptAddendum, cfg, fields)
	assert.True(t, strings.Contains(got, systemPromptAddendum), "must preserve the base addendum")
	assert.True(t, strings.Contains(got, "Suggested namespace(s): saas-zeebe"), "must append the rendered hint")
}

func TestRenderSystemPromptAddendum_NoMatchAppendsNothing(t *testing.T) {
	t.Parallel()
	cfg := profile.NamespaceDerivation{Template: "${absent}-zeebe"}
	got := renderSystemPromptAddendum(systemPromptAddendum, cfg, map[string]string{"other": "x"})
	assert.Equal(t, systemPromptAddendum, got)
}

func TestNew_ForwardsProfileInvestigationModel(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not on PATH; skipping")
	}
	opts := Options{
		Profile: &profile.Profile{Models: profile.Models{Investigation: "claude-opus-4-7"}},
	}
	sess, err := New(opts)
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-7", sess.inner.Model())
}

func TestStartPrompt_EmitsSeededKubeContext(t *testing.T) {
	t.Parallel()
	s := &Session{opts: Options{
		Cluster: "camunda.teleport.sh-saas-int-worker-3",
		Profile: &profile.Profile{},
	}}
	got := s.startPrompt()
	assert.Contains(t, got, "kubernetes-context: camunda.teleport.sh-saas-int-worker-3\n")
	assert.NotContains(t, got, "kubernetes-context: <unset>")
}

func TestStartPrompt_UnsetWhenNoContextSeeded(t *testing.T) {
	t.Parallel()
	s := &Session{opts: Options{Profile: &profile.Profile{}}}
	assert.Contains(t, s.startPrompt(), "kubernetes-context: <unset>\n")
}
