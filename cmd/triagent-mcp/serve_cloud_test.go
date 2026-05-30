package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunServe_CloudKindRequiresProvider(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "cloud"})
	require.Error(t, err, "expected error when --provider is missing")
	assert.Contains(t, err.Error(), "provider", "error should mention --provider")
}

func TestRunServe_CloudKindRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "cloud", cloudProvider: "azure"})
	require.Error(t, err, "expected error for an unknown provider")
	assert.Contains(t, err.Error(), "azure", "error should name the rejected provider")
}

func TestRunServe_UnknownKindErrorListsCloud(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "bogus"})
	require.Error(t, err, "expected error for unknown kind")
	assert.Contains(t, err.Error(), "cloud", "kind list should include cloud")
}

func TestServeCmd_KnowsCloudKind(t *testing.T) {
	t.Parallel()
	cmd := serveCmd()
	assert.Contains(t, cmd.Long, "cloud", "serve --help should list cloud")
}

func TestNewCloudProvider_AWSIsBuilt(t *testing.T) {
	t.Parallel()
	p, err := newCloudProvider("aws")
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "aws", p.Name())
}
