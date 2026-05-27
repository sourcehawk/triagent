package git

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultBranch_QueriesGhAndCaches verifies the first call shells out
// to `gh repo view --json defaultBranchRef --jq .defaultBranchRef.name`,
// and subsequent calls reuse the cached value without re-invoking gh.
func TestDefaultBranch_QueriesGhAndCaches(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("master\n")}
	s := &Server{owner: "o", name: "n", gh: stub}

	got, err := s.defaultBranch(context.Background())
	require.NoError(t, err)
	require.Equal(t, "master", got)

	// Second call must hit the cache, not gh.
	got2, err := s.defaultBranch(context.Background())
	require.NoError(t, err)
	require.Equal(t, "master", got2)
	require.Len(t, stub.calls, 1, "expected exactly one gh invocation; got %d", len(stub.calls))

	// Verify the shape of the gh call.
	args := stub.calls[0]
	require.Equal(t, "repo", args[0])
	require.Equal(t, "view", args[1])
	require.Contains(t, args, "o/n")
	require.Contains(t, args, "--json")
	require.Contains(t, args, "defaultBranchRef")
	require.Contains(t, args, "--jq")
}

// TestDefaultBranch_TrimsWhitespace verifies the helper handles trailing
// newlines (the normal gh output shape) and stray spaces.
func TestDefaultBranch_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("  develop  \n")}
	s := &Server{owner: "o", name: "n", gh: stub}
	got, err := s.defaultBranch(context.Background())
	require.NoError(t, err)
	require.Equal(t, "develop", got)
}

// TestDefaultBranch_FallsBackToMainOnGhFailure verifies that a gh error
// (auth, network, no-such-repo) doesn't propagate — we fall back to "main"
// and cache the fallback so we don't retry on every tool call.
func TestDefaultBranch_FallsBackToMainOnGhFailure(t *testing.T) {
	t.Parallel()
	stub := &stubGh{stderr: []byte("auth required"), err: fmt.Errorf("exit 4")}
	s := &Server{owner: "o", name: "n", gh: stub}
	got, err := s.defaultBranch(context.Background())
	require.NoError(t, err)
	require.Equal(t, "main", got)
	// Cache the fallback so we don't re-shell on every subsequent tool call.
	_, _ = s.defaultBranch(context.Background())
	require.Len(t, stub.calls, 1, "expected fallback to be cached")
}

// TestDefaultBranch_FallsBackOnEmptyOutput covers the edge where gh
// "succeeds" but returns empty stdout (e.g. a repo with no default
// branch ref configured — pathological but possible). We still want a
// usable fallback rather than empty-string disaster.
func TestDefaultBranch_FallsBackOnEmptyOutput(t *testing.T) {
	t.Parallel()
	stub := &stubGh{response: []byte("\n")}
	s := &Server{owner: "o", name: "n", gh: stub}
	got, err := s.defaultBranch(context.Background())
	require.NoError(t, err)
	require.Equal(t, "main", got)
}

// concurrentGh is a thin stub that counts invocations atomically so the
// race test can assert "exactly one call survived the concurrent first-
// callers". We need this rather than stubGh because stubGh.calls slice
// append isn't safe under -race.
type concurrentGh struct {
	count    int32
	response []byte
}

func (c *concurrentGh) Run(_ context.Context, _ ...string) ([]byte, []byte, error) {
	atomic.AddInt32(&c.count, 1)
	return c.response, nil, nil
}

// TestDefaultBranch_RaceSafe verifies that concurrent first-call
// invocations do not each re-shell out to gh (the cache must serialize
// the first-resolve). Runs with -race in CI.
func TestDefaultBranch_RaceSafe(t *testing.T) {
	t.Parallel()
	gh := &concurrentGh{response: []byte("main\n")}
	s := &Server{owner: "o", name: "n", gh: gh}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.defaultBranch(context.Background())
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	// Allow up to a small number of races on the first resolve (sync.Once
	// serializes, but if we used a plain mutex+flag pattern the count is
	// exactly 1). With sync.Once it's strictly 1.
	require.Equal(t, int32(1), atomic.LoadInt32(&gh.count),
		"sync.Once must collapse concurrent first-calls into one gh invocation")
}
