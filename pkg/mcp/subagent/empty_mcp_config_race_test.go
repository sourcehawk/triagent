package subagent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteEmptyMCPConfig_RaceClean fires N concurrent first-callers at
// writeEmptyMCPConfig. The function caches the resulting path in a
// package-global; without synchronization the read+write on that global
// is a data race. Verifies all callers see a non-empty path and the
// package-global ends up consistent. Failure surfaces under
// `go test -race`.
func TestWriteEmptyMCPConfig_RaceClean(t *testing.T) {
	// Reset the package state so this test exercises the first-init
	// code path. Must hold the lock to write the global safely.
	emptyCfgMu.Lock()
	emptyCfgPath = ""
	emptyCfgMu.Unlock()

	var wg sync.WaitGroup
	results := make([]string, 32)
	errs := make([]error, 32)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := writeEmptyMCPConfig()
			results[i] = p
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d", i)
		require.NotEmpty(t, results[i], "goroutine %d returned empty path", i)
	}
	// All goroutines should see the same cached path after init settles.
	first := results[0]
	for i, p := range results {
		require.Equal(t, first, p, "goroutine %d got %q, expected %q (cached)", i, p, first)
	}
}
