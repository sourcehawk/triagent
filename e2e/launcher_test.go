//go:build e2e

// Flow 1 — launcher boot options. One subtest per boot knob the launcher
// documents, each asserting the knob's observable effect against the real
// binary (no implementation internals). A final completeness scan parses
// `triagent start --help` and fails if any documented flag lacks a subtest,
// so help text drifting ahead of behaviour coverage breaks the build.
package e2e

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// bootKnob names a documented boot knob and the subtest that exercises it.
// flag is the long flag as it appears in `triagent start --help` (empty for
// env-only knobs, which the help scan doesn't enforce). subtest runs the
// knob's observable assertion.
type bootKnob struct {
	name    string
	flag    string // "--port", "--profile"; "" for env-var-only knobs
	subtest func(t *testing.T)
}

func bootKnobs() []bootKnob {
	return []bootKnob{
		{
			name: "port_binds_requested_port",
			flag: "--port",
			subtest: func(t *testing.T) {
				// The harness allocates a free port and passes it as --port;
				// it surfaces only as h.BaseURL. The observable that --port
				// took effect is that /healthz answers on exactly that port,
				// and nothing answers on a different one.
				h := harness.Launch(t, harness.Options{Profile: "minimal"})

				host := strings.TrimPrefix(h.BaseURL, "http://")
				_, port, err := net.SplitHostPort(host)
				if err != nil {
					t.Fatalf("parse BaseURL host:port from %q: %v", h.BaseURL, err)
				}
				if !strings.HasPrefix(host, "127.0.0.1:") {
					t.Errorf("launcher bound %q, want loopback 127.0.0.1", host)
				}

				// Probe the bound port directly (not via the client) so the
				// assertion is "the requested port is the one serving", not
				// "the client's stored URL works".
				status, err := probeHealthz("127.0.0.1:" + port)
				if err != nil {
					t.Fatalf("GET /healthz on requested port %s: %v", port, err)
				}
				if status != http.StatusOK {
					t.Errorf("/healthz on requested port %s = %d, want 200", port, status)
				}
			},
		},
		{
			name: "profile_resolution_reported_in_healthz",
			flag: "--profile",
			subtest: func(t *testing.T) {
				// /healthz reports the resolved profile name. Two distinct
				// fixture profiles must each report their own name, proving
				// the field tracks the --profile value rather than a constant.
				//
				// Flag-over-env precedence (TRIAGENT_PROFILE) is not
				// exercisable through the current harness: startLauncher
				// always passes --profile, which wins over the env var by
				// design. The precedence itself is pinned by the
				// ResolveProfileRef unit test in cmd/triagent; here we assert
				// the flag's resolved name reaches /healthz.
				for _, prof := range []string{"minimal", "minimal-alt"} {
					prof := prof
					t.Run(prof, func(t *testing.T) {
						h := harness.Launch(t, harness.Options{Profile: prof})
						got, version := h.Client.Healthz(t)
						if got != prof {
							t.Errorf("/healthz profile = %q, want %q", got, prof)
						}
						if version == "" {
							t.Error("/healthz version is empty")
						}
					})
				}
			},
		},
		{
			name: "xdg_config_home_redirects_state",
			subtest: func(t *testing.T) {
				// The harness points XDG_CONFIG_HOME at h.StateDir. The
				// launcher persists per-profile state under
				// ${XDG_CONFIG_HOME}/triagent/<profile>/; it MkdirAll's the
				// sessions root at boot, so its presence under StateDir is the
				// observable that the redirect took effect.
				h := harness.Launch(t, harness.Options{Profile: "minimal"})
				h.Client.Healthz(t) // gate on readiness before stat'ing disk

				sessions := filepath.Join(h.StateDir, "triagent", "minimal", "sessions")
				if info, err := os.Stat(sessions); err != nil {
					t.Errorf("sessions root not under XDG_CONFIG_HOME: stat %s: %v", sessions, err)
				} else if !info.IsDir() {
					t.Errorf("%s exists but is not a directory", sessions)
				}
			},
		},
		{
			name: "xdg_cache_home_redirects_cache",
			subtest: func(t *testing.T) {
				// The harness redirects XDG_CACHE_HOME to a temp dir disjoint
				// from XDG_CONFIG_HOME. The launcher resolves its git-cache
				// path under ${XDG_CACHE_HOME} but doesn't create it at boot,
				// and the harness doesn't expose the cache dir on Harness, so
				// the cache path itself isn't directly observable here. The
				// observable we can pin is the converse invariant: a clean
				// boot with the cache redirected proves the launcher doesn't
				// depend on a writable real ~/.cache, and no cache-shaped
				// subtree leaks into the config dir.
				h := harness.Launch(t, harness.Options{Profile: "minimal"})
				h.Client.Healthz(t)

				leaked := filepath.Join(h.StateDir, "triagent-mcp")
				if _, err := os.Stat(leaked); err == nil {
					t.Errorf("cache subtree %s leaked into XDG_CONFIG_HOME; XDG_CACHE_HOME redirect not honoured", leaked)
				}
			},
		},
		{
			name: "launch_token_gates_api",
			subtest: func(t *testing.T) {
				// The per-launch token (the same secret the launcher hands to
				// MCP children as TRIAGENT_MCP_TELEMETRY_TOKEN) gates the
				// cookie-authenticated /api/* surface: present → 200, absent →
				// 401. /healthz stays open regardless. This is the launch-token
				// plumbing observable the harness's authenticated calls depend
				// on.
				h := harness.Launch(t, harness.Options{Profile: "minimal"})

				status, _ := h.Client.Get(t, "/api/investigations")
				if status != http.StatusOK {
					t.Errorf("authenticated GET /api/investigations = %d, want 200", status)
				}

				unauth, err := probeStatus(h.BaseURL + "/api/investigations")
				if err != nil {
					t.Fatalf("unauthenticated GET /api/investigations: %v", err)
				}
				if unauth != http.StatusUnauthorized {
					t.Errorf("unauthenticated GET /api/investigations = %d, want 401", unauth)
				}
			},
		},
	}
}

