package strategies

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListProposals_ReadsPendingDraftsFromTypeDirs covers the
// awaiting-review case: a draft on disk with no .resolved/<id>.json
// marker yields a row with status="awaiting_review".
func TestListProposals_ReadsPendingDraftsFromTypeDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ProposalsSubdir, "investigation"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ProposalsSubdir, "investigation", "stuck_reconciliation__abc123def456.yaml"),
		[]byte("id: stuck_reconciliation\n"), 0o600))

	got, err := ListProposals(dir)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "abc123def456", got[0].ProposalID)
	assert.Equal(t, "stuck_reconciliation", got[0].PlaybookID)
	assert.Equal(t, "investigation", got[0].Type)
	assert.Equal(t, "awaiting_review", got[0].Status)
	assert.NotEmpty(t, got[0].At, "awaiting drafts use file mtime so sub-agents see recency")
}

// TestListProposals_ReadsResolvedMarkers covers the approved and
// declined branches. The draft file is typically gone by the time the
// marker exists (decline deletes the draft), so the resolved marker is
// the only source of the playbook id + type + note.
func TestListProposals_ReadsResolvedMarkers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resolvedDir := filepath.Join(dir, ProposalsSubdir, ".resolved")
	require.NoError(t, os.MkdirAll(resolvedDir, 0o755))

	approved, _ := json.Marshal(map[string]any{
		"outcome":   "approved",
		"id":        "broker-crashloop",
		"type":      "investigation",
		"activated": true,
		"at":        "2026-05-07T10:00:00Z",
	})
	require.NoError(t, os.WriteFile(filepath.Join(resolvedDir, "ap111111aaaa.json"), approved, 0o644))

	declined, _ := json.Marshal(map[string]any{
		"outcome": "declined",
		"id":      "stuck_reconciliation",
		"type":    "investigation",
		"at":      "2026-05-20T20:51:30Z",
		"note":    "split into two entries — one per cluster",
	})
	require.NoError(t, os.WriteFile(filepath.Join(resolvedDir, "de222222bbbb.json"), declined, 0o644))

	got, err := ListProposals(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)

	// Sort is most-recent first; declined is later.
	assert.Equal(t, "de222222bbbb", got[0].ProposalID)
	assert.Equal(t, "declined", got[0].Status)
	assert.Equal(t, "stuck_reconciliation", got[0].PlaybookID)
	assert.Equal(t, "split into two entries — one per cluster", got[0].Note,
		"decline note must surface so sub-agents adjust without re-submitting the same shape")

	assert.Equal(t, "ap111111aaaa", got[1].ProposalID)
	assert.Equal(t, "approved", got[1].Status)
	assert.True(t, got[1].Activated)
}

// TestListProposals_MixedPendingAndResolved is the realistic shape:
// some drafts awaiting review, some recently resolved.
func TestListProposals_MixedPendingAndResolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ProposalsSubdir, "investigation"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ProposalsSubdir, ".resolved"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ProposalsSubdir, "investigation", "cluster_health__pending123.yaml"),
		[]byte("id: cluster_health\n"), 0o600))
	declined, _ := json.Marshal(map[string]any{
		"outcome": "declined", "id": "stuck_reconciliation", "type": "investigation",
		"at": "2026-05-20T20:51:30Z",
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, ProposalsSubdir, ".resolved", "olddeclined1.json"), declined, 0o644))

	got, err := ListProposals(dir)
	require.NoError(t, err)
	assert.Len(t, got, 2, "pending draft + resolved marker → two rows")

	statuses := map[string]string{}
	for _, p := range got {
		statuses[p.ProposalID] = p.Status
	}
	assert.Equal(t, "awaiting_review", statuses["pending123"])
	assert.Equal(t, "declined", statuses["olddeclined1"])
}

// TestListProposals_MissingDirReturnsEmpty: brand-new launcher with no
// proposals dir on disk yet must not error — sub-agents that list at
// dispatch time during a first investigation would otherwise fail.
func TestListProposals_MissingDirReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := ListProposals(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestListProposalsTool_ReturnsViaMCPHandler exercises the wired tool
// surface that the dispatched sub-agent will call.
func TestListProposalsTool_ReturnsViaMCPHandler(t *testing.T) {
	t.Parallel()
	srv := newServerWithUserPlaybooksDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(srv.userPlaybooksDir, ProposalsSubdir, ".resolved"), 0o755))
	declined, _ := json.Marshal(map[string]any{
		"outcome": "declined", "id": "stuck_reconciliation", "type": "investigation",
		"at": "2026-05-20T20:51:30Z", "note": "split it up",
	})
	require.NoError(t, os.WriteFile(
		filepath.Join(srv.userPlaybooksDir, ProposalsSubdir, ".resolved", "declined0001.json"),
		declined, 0o644))

	_, out, err := srv.listProposals(context.Background(), nil, listProposalsIn{})
	require.NoError(t, err)
	require.Len(t, out.Proposals, 1)
	assert.Equal(t, "declined", out.Proposals[0].Status)
	assert.Equal(t, "split it up", out.Proposals[0].Note)
}
