package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventEnvelope_AutoModeStateJSON(t *testing.T) {
	env := EventEnvelope{
		Seq:  1,
		Kind: envKindAutoModeState,
		AutoMode: &AutoModePayload{
			Phase:       "paused",
			TakenOverBy: "human",
			Reason:      "operator requested",
		},
	}
	b, err := json.Marshal(env)
	require.NoError(t, err)
	require.Contains(t, string(b), `"kind":"auto_mode_state"`)
	require.Contains(t, string(b), `"phase":"paused"`)
	require.Contains(t, string(b), `"takenOverBy":"human"`)
}

func TestEventEnvelope_UserOrigin(t *testing.T) {
	env := EventEnvelope{Seq: 2, Kind: envKindUser, Origin: "operator", Text: "wiki"}
	b, err := json.Marshal(env)
	require.NoError(t, err)
	require.Contains(t, string(b), `"origin":"operator"`)
}

// TestFromClaudeEvent_AssistantCarriesUsage asserts that the usage block
// claude attaches to an assistant message survives the conversion into
// an EventEnvelope. The frontend reads usage off the envelope to render
// session-level totals — drop it here and the UI goes blank.
func TestFromClaudeEvent_AssistantCarriesUsage(t *testing.T) {
	env, ok := fromClaudeEvent(claude.Event{
		Kind: claude.EventAssistant,
		Text: "hi",
		Usage: &claude.Usage{
			InputTokens:              10,
			OutputTokens:             20,
			CacheCreationInputTokens: 30,
			CacheReadInputTokens:     40,
		},
	})
	require.True(t, ok)
	require.NotNil(t, env.Usage)
	assert.Equal(t, 10, env.Usage.InputTokens)
	assert.Equal(t, 20, env.Usage.OutputTokens)
	assert.Equal(t, 30, env.Usage.CacheCreationInputTokens)
	assert.Equal(t, 40, env.Usage.CacheReadInputTokens)
}

// TestFromClaudeEvent_ResultCarriesCostNotUsage asserts that the final
// result line's total_cost_usd survives into the envelope but the
// last-API-call usage snapshot does NOT — the canonical per-API-call
// usage flows through dedicated `usage` envelopes, summed for the
// session total. CostUSD is the invocation roll-up and remains on
// result envelopes.
func TestFromClaudeEvent_ResultCarriesCostNotUsage(t *testing.T) {
	env, ok := fromClaudeEvent(claude.Event{
		Kind:      claude.EventResult,
		Subtype:   "success",
		FinalText: "done",
		Usage: &claude.Usage{
			InputTokens:  100,
			OutputTokens: 200,
		},
		CostUSD: 0.0456,
	})
	require.True(t, ok)
	assert.Nil(t, env.Usage, "result envelope must not carry the last-API-call snapshot — usage flows via envKindUsage")
	assert.InDelta(t, 0.0456, env.CostUSD, 1e-9)
}

// TestFromClaudeEvent_UsageEventCarriesUsage asserts the dedicated
// EventUsage maps to an envKindUsage envelope carrying the per-API-call
// token tally. This is the source of truth for the per-investigation
// token total.
func TestFromClaudeEvent_UsageEventCarriesUsage(t *testing.T) {
	env, ok := fromClaudeEvent(claude.Event{
		Kind: claude.EventUsage,
		Usage: &claude.Usage{
			InputTokens:              7,
			OutputTokens:             11,
			CacheCreationInputTokens: 13,
			CacheReadInputTokens:     17,
		},
	})
	require.True(t, ok)
	assert.Equal(t, envKindUsage, env.Kind)
	require.NotNil(t, env.Usage)
	assert.Equal(t, 7, env.Usage.InputTokens)
	assert.Equal(t, 17, env.Usage.CacheReadInputTokens)
}

// TestInvestigation_SnapshotAggregatesUsage publishes a sequence of
// usage envelopes (per-API-call tallies) + result envelopes (cost
// roll-ups) and asserts the Snapshot reports correctly partitioned
// totals: tokens from usage envelopes, cost from result envelopes.
func TestInvestigation_SnapshotAggregatesUsage(t *testing.T) {
	inv := &Investigation{ID: "inv-usage"}
	inv.publish(EventEnvelope{Kind: envKindUsage, Usage: &claude.Usage{InputTokens: 100, OutputTokens: 200, CacheReadInputTokens: 50}})
	inv.publish(EventEnvelope{Kind: envKindUsage, Usage: &claude.Usage{InputTokens: 30, OutputTokens: 40, CacheCreationInputTokens: 10}})
	inv.publish(EventEnvelope{Kind: envKindResult, CostUSD: 0.10})
	inv.publish(EventEnvelope{Kind: envKindResult, CostUSD: 0.05})
	// Non-usage / non-result envelopes must not contribute, even if
	// they happen to carry a Usage field (e.g. an assistant envelope
	// that legacy paths stamped Usage onto).
	inv.publish(EventEnvelope{Kind: envKindAssistant, Text: "noise", Usage: &claude.Usage{InputTokens: 999}})

	snap := inv.Snapshot()
	require.NotNil(t, snap.Usage)
	assert.Equal(t, 130, snap.Usage.InputTokens)
	assert.Equal(t, 240, snap.Usage.OutputTokens)
	assert.Equal(t, 10, snap.Usage.CacheCreationInputTokens)
	assert.Equal(t, 50, snap.Usage.CacheReadInputTokens)
	assert.InDelta(t, 0.15, snap.CostUSD, 1e-9)
}

