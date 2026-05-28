//go:build e2e

package harness

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// launcherProc supervises one `triagent start` child: captured stdout +
// stderr, a SIGTERM-then-SIGKILL stop, and a wait goroutine so stop never
// blocks forever on a wedged child.
type launcherProc struct {
	cmd    *exec.Cmd
	stdout *syncBuffer
	stderr *syncBuffer

	stopOnce sync.Once
	waitErr  chan error
}

// envConfig is the input to launcherEnv.
type envConfig struct {
	stateDir  string
	cacheDir  string
	binDir    string
	traceDir  string
	signalDir string
	// kubeconfigPath, when set (K8s launches), is exported as KUBECONFIG so
	// the launcher's preflight freezes a copy and wires it into every spawned
	// MCP child. Empty leaves the kubeconfig provider on its ~/.kube fallback.
	kubeconfigPath string
	opts           Options
}

// launcherEnv builds the child's environment. XDG roots redirect all
// launcher + cache state into the temp dirs; PATH is prefixed with the bin
// dir so the launcher resolves the stub claude/gh; the stub-script /
// trace-dir / signal-dir vars wire the stubs to their per-test scripts and
// the shared signal dir.
func launcherEnv(c envConfig) []string {
	env := []string{
		"XDG_CONFIG_HOME=" + c.stateDir,
		"XDG_CACHE_HOME=" + c.cacheDir,
		"PATH=" + c.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"CLAUDE_STUB_TRACE_DIR=" + c.traceDir,
		"GH_STUB_TRACE_DIR=" + c.traceDir,
		"CLAUDE_STUB_SIGNAL_DIR=" + c.signalDir,
	}
	if c.opts.StubScript != "" {
		env = append(env, "CLAUDE_STUB_SCRIPT="+fixtureDir("stub-scripts", c.opts.StubScript))
	}
	if c.opts.GhScript != "" {
		env = append(env, "GH_STUB_SCRIPT="+fixtureDir("gh-scripts", c.opts.GhScript))
	}
	// HOME is preserved so the kubeconfig provider's ~/.kube fallback and
	// any go-toolchain temp resolution keep working; XDG_* above already
	// redirects the launcher's own state.
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	// K8s launches pin KUBECONFIG to the harness-written static kubeconfig so
	// the launcher freezes that exact file and the k8s MCP child binds to the
	// envtest apiserver — no ambient ~/.kube state leaks in.
	if c.kubeconfigPath != "" {
		env = append(env, "KUBECONFIG="+c.kubeconfigPath)
	}
	return env
}

// startConfig is the input to startLauncher.
type startConfig struct {
	binDir      string
	profilePath string
	port        int
	env         []string
}

// startLauncher execs `triagent start --profile <path> --port <n>` with the
// composed env and captured output, then returns a handle. It does not wait
// for readiness — Launch polls /healthz separately.
func startLauncher(c startConfig) (*launcherProc, error) {
	bin := filepath.Join(c.binDir, "triagent")
	cmd := exec.Command(bin, "start",
		"--profile", c.profilePath,
		"--port", fmt.Sprintf("%d", c.port),
	)
	cmd.Env = c.env
	stdout := &syncBuffer{}
	stderr := &syncBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// New process group so SIGKILL on stop reaps any MCP grandchildren the
	// launcher spawned, not just the launcher itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec %s: %w", bin, err)
	}
	p := &launcherProc{cmd: cmd, stdout: stdout, stderr: stderr, waitErr: make(chan error, 1)}
	go func() { p.waitErr <- cmd.Wait() }()
	return p, nil
}

// stop sends SIGTERM, waits up to 5s for a clean exit, then SIGKILLs the
// whole process group. Idempotent.
func (p *launcherProc) stop() {
	p.stopOnce.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		pgid := -p.cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGTERM)
		select {
		case <-p.waitErr:
			return
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(pgid, syscall.SIGKILL)
			<-p.waitErr
		}
	})
}

// token scans the captured launcher output for the per-launch token the
// launcher prints alongside its URL (url=http://127.0.0.1:PORT/?token=HEX).
// Returns "" until the line has been logged; Launch polls /healthz first so
// by the time it calls this the line is present.
func (p *launcherProc) token() string {
	combined := p.stdout.String() + "\n" + p.stderr.String()
	const marker = "token="
	idx := strings.Index(combined, marker)
	if idx < 0 {
		return ""
	}
	rest := combined[idx+len(marker):]
	end := 0
	for end < len(rest) {
		ch := rest[end]
		isHex := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
		if !isHex {
			break
		}
		end++
	}
	return rest[:end]
}

// dumpLogs writes the captured launcher output through t.Log so a failed
// test shows why the launcher misbehaved.
func (p *launcherProc) dumpLogs(t *testing.T) {
	t.Helper()
	if out := strings.TrimSpace(p.stdout.String()); out != "" {
		t.Logf("launcher stdout:\n%s", out)
	}
	if errOut := strings.TrimSpace(p.stderr.String()); errOut != "" {
		t.Logf("launcher stderr:\n%s", errOut)
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer: the child's stdout/stderr
// writer goroutines and the test's reader (dumpLogs) touch it concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
