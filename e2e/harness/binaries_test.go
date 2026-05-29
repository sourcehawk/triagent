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
//
// The shared envtest cluster is NOT booted here — it's booted lazily on the
// first K8s launch (see envtest.go) so non-k8s runs pay nothing. TestMain only
// owns the teardown: stopSharedEnv after m.Run() is a no-op unless envtest was
// booted, and stopping it from a deferred test cleanup would race the shared
// once.
func TestMain(m *testing.M) {
	if err := buildBinaries(); err != nil {
		_, _ = os.Stderr.WriteString("harness: build binaries: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	stopSharedEnv()
	os.Exit(code)
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