// TestFlow1_LauncherBootOptions runs every boot-knob subtest.
func TestFlow1_LauncherBootOptions(t *testing.T) {
	for _, k := range bootKnobs() {
		t.Run(k.name, k.subtest)
	}
}

// TestFlow1_HelpFlagsAllCovered parses `triagent start --help` and fails if a
// documented flag has no corresponding subtest. Catches help text that drifts
// ahead of behaviour coverage. -h/--help is a built-in cobra flag, not a
// behavioural knob, so it's exempt.
func TestFlow1_HelpFlagsAllCovered(t *testing.T) {
	flags := documentedStartFlags(t)
	if len(flags) == 0 {
		t.Fatal("parsed zero flags from `triagent start --help`; parser or help output changed")
	}

	covered := map[string]bool{}
	for _, k := range bootKnobs() {
		if k.flag != "" {
			covered[k.flag] = true
		}
	}

	exempt := map[string]bool{"--help": true}
	var untested []string
	for _, f := range flags {
		if exempt[f] || covered[f] {
			continue
		}
		untested = append(untested, f)
	}
	if len(untested) > 0 {
		t.Errorf("documented `triagent start` flags with no boot-knob subtest: %v", untested)
	}
}

// flagLine matches a long flag at the start of a `--help` flag line, e.g.
// "      --port int   ..." or "  -h, --help   ...".
var flagLine = regexp.MustCompile(`(?m)^\s+(?:-\w,\s+)?(--[a-z][a-z0-9-]*)`)

// documentedStartFlags builds the triagent launcher into a temp dir and execs
// `triagent start --help`, returning the long flags it documents. It builds
// its own binary (rather than reaching into the harness's private build) so
// the completeness scan runs even when no Launch has populated the harness bin
// dir yet.
func documentedStartFlags(t *testing.T) []string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "triagent")
	build := exec.Command("go", "build", "-o", bin, "./cmd/triagent")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build triagent: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "start", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("triagent start --help: %v\n%s", err, out)
	}
	var flags []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		m := flagLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		if !seen[m[1]] {
			seen[m[1]] = true
			flags = append(flags, m[1])
		}
	}
	return flags
}

// repoRoot walks up from this test file to the directory holding go.mod, so
// the in-test `go build` resolves the module regardless of the working dir.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller for repo root")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// probeHealthz issues an unauthenticated GET /healthz against host:port and
// returns the status. Used to confirm --port bound where requested.
func probeHealthz(hostPort string) (int, error) {
	return probeStatus("http://" + hostPort + "/healthz")
}

// probeStatus issues a bare GET (no launch token) and returns the status.
func probeStatus(url string) (int, error) {
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Get(url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}
