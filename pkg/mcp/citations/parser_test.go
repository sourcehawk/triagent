package citations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBlock_OK(t *testing.T) {
	raw := strings.TrimSpace(`
Responders rolled back at 14:02 [1] after 503s spiked [2].

<<<CITATIONS
[
  {"kind":"slack_thread","channel_id":"C1","thread_ts":"500.000100"},
  {"kind":"slack_thread","channel_id":"C1","thread_ts":"480.000100","message_ts":"485.000700"}
]
CITATIONS>>>
`)

	prose, cits, err := ParseBlock(raw)
	require.NoError(t, err)
	assert.Equal(t, "Responders rolled back at 14:02 [1] after 503s spiked [2].", strings.TrimSpace(prose))
	require.Len(t, cits, 2)
	assert.Equal(t, KindSlackThread, cits[0].Kind)
	assert.Equal(t, "C1", cits[0].ChannelID)
	assert.Equal(t, "485.000700", cits[1].MessageTS)
}

func TestParseBlock_NoBlock(t *testing.T) {
	prose, cits, err := ParseBlock("just prose, no block")
	require.Error(t, err)
	assert.Equal(t, "just prose, no block", prose, "no-block path returns input unchanged as prose")
	assert.Nil(t, cits)
}

func TestParseBlock_Unterminated(t *testing.T) {
	prose, cits, err := ParseBlock("body [1].\n\n<<<CITATIONS\n[]")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated")
	assert.Equal(t, "body [1].\n\n<<<CITATIONS\n[]", prose, "soft-fail surfaces input verbatim")
	assert.Nil(t, cits)
}

func TestParseBlock_MalformedJSON(t *testing.T) {
	raw := "prose [1].\n\n<<<CITATIONS\nnot-json\nCITATIONS>>>"
	prose, cits, err := ParseBlock(raw)
	require.Error(t, err)
	assert.Contains(t, prose, "prose [1]", "malformed-json path still returns the prose so soft-fail surfaces something")
	assert.Nil(t, cits)
}

func TestParseBlock_StripsTrailingWhitespace(t *testing.T) {
	raw := "Body line.\n\n<<<CITATIONS\n[]\nCITATIONS>>>\n\n   "
	prose, _, err := ParseBlock(raw)
	require.NoError(t, err)
	assert.Equal(t, "Body line.", strings.TrimSpace(prose))
}

func TestParseBlock_EmptyBodyIsZeroCitations(t *testing.T) {
	// A sub-agent with nothing to cite emits the markers with whitespace
	// (or nothing) between them. That should parse as []Citation, not as
	// a JSON syntax error — this is the legitimate "nothing to cite"
	// outcome.
	cases := []string{
		"Body line.\n\n<<<CITATIONS\nCITATIONS>>>",
		"Body line.\n\n<<<CITATIONS\n\nCITATIONS>>>",
		"Body line.\n\n<<<CITATIONS\n   \nCITATIONS>>>",
	}
	for _, raw := range cases {
		prose, cits, err := ParseBlock(raw)
		require.NoError(t, err, "raw=%q", raw)
		assert.Equal(t, "Body line.", strings.TrimSpace(prose), "raw=%q", raw)
		assert.NotNil(t, cits, "raw=%q: should be []Citation{}, not nil", raw)
		assert.Empty(t, cits, "raw=%q", raw)
	}
}
