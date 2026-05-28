package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/auto"
	"github.com/sourcehawk/triagent/internal/promforward"
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

// TestPersistedMetadata_PromRoundTrip verifies that PromTarget and PromDisabled
// survive a write/load cycle. This covers the rehydrate-after-restart path for
// per-investigation prom overrides.
func TestPersistedMetadata_PromRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("with_target", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		s := newStore(dir)
		t.Cleanup(s.close)

		dto := InvestigationDTO{
			ID:        "inv-prom",
			Namespace: "ns",
			SessionDir: dir,
			CreatedAt: time.Now().UTC(),
			PromTarget: &promforward.Target{
				Service:   "prometheus-server",
				Namespace: "monitoring",
				Port:      9090,
			},
			PromDisabled: false,
		}
		require.NoError(t, s.writeMetadata(dto))

		inv, err := loadInvestigation(dir)
		require.NoError(t, err)
		require.NotNil(t, inv.PromTarget, "PromTarget should survive round-trip")
		assert.Equal(t, "prometheus-server", inv.PromTarget.Service)
		assert.Equal(t, "monitoring", inv.PromTarget.Namespace)
		assert.Equal(t, 9090, inv.PromTarget.Port)
		assert.False(t, inv.PromDisabled)
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		s := newStore(dir)
		t.Cleanup(s.close)

		dto := InvestigationDTO{
			ID:           "inv-prom-disabled",
			Namespace:    "ns",
			SessionDir:   dir,
			CreatedAt:    time.Now().UTC(),
			PromTarget:   nil,
			PromDisabled: true,
		}
		require.NoError(t, s.writeMetadata(dto))

		inv, err := loadInvestigation(dir)
		require.NoError(t, err)
		assert.Nil(t, inv.PromTarget, "nil PromTarget should survive round-trip")
		assert.True(t, inv.PromDisabled, "PromDisabled=true should survive round-trip")
	})

	t.Run("no_prom_configured", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		s := newStore(dir)
		t.Cleanup(s.close)

		dto := InvestigationDTO{
			ID:        "inv-no-prom",
			Namespace: "ns",
			SessionDir: dir,
			CreatedAt: time.Now().UTC(),
		}
		require.NoError(t, s.writeMetadata(dto))

		inv, err := loadInvestigation(dir)
		require.NoError(t, err)
		assert.Nil(t, inv.PromTarget, "absent PromTarget should stay nil")
		assert.False(t, inv.PromDisabled)
	})
}
