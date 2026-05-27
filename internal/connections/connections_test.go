package connections

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetGetSlackToken_FileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewWithDir(dir)
	require.NoError(t, m.SetSlackToken("xoxp-abc", ""))
	got, err := m.GetSlackToken()
	require.NoError(t, err)
	assert.Equal(t, "xoxp-abc", got)
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestEnvOverride_BeatsFile(t *testing.T) {
	dir := t.TempDir()
	m := NewWithDir(dir)
	require.NoError(t, m.SetSlackToken("xoxp-from-file", ""))
	t.Setenv(envSlack, "xoxp-from-env")
	got, _ := m.GetSlackToken()
	assert.Equal(t, "xoxp-from-env", got, "env should win")
}

func TestSetSlackToken_RejectsBadPrefix(t *testing.T) {
	m := NewWithDir(t.TempDir())
	require.Error(t, m.SetSlackToken("not-a-slack-token", ""), "want error for malformed token")
}

func TestSetIncidentioToken_RejectsEmpty(t *testing.T) {
	m := NewWithDir(t.TempDir())
	require.Error(t, m.SetIncidentioToken(""), "want error for empty token")
	require.Error(t, m.SetIncidentioToken("   "), "want error for whitespace-only token")
}

func TestStatus_ReportsConnectedAfterSet(t *testing.T) {
	dir := t.TempDir()
	m := NewWithDir(dir)
	got := m.Status()
	assert.False(t, got.Slack, "want empty status")
	assert.False(t, got.Incidentio, "want empty status")
	_ = m.SetSlackToken("xoxp-x", "")
	_ = m.SetIncidentioToken("io-key")
	got = m.Status()
	assert.True(t, got.Slack, "want both connected")
	assert.True(t, got.Incidentio, "want both connected")
}

func TestClear_DropsToken(t *testing.T) {
	dir := t.TempDir()
	m := NewWithDir(dir)
	_ = m.SetSlackToken("xoxp-x", "")
	require.NoError(t, m.Clear(KindSlack))
	got, _ := m.GetSlackToken()
	assert.Empty(t, got, "want cleared")
}

func TestClear_IdempotentOnEmpty(t *testing.T) {
	m := NewWithDir(t.TempDir())
	require.NoError(t, m.Clear(KindIncidentio), "clear empty should succeed")
}

func TestClear_RejectsUnknownKind(t *testing.T) {
	m := NewWithDir(t.TempDir())
	require.Error(t, m.Clear(Kind("github")), "want error for unknown kind")
}

func TestPersistsAcrossManagers(t *testing.T) {
	dir := t.TempDir()
	m1 := NewWithDir(dir)
	_ = m1.SetSlackToken("xoxp-persist", "")
	m2 := NewWithDir(dir)
	got, _ := m2.GetSlackToken()
	assert.Equal(t, "xoxp-persist", got)
}

func TestSetSlackToken_PersistsWorkspaceURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := NewWithDir(dir)

	require.NoError(t, m.SetSlackToken("xoxp-abc", "https://example.slack.com"))

	tok, err := m.GetSlackToken()
	require.NoError(t, err)
	assert.Equal(t, "xoxp-abc", tok)

	wsURL, err := m.GetSlackWorkspaceURL()
	require.NoError(t, err)
	assert.Equal(t, "https://example.slack.com", wsURL)
}

func TestSetSlackToken_EmptyWorkspaceURLAccepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := NewWithDir(dir)

	// Empty workspace URL must be accepted — auth.test failure shouldn't
	// block token storage. The picker degrades gracefully.
	require.NoError(t, m.SetSlackToken("xoxp-abc", ""))

	wsURL, err := m.GetSlackWorkspaceURL()
	require.NoError(t, err)
	assert.Empty(t, wsURL)
}
