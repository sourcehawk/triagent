//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// Browser is the Playwright runner handle, attached to a Harness when
// Options.Browser is set. It owns the env handshake to the e2e/browser
// Playwright project: Run execs `npx playwright test <spec>` against the
// launcher's loopback URL, passing the base URL + launch token (and any
// per-run env a spec needs) so the browser authenticates and points at
// the right instance. The Go harness owns the launcher lifecycle; the
// Playwright config has no webServer block.
type Browser struct {
	baseURL  string
	token    string
	extraEnv map[string]string
}

// SetEnv records a per-run environment variable forwarded to Playwright.
// Use it to pass a spec the context it needs — e.g. the investigation id
// the harness created — via TRIAGENT_* env the helpers read. Call before
// Run.
func (b *Browser) SetEnv(key, value string) {
	if b.extraEnv == nil {
		b.extraEnv = map[string]string{}
	}
	b.extraEnv[key] = value
}

// browserOnce guards the one-time Playwright browser install per `go test`
// invocation; the chromium download is cached under ~/.cache/ms-playwright
// across runs, so the install is near-free after the first.
var (
	browserOnce    sync.Once
	browserInstErr error
)

// browserDir resolves the e2e/browser directory from this source file's
// location, so Run works regardless of the test's working dir.
func browserDir() string {
	_, file, _, _ := runtime.Caller(0)
	// file is e2e/harness/browser.go → up two to e2e/, then browser/.
	return filepath.Join(filepath.Dir(filepath.Dir(file)), "browser")
}

// ensureBrowsersInstalled runs `npx playwright install chromium` once. The
// Makefile's test-e2e target runs `npm install` in e2e/browser first, so
// the playwright CLI is on hand; this step fetches the browser binary the
// CI cache persists.
func ensureBrowsersInstalled(dir string) error {
	browserOnce.Do(func() {
		cmd := exec.Command("npx", "playwright", "install", "chromium")
		cmd.Dir = dir
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			browserInstErr = &runError{op: "playwright install chromium", output: out, err: err}
		}
	})
	return browserInstErr
}

// Run executes one Playwright spec against the launcher. spec is a path
// relative to e2e/browser (e.g. "investigation.spec.ts"). It streams the
// runner output through t.Log and fails the test if Playwright exits
// non-zero. The base URL + token (+ any SetEnv values) reach the spec via
// TRIAGENT_* env the helpers read.
func (b *Browser) Run(t *testing.T, spec string) {
	t.Helper()
	dir := browserDir()
	if err := ensureBrowsersInstalled(dir); err != nil {
		t.Fatalf("harness: %v", err)
	}

	cmd := exec.Command("npx", "playwright", "test", spec)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TRIAGENT_BASE_URL="+b.baseURL,
		"TRIAGENT_TOKEN="+b.token,
	)
	for k, v := range b.extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		t.Logf("playwright %s:\n%s", spec, out)
	}
	if err != nil {
		t.Fatalf("harness: playwright test %s failed: %v", spec, err)
	}
}

// runError carries a subprocess's combined output alongside the exec
// error so a failed install surfaces the npm/playwright diagnostics.
type runError struct {
	op     string
	output []byte
	err    error
}

func (e *runError) Error() string {
	return e.op + ": " + e.err.Error() + "\n" + string(e.output)
}
