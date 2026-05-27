package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/auto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir)
	t.Cleanup(s.close)

	startedAt := time.Date(2026, 5, 8, 1, 2, 3, 0, time.UTC)
	dto := InvestigationDTO{
		ID:             "abc",
		SessionDir:     dir,
		CreatedAt:      time.Now().UTC(),
		PushInProgress: true,
		PushStartedAt:  &startedAt,
		PushError:      "previous error",
	}
	require.NoError(t, s.writeMetadata(dto), "writeMetadata")

	inv, err := loadInvestigation(dir)
	require.NoError(t, err, "loadInvestigation")
	assert.True(t, inv.PushInProgress, "PushInProgress not restored")
	assert.True(t, inv.PushStartedAt != nil && inv.PushStartedAt.Equal(startedAt),
		"PushStartedAt = %v, want %v", inv.PushStartedAt, startedAt)
	assert.Equal(t, "previous error", inv.PushError, "PushError")
}

func TestPersist_ResumeFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir)
	t.Cleanup(s.close)

	dto := InvestigationDTO{
		ID:              "rid",
		Namespace:       "ns",
		MCPConfigPath:   filepath.Join(dir, "mcp.json"),
		SessionDir:      dir,
		CreatedAt:       time.Now().UTC(),
		ClaudeSessionID: "claude-abc",
		LaunchCwd:       dir,
		KubeconfigPath:  "/tmp/kc.yaml",
	}
	require.NoError(t, s.writeMetadata(dto), "writeMetadata")
	inv, err := loadInvestigation(dir)
	require.NoError(t, err, "loadInvestigation")
	assert.Equal(t, "claude-abc", inv.ClaudeSessionID, "ClaudeSessionID")
	assert.Equal(t, dir, inv.LaunchCwd, "LaunchCwd")
	assert.Equal(t, "/tmp/kc.yaml", inv.KubeconfigPath, "KubeconfigPath")
}

func TestPersistMetadata_LabelRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir)
	t.Cleanup(s.close)

	now := time.Now().UTC()
	dto := InvestigationDTO{
		ID:            "abc123",
		Namespace:     "cluster-x-zeebe",
		Label:         "OOMKilled in zeebe-broker after 8.7 deploy",
		MCPConfigPath: filepath.Join(dir, "mcp.json"),
		SessionDir:    dir,
		CreatedAt:     now,
	}
	require.NoError(t, s.writeMetadata(dto), "writeMetadata")
	inv, err := loadInvestigation(dir)
	require.NoError(t, err, "loadInvestigation")
	require.NotNil(t, inv, "expected investigation, got nil")
	assert.Equal(t, dto.Label, inv.Label, "Label round-trip")
}

func TestAutoStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir)
	t.Cleanup(s.close)
	dto := InvestigationDTO{
		ID: "abc", SessionDir: dir, CreatedAt: time.Now().UTC(),
		Auto: auto.State{
			Enabled: true, Phase: auto.PhaseStarted,
			OperatorSessionID: "op-1", LastSentSeq: 7,
		},
	}
	require.NoError(t, s.writeMetadata(dto))
	inv, err := loadInvestigation(dir)
	require.NoError(t, err)
	require.True(t, inv.Auto.Enabled)
	require.Equal(t, auto.PhaseStarted, inv.Auto.Phase)
	require.Equal(t, "op-1", inv.Auto.OperatorSessionID)
	require.Equal(t, 7, inv.Auto.LastSentSeq)
}
