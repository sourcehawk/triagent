package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeCmd_KnowsAgentOperatorKind(t *testing.T) {
	cmd := serveCmd()
	require.NotNil(t, cmd)
	require.Contains(t, cmd.Long, "agent-operator")
}

func TestResolveFlags_ReadsTeleportProxyAndConnectorFromEnv(t *testing.T) {
	t.Setenv("TRIAGENT_MCP_TELEPORT_PROXY", "proxy.example.com")
	t.Setenv("TRIAGENT_MCP_TELEPORT_AUTH_CONNECTOR", "github")
	got := resolveFlags(&serveFlags{})
	assert.Equal(t, "proxy.example.com", got.teleportProxy)
	assert.Equal(t, "github", got.teleportAuthConnector)
}

func TestResolveFlags_ExplicitTeleportFlagsWinOverEnv(t *testing.T) {
	t.Setenv("TRIAGENT_MCP_TELEPORT_PROXY", "from-env")
	got := resolveFlags(&serveFlags{teleportProxy: "from-flag"})
	assert.Equal(t, "from-flag", got.teleportProxy)
}
