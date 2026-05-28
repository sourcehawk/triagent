//go:build e2e

package harness

import (
	"os"
	"testing"
)

// TestMain builds the four binaries the suite drives (triagent,
// triagent-mcp, claude-stub, gh-stub) once before any harness test runs.
// A build failure aborts the whole run loudly rather than letting every
// test fail on a missing binary.
func TestMain(m *testing.M) {
	if err := buildBinaries(); err != nil {
		_, _ = os.Stderr.WriteString("harness: build binaries: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// buildBinaries must produce all four executables and expose their dir.
func TestBuildBinaries_ProducesAllFour(t *testing.T) {
	dir := BinDir()
	if dir == "" {
		t.Fatal("BinDir() is empty; buildBinaries did not run")
	}
	for _, name := range []string{"triagent", "triagent-mcp", "claude", "gh"} {
		info, err := os.Stat(dir + "/" + name)
		if err != nil {
			t.Fatalf("binary %q missing in bin dir: %v", name, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("binary %q is not executable (mode %v)", name, info.Mode())
		}
	}
}
