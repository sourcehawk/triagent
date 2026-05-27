package wiki

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProposalID_Format(t *testing.T) {
	t.Parallel()
	id := NewProposalID()
	assert.True(t, strings.HasPrefix(id, "prop-"), "expected prop- prefix: %q", id)
	assert.Len(t, id, len("prop-")+12, "expected 12-hex-char suffix: %q", id)
}

func TestWriteAndReadProposal_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := "---\nid: inc-12345-foo\n---\nbody\n"
	id := "prop-abc123def456"

	path, err := WriteProposal(dir, id, "inc-12345-foo", []byte(body))
	require.NoError(t, err, "WriteProposal")
	assert.Contains(t, path, id, "path missing proposal id")
	assert.Contains(t, path, "inc-12345-foo", "path missing incident id")

	got, gotIncident, err := ReadProposal(dir, id)
	require.NoError(t, err, "ReadProposal")
	assert.Equal(t, body, string(got), "body mismatch")
	assert.Equal(t, "inc-12345-foo", gotIncident, "incident id mismatch")
}

func TestDeleteProposal_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	id := "prop-abc123def456"
	_, err := WriteProposal(dir, id, "inc-12345-foo", []byte("x"))
	require.NoError(t, err)
	require.NoError(t, DeleteProposal(dir, id), "first delete")
	require.NoError(t, DeleteProposal(dir, id), "second delete (should be no-op)")
	// Confirm gone from filesystem.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), id, "expected proposal removed")
	}
}
