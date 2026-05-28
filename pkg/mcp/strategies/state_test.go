package strategies

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStore_PersistsMultipleSessionsAcrossRestart pins the requirement that
// every session created in the store survives a process restart. A real
// investigation can open two top-level walkers (e.g. an investigation
// playbook plus a parallel strategic_log_extraction probe); the launcher
// then re-spawns MCP processes between Claude sessions, dropping any
// sessions not written to disk. The previous implementation snapshotted
// every session to the same `strategy.json`, so each new session silently
// overwrote the prior one and only the last-touched session could be
// recovered.
func TestStore_PersistsMultipleSessionsAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s1 := newStore(dir)
	a := &Session{ID: "aaa", PlaybookID: "investigation", CurrentNode: "n1", Visited: []string{"n1"}, StartedAt: time.Now().UTC()}
	b := &Session{ID: "bbb", PlaybookID: "strategic_log_extraction", CurrentNode: "probe_structure", Visited: []string{"probe_structure"}, StartedAt: time.Now().UTC()}
	require.NoError(t, s1.create(a))
	require.NoError(t, s1.create(b))

	// Mutate B after A to mimic the real-world sequence where B is the
	// most-recently-snapshotted session. Earlier implementations would
	// leave only B on disk.
	_, err := s1.update("bbb", func(s *Session) { s.CurrentNode = "stream_capture" })
	require.NoError(t, err)

	s2 := newStore(dir)
	gotA, err := s2.get("aaa")
	require.NoError(t, err, "session aaa must survive restart")
	require.Equal(t, "investigation", gotA.PlaybookID)

	gotB, err := s2.get("bbb")
	require.NoError(t, err, "session bbb must survive restart")
	require.Equal(t, "stream_capture", gotB.CurrentNode, "latest snapshot of bbb must be recovered")
}
