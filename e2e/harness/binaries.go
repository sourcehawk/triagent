//go:build e2e

package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// binDir holds the directory the built binaries live in, set once by
// buildBinaries. The stubs are installed under the real CLI names (claude,
// gh) so that prefixing binDir onto PATH makes exec.LookPath resolve them.
var (
	binDir    string
	buildOnce sync.Once
	buildErr  error
)

// BinDir returns the directory holding the built binaries. Empty until
// buildBinaries has run (via the harness TestMain).
func BinDir() string { return binDir }

// buildBinaries compiles triagent, triagent-mcp, claude-stub (as "claude"),
// and gh-stub (as "gh") once into a temp bin dir. Idempotent: repeated calls
// return the first result. A failure to build any one binary is fatal —
// every downstream test would fail on a missing binary, so surface it here
// with the compiler output attached.
func buildBinaries() error {
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "triagent-e2e-bin-")
		if err != nil {
			buildErr = fmt.Errorf("create bin dir: %w", err)
			return
		}
		root, err := repoRoot()
		if err != nil {
			buildErr = err
			return
		}
		targets := []struct {
			out string // installed binary name
			pkg string // package path relative to repo root
		}{
			{"triagent", "./cmd/triagent"},
			{"triagent-mcp", "./cmd/triagent-mcp"},
			{"claude", "./e2e/cmd/claude-stub"},
			{"gh", "./e2e/cmd/gh-stub"},
		}
		for _, tgt := range targets {
			outPath := filepath.Join(dir, tgt.out)
			// -tags=e2e so the claude/gh stub sources (now behind //go:build
			// e2e, to keep them out of the fast `go test ./...` loop) compile.
			// Harmless for triagent/triagent-mcp, which carry no e2e-tagged files.
			cmd := exec.Command("go", "build", "-tags=e2e", "-o", outPath, tgt.pkg)
			cmd.Dir = root
			cmd.Env = os.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s (%s): %w\n%s", tgt.out, tgt.pkg, err, out)
				return
			}
		}
		binDir = dir
	})
	return buildErr
}

// repoRoot walks up from this source file to the directory holding go.mod.
// Anchoring on the source file (via runtime.Caller) keeps the build working
// regardless of the test's working directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve caller for repo root")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from %s", filepath.Dir(file))
		}
		dir = parent
	}
}
