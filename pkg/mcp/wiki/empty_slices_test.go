package wiki

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWikiSearch_EmptyHitsMarshalAsArray verifies that when no entries
// match, `hits` is emitted as `[]` rather than `null`. Agents reading
// the tool output read `null` as failure (we've seen this in session
// transcripts); `[]` reads unambiguously as "zero results". This is
// purely a JSON-shape fix; semantics are unchanged.
func TestWikiSearch_EmptyHitsMarshalAsArray(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	// Query for something that won't match anything in seedVault.
	_, out, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{Query: "xyzzy-nothing-matches"})
	require.NoError(t, err)
	require.Empty(t, out.Hits, "no entries match — Hits is empty")

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"hits":null`,
		"empty result must marshal as `\"hits\":[]`, not `null`")
	require.True(t, strings.Contains(string(raw), `"hits":[]`),
		"expected `\"hits\":[]` in output, got: %s", raw)
}
