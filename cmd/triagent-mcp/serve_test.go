package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServeCmd_KnowsAgentOperatorKind(t *testing.T) {
	cmd := serveCmd()
	require.NotNil(t, cmd)
	require.Contains(t, cmd.Long, "agent-operator")
}
