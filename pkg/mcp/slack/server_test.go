package slack

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_RejectsMissingToken(t *testing.T) {
	_, err := New(Options{CacheDir: t.TempDir()})
	require.Error(t, err, "want error for missing token")
}

func TestNew_RejectsBadTokenPrefix(t *testing.T) {
	_, err := New(Options{Token: "not-a-slack", CacheDir: t.TempDir()})
	require.Error(t, err, "want error for malformed token")
}

func TestNew_BootsWithoutChannel(t *testing.T) {
	// Channel scope is per-call now; the MCP must boot on token alone.
	srv, err := New(Options{Token: "xoxp-x", CacheDir: t.TempDir()})
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestResolveStore_RejectsEmptyChannel(t *testing.T) {
	srv, err := New(Options{Token: "xoxp-x", CacheDir: t.TempDir()})
	require.NoError(t, err)
	_, err = srv.resolveStore("")
	require.Error(t, err, "empty channel id must error")
}

func TestResolveStore_CachesPerChannel(t *testing.T) {
	srv, err := New(Options{Token: "xoxp-x", CacheDir: t.TempDir()})
	require.NoError(t, err)
	a, err := srv.resolveStore("C1")
	require.NoError(t, err)
	b, err := srv.resolveStore("C1")
	require.NoError(t, err)
	require.Same(t, a, b, "same channel id must return the same *Store")
	c, err := srv.resolveStore("C2")
	require.NoError(t, err)
	require.NotSame(t, a, c, "different channel ids must yield different stores")
}
