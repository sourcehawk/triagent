package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalRing_AppendAndReplay_RecentOnly(t *testing.T) {
	t.Parallel()
	ring := newGlobalRing()

	old := time.Now().Add(-10 * time.Minute).UTC()
	mid := time.Now().Add(-2 * time.Minute).UTC()
	now := time.Now().UTC()

	ring.append(GlobalEventEnvelope{Seq: 1, Kind: "repo_summary_state", Timestamp: old})
	ring.append(GlobalEventEnvelope{Seq: 2, Kind: "repo_summary_state", Timestamp: mid})
	ring.append(GlobalEventEnvelope{Seq: 3, Kind: "repo_summary_state", Timestamp: now})

	got := ring.replay(now)
	require.Len(t, got, 2, "replay must drop events older than the 5-minute window")
	assert.Equal(t, 2, got[0].Seq)
	assert.Equal(t, 3, got[1].Seq)
}

func TestGlobalRing_AppendCapsAt50(t *testing.T) {
	t.Parallel()
	ring := newGlobalRing()
	now := time.Now().UTC()
	for i := 1; i <= 60; i++ {
		ring.append(GlobalEventEnvelope{Seq: i, Kind: "repo_summary_state", Timestamp: now})
	}
	got := ring.replay(now)
	assert.Len(t, got, 50, "ring is capped at 50 entries")
	assert.Equal(t, 11, got[0].Seq, "oldest 10 envelopes were evicted")
	assert.Equal(t, 60, got[49].Seq)
}

func TestRepoSummaryStatePayload_RoundTripsJSON(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	payload := RepoSummaryStatePayload{
		Owner:       "example-org",
		Name:        "operate",
		Phase:       "success",
		GeneratedAt: &when,
		ByteCount:   8421,
		Kind:        "freeform",
	}
	env := GlobalEventEnvelope{
		Seq:         1,
		Kind:        "repo_summary_state",
		Timestamp:   when,
		RepoSummary: &payload,
	}
	body, err := json.Marshal(env)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"phase":"success"`)
	assert.Contains(t, string(body), `"owner":"example-org"`)
	assert.Contains(t, string(body), `"byteCount":8421`)
}
