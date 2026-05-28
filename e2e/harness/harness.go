//go:build e2e

// Package harness boots the real triagent launcher against scripted stubs
// for the e2e suite. Launch builds nothing itself (TestMain does, once);
// it seeds a temp XDG state dir from fixtures, execs `triagent start
// --port=<n>` with the stub binaries on PATH, waits for /healthz, and
// registers cleanup that SIGTERMs the launcher and dumps its logs on
// failure.
package harness

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Options configures a Launch. Profile names a fixture profile under
// fixtures/profiles/; the *Fixtures fields name optional scenarios under
// the matching fixtures/ subtree; StubScript / GhScript name the per-test
// script directories the stubs replay.
type Options struct {
	Profile          string // fixture profile name under fixtures/profiles/
	SessionFixtures  string // optional scenario under fixtures/sessions/
	PlaybookFixtures string // optional scenario under fixtures/playbooks/
	WikiFixtures     string // optional scenario under fixtures/wiki/
	RepoFixtures     string // optional scenario under fixtures/repos/
	K8s              bool   // attach an envtest namespace and write kubeconfig (wired by #17)
	K8sFixtures      string // optional scenario under fixtures/k8s/ to pre-apply (#17)
	StubScript       string // per-test script dir under fixtures/stub-scripts/
	GhScript         string // per-test responses dir under fixtures/gh-scripts/
	Browser          bool   // launch the Playwright runner for this test (wired by #16)
}

// Harness is the live launcher under test. BaseURL is the launcher's
// loopback URL; StateDir is the temp XDG_CONFIG_HOME root the launcher
// persisted into; Client carries HTTP helpers. Browser is nil unless
// Options.Browser (and is wired by #16).
type Harness struct {
	BaseURL  string
	StateDir string
	Client   *Client
	Browser  *Browser

	proc *launcherProc
}

// readyTimeout bounds how long Launch waits for /healthz after exec.
const readyTimeout = 15 * time.Second

// Launch boots the launcher and returns once /healthz answers. It fails
// the test (via t.Fatalf) on any setup error, and registers a t.Cleanup
// that tears the launcher down.
func Launch(t *testing.T, opts Options) *Harness {
	t.Helper()
	if err := buildBinaries(); err != nil {
		t.Fatalf("harness: build binaries: %v", err)
	}

	stateDir := t.TempDir()
	cacheDir := t.TempDir()

	profilePath, err := seedFixtures(stateDir, opts)
	if err != nil {
		t.Fatalf("harness: seed fixtures: %v", err)
	}

	port, err := freePort()
	if err != nil {
		t.Fatalf("harness: allocate port: %v", err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	traceDir := filepath.Join(stateDir, "traces")
	signalDir := filepath.Join(stateDir, "signals")
	for _, d := range []string{traceDir, signalDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("harness: mkdir %s: %v", d, err)
		}
	}

	// When the test opts into k8s, boot/attach envtest, apply the fixture
	// manifests, and write a static kubeconfig. The launcher discovers it via
	// KUBECONFIG below; preflight then freezes a per-session copy and every
	// spawned MCP child carries that explicit path (project rule).
	var kubeconfigPath string
	if opts.K8s {
		setup, err := setupK8s(t, stateDir, opts)
		if err != nil {
			t.Fatalf("harness: k8s setup: %v", err)
		}
		kubeconfigPath = setup.kubeconfigPath
	}

	env := launcherEnv(envConfig{
		stateDir:       stateDir,
		cacheDir:       cacheDir,
		binDir:         BinDir(),
		traceDir:       traceDir,
		signalDir:      signalDir,
		kubeconfigPath: kubeconfigPath,
		opts:           opts,
	})

	proc, err := startLauncher(startConfig{
		binDir:      BinDir(),
		profilePath: profilePath,
		port:        port,
		env:         env,
	})
	if err != nil {
		t.Fatalf("harness: start launcher: %v", err)
	}

	h := &Harness{
		BaseURL:  baseURL,
		StateDir: stateDir,
		Client:   newClient(baseURL),
		proc:     proc,
	}

	t.Cleanup(func() {
		if t.Failed() {
			proc.dumpLogs(t)
		}
		h.Close()
	})

	if err := waitForHealthz(baseURL, readyTimeout); err != nil {
		proc.dumpLogs(t)
		t.Fatalf("harness: launcher not ready: %v", err)
	}
	// The launcher logs its token alongside the URL once it's up; capture
	// it so authenticated /api/* calls work for wave-2 consumers.
	token := proc.token()
	h.Client.setToken(token)
	if opts.Browser {
		h.Browser = &Browser{baseURL: baseURL, token: token}
	}
	return h
}

// Close tears the launcher down (SIGTERM, then SIGKILL on timeout). Safe to
// call more than once.
func (h *Harness) Close() {
	if h.proc != nil {
		h.proc.stop()
	}
}

// ReleaseSignal writes <state_dir>/signals/<name>, unblocking a stub
// wait_for_signal action of the same name. Used by tests that observe an
// in-flight intermediate state before letting the stub finish.
func (h *Harness) ReleaseSignal(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(h.StateDir, "signals", name)
	if err := os.WriteFile(path, []byte("released\n"), 0o600); err != nil {
		t.Fatalf("harness: release signal %q: %v", name, err)
	}
}

// freePort asks the OS for an unused TCP port on the loopback interface
// and returns it. There's an inherent (small) race between releasing the
// listener here and the launcher re-binding it; on loopback in CI this is
// reliable enough, and a bind failure surfaces as a not-ready error.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