// TestInvestigation_SnapshotNoUsage asserts Snapshot reports nil/zero
// totals for sessions that haven't seen a result envelope yet — both
// JSON outputs cleanly omit the fields under omitempty.
func TestInvestigation_SnapshotNoUsage(t *testing.T) {
	inv := &Investigation{ID: "inv-empty"}
	inv.publish(EventEnvelope{Kind: envKindAssistant, Text: "hi"})

	snap := inv.Snapshot()
	assert.Nil(t, snap.Usage)
	assert.Equal(t, 0.0, snap.CostUSD)
}

// TestRestore_ReplaysUsageTotals seeds an events.jsonl with per-API-call
// usage envelopes + result envelopes, restores the investigation, and
// asserts the Snapshot surfaces the correctly partitioned totals
// (tokens from usage, cost from result). Guards against a launcher
// restart blanking the sidebar / chat footer numbers for resumed
// sessions.
func TestRestore_ReplaysUsageTotals(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess-usage")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	st := newStore(dir)
	require.NoError(t, st.writeMetadata(InvestigationDTO{
		ID:         "sess-usage",
		SessionDir: dir,
		CreatedAt:  time.Now().UTC(),
	}))
	st.close()

	lines := []EventEnvelope{
		{Seq: 1, Kind: envKindUsage, Usage: &claude.Usage{InputTokens: 100, OutputTokens: 200}},
		{Seq: 2, Kind: envKindAssistant, Text: "ignored"},
		{Seq: 3, Kind: envKindUsage, Usage: &claude.Usage{InputTokens: 30, OutputTokens: 40, CacheReadInputTokens: 5}},
		{Seq: 4, Kind: envKindResult, CostUSD: 0.10},
		{Seq: 5, Kind: envKindResult, CostUSD: 0.05},
	}
	var buf strings.Builder
	for _, env := range lines {
		b, err := json.Marshal(env)
		require.NoError(t, err)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(buf.String()), 0o600))

	m := NewManager(context.Background(), root)
	require.NoError(t, m.Restore())
	inv := m.Get("sess-usage")
	require.NotNil(t, inv)

	snap := inv.Snapshot()
	require.NotNil(t, snap.Usage)
	assert.Equal(t, 130, snap.Usage.InputTokens)
	assert.Equal(t, 240, snap.Usage.OutputTokens)
	assert.Equal(t, 5, snap.Usage.CacheReadInputTokens)
	assert.InDelta(t, 0.15, snap.CostUSD, 1e-9)
}

// TestRestore_LegacySessionsFallBackToResultUsage covers the
// backwards-compatibility path for sessions persisted before the
// EventUsage envelope existed. Those events.jsonl files have Usage
// stamped on result envelopes only. Rehydrating them must keep
// surfacing those (still under-reporting) totals rather than blanking
// the sidebar to zero.
func TestRestore_LegacySessionsFallBackToResultUsage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess-legacy")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	st := newStore(dir)
	require.NoError(t, st.writeMetadata(InvestigationDTO{
		ID:         "sess-legacy",
		SessionDir: dir,
		CreatedAt:  time.Now().UTC(),
	}))
	st.close()

	// Legacy shape: Usage rides on result envelopes only; no envKindUsage.
	lines := []EventEnvelope{
		{Seq: 1, Kind: envKindResult, Usage: &claude.Usage{InputTokens: 100, OutputTokens: 200}, CostUSD: 0.10},
		{Seq: 2, Kind: envKindResult, Usage: &claude.Usage{InputTokens: 30, OutputTokens: 40, CacheReadInputTokens: 5}, CostUSD: 0.05},
	}
	var buf strings.Builder
	for _, env := range lines {
		b, err := json.Marshal(env)
		require.NoError(t, err)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(buf.String()), 0o600))

	m := NewManager(context.Background(), root)
	require.NoError(t, m.Restore())
	inv := m.Get("sess-legacy")
	require.NotNil(t, inv)

	snap := inv.Snapshot()
	require.NotNil(t, snap.Usage)
	assert.Equal(t, 130, snap.Usage.InputTokens)
	assert.Equal(t, 240, snap.Usage.OutputTokens)
	assert.Equal(t, 5, snap.Usage.CacheReadInputTokens)
	assert.InDelta(t, 0.15, snap.CostUSD, 1e-9)
}

// TestEventEnvelope_UsageRoundTripsJSON asserts that an envelope with
// usage + cost round-trips through encoding/json with the camelCase
// field names the frontend expects.
func TestEventEnvelope_UsageRoundTripsJSON(t *testing.T) {
	env := EventEnvelope{
		Seq:  3,
		Kind: envKindResult,
		Usage: &claude.Usage{
			InputTokens:              1,
			OutputTokens:             2,
			CacheCreationInputTokens: 3,
			CacheReadInputTokens:     4,
		},
		CostUSD: 1.5,
	}
	b, err := json.Marshal(env)
	require.NoError(t, err)
	s := string(b)
	require.Contains(t, s, `"usage":`)
	require.Contains(t, s, `"inputTokens":1`)
	require.Contains(t, s, `"outputTokens":2`)
	require.Contains(t, s, `"cacheCreationInputTokens":3`)
	require.Contains(t, s, `"cacheReadInputTokens":4`)
	require.Contains(t, s, `"costUsd":1.5`)
}
