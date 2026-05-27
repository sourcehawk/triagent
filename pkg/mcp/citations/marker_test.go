package citations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkerCheck_AllReferenced(t *testing.T) {
	cits := []Citation{
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"},
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "2"},
	}
	errs := MarkerCheck("claim [1] and claim [2]", cits)
	assert.Empty(t, errs)
}

func TestMarkerCheck_OrphanMarker(t *testing.T) {
	cits := []Citation{
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"},
	}
	errs := MarkerCheck("claim [1] then claim [2]", cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "[2]")
}

func TestMarkerCheck_UnusedEntry(t *testing.T) {
	cits := []Citation{
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"},
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "2"},
	}
	errs := MarkerCheck("only one claim [1]", cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "[2]")
}

func TestMarkerCheck_DupMarkerOK(t *testing.T) {
	// Multiple [1] markers in the prose are fine — they all resolve to the
	// same entry. Don't surface as an error.
	cits := []Citation{{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"}}
	errs := MarkerCheck("first [1] and again [1]", cits)
	assert.Empty(t, errs)
}

func TestMarkerCheck_IgnoresArrayIndexSyntax(t *testing.T) {
	// `processors[0]`, `args[1]`, etc. in the prose (code references) must
	// not be treated as citation markers. Real markers are preceded by
	// whitespace or punctuation, never glued to an identifier. This was a
	// false-positive that rejected valid envelopes in production.
	cits := []Citation{
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"},
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "2"},
	}
	prose := "asserts that `memory_limiter` is `processors[0]` [1] and that " +
		"`args[1]` resolves [2]"
	errs := MarkerCheck(prose, cits)
	assert.Empty(t, errs)
}

func TestMarkerCheck_AdjacentMarkers(t *testing.T) {
	// `[1][2]` adjacency (no separator) is a common shape — the second
	// marker is preceded by `]` (non-word), so it must still be detected.
	cits := []Citation{
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"},
		{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "2"},
	}
	errs := MarkerCheck("limit derived from cgroup [1][2] then wired", cits)
	assert.Empty(t, errs)
}

func TestMarkerCheck_StandaloneMarkerStillMatches(t *testing.T) {
	// Real citation markers can appear right after punctuation
	// (e.g. `).[1]`) or at the start of input. They must still match.
	cits := []Citation{{Kind: KindSlackThread, ChannelID: "C1", ThreadTS: "1"}}
	assert.Empty(t, MarkerCheck("[1] leading claim", cits))
	assert.Empty(t, MarkerCheck("claim).[1]", cits))
	assert.Empty(t, MarkerCheck("claim,[1] more", cits))
}
