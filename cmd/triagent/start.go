package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/server"
	"github.com/sourcehawk/triagent/system"
	"github.com/spf13/cobra"
)

// ResolveProfileRef returns the profile reference to load, applying the
// precedence: flag value > env var > "default" built-in profile.
func ResolveProfileRef(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("TRIAGENT_PROFILE"); env != "" {
		return env
	}
	return "default"
}

type startFlags struct {
	profile       string
	port          int
	launchBrowser bool
}

func start() *cobra.Command {
	f := &startFlags{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Open the browser-based investigation UI",
		Long: `Boots a local HTTP server, opens the default browser to it, and
walks you through picking a Kubernetes cluster, running preflight, and
holding a Claude-driven investigation backed by the triagent-mcp servers.

The launcher prints a URL with a per-launch token; the page sets a cookie
and the token drops out of the address bar. Press Ctrl-C in the launcher
terminal to tear down the server and any in-flight Claude CLIs.

Everything else — paths, repos, auto-mode default, air-gapped behavior —
is read from the profile. Fork the embedded "default" profile (or copy
it onto disk) to change those.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error { return runStart(c, f) },
	}
	cmd.Flags().StringVar(&f.profile, "profile", "",
		"Profile name (embedded: default) or filesystem path. "+
			"Overridable via TRIAGENT_PROFILE. Defaults to default.")
	cmd.Flags().IntVar(&f.port, "port", 0,
		"TCP port to bind on 127.0.0.1. Defaults to 0 (a random free port).")
	cmd.Flags().BoolVar(&f.launchBrowser, "launch-browser", true,
		"Open the default browser to the launch URL on startup. "+
			"Pass --launch-browser=false to only print the URL (headless / SSH).")
	return cmd
}

func runStart(cmd *cobra.Command, f *startFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	ref := ResolveProfileRef(f.profile)
	prof, err := profile.Load(ref)
	if err != nil {
		return fmt.Errorf("load profile %q: %w", ref, err)
	}
	if err := prof.Validate(); err != nil {
		return err
	}
	paths, err := prof.Paths.Resolve(prof.Name)
	if err != nil {
		return fmt.Errorf("resolve profile paths: %w", err)
	}
	warnLegacyUnnamespacedDirs(prof.Name, paths)

	p, err := newAuthProvider(prof.Auth)
	if err != nil {
		return fmt.Errorf("auth provider: %w", err)
	}
	SetProvider(p)

	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	mcpBin, err := locateMCPBinary(bin)
	if err != nil {
		return fmt.Errorf("locate triagent-mcp: %w (build it with `make build`)", err)
	}

	if paths.SessionsRoot == "" {
		return fmt.Errorf("profile %q has no paths.sessions_root", prof.Name)
	}
	if err := os.MkdirAll(paths.SessionsRoot, 0o700); err != nil {
		return fmt.Errorf("create sessions root: %w", err)
	}
	if paths.UserPlaybooksDir != "" {
		if err := os.MkdirAll(paths.UserPlaybooksDir, 0o700); err != nil {
			return fmt.Errorf("create user playbooks dir: %w", err)
		}
	}

	playbooksRepo := server.PlaybooksRepoFor(prof, "")
	if paths.UpstreamPlaybooksDir != "" {
		if err := preflight.EnsurePlaybooksClone(ctx, preflight.PlaybooksCloneOpts{
			Repo:    playbooksRepo,
			Dir:     paths.UpstreamPlaybooksDir,
			Offline: prof.Defaults.Offline,
		}); err != nil {
			return fmt.Errorf("upstream playbooks: %w", err)
		}
		// Materialise the configured subpath inside the clone so the
		// strategies loader and first-write paths can stat a non-empty
		// vault dir even on a fresh clone of a repo that hasn't had any
		// playbooks committed under <playbooks_path>/ yet.
		if sub := prof.Defaults.PlaybooksPath; sub != "" {
			if err := os.MkdirAll(filepath.Join(paths.UpstreamPlaybooksDir, sub), 0o700); err != nil {
				return fmt.Errorf("create playbooks subpath %q: %w", sub, err)
			}
		}
	}

	// Materialise the launcher-bundled system playbooks (the master
	// "investigation" entrypoint + workflow metas) onto disk so the
	// strategies MCP can load them via --system-playbooks-dir. Re-
	// extracted every startup; the embed is the source of truth.
	if paths.SystemPlaybooksDir != "" {
		if _, err := system.Extract(paths.SystemPlaybooksDir); err != nil {
			return fmt.Errorf("extract bundled system playbooks: %w", err)
		}
	}

	wikiRepo := server.WikiRepoFor(prof, "")
	if paths.WikiDir != "" {
		if err := preflight.EnsureWikiClone(ctx, preflight.WikiCloneOpts{
			Repo:    wikiRepo,
			Dir:     paths.WikiDir,
			Offline: prof.Defaults.Offline,
		}); err != nil {
			return fmt.Errorf("upstream wiki: %w", err)
		}
		if sub := prof.Defaults.WikiPath; sub != "" {
			if err := os.MkdirAll(filepath.Join(paths.WikiDir, sub), 0o700); err != nil {
				return fmt.Errorf("create wiki subpath %q: %w", sub, err)
			}
		}
	}

	sessionsRepo := server.SessionsRepoFor(prof, "")
	if paths.UpstreamSessionsDir != "" {
		if err := preflight.EnsureSessionsClone(ctx, preflight.SessionsCloneOpts{
			Repo:    sessionsRepo,
			Dir:     paths.UpstreamSessionsDir,
			Offline: prof.Defaults.Offline,
		}); err != nil {
			return fmt.Errorf("upstream sessions: %w", err)
		}
		if sub := prof.Defaults.SessionsPath; sub != "" {
			if err := os.MkdirAll(filepath.Join(paths.UpstreamSessionsDir, sub), 0o700); err != nil {
				return fmt.Errorf("create sessions subpath %q: %w", sub, err)
			}
		}
	}

	return runWeb(ctx, mcpBin, paths, playbooksRepo, wikiRepo, sessionsRepo, prof, f.port, f.launchBrowser)
}

// warnLegacyUnnamespacedDirs surfaces a one-line WARN per legacy
// un-namespaced state dir still on disk. Triggered by the migration
// to per-profile paths (`${XDG_CONFIG_HOME}/triagent/<profile>/...`):
// operators upgrading from an earlier triagent had every profile
// share one set of dirs at `${XDG_CONFIG_HOME}/triagent/...`. Those
// orphan dirs are no longer read by the launcher, so we point at
// them once so the operator can `mv` or `rm` deliberately. Profiles
// whose paths don't bake in the profile-name segment skip the
// check — they opted out of the namespacing.
func warnLegacyUnnamespacedDirs(profileName string, paths profile.Paths) {
	if profileName == "" {
		return
	}
	seg := string(filepath.Separator) + profileName + string(filepath.Separator)
	entries := []struct{ kind, current string }{
		{"upstream-playbooks", paths.UpstreamPlaybooksDir},
		{"wiki", paths.WikiDir},
		{"upstream-sessions", paths.UpstreamSessionsDir},
		{"sessions-root", paths.SessionsRoot},
		{"user-playbooks", paths.UserPlaybooksDir},
		{"git-cache", paths.GitCacheDir},
	}
	for _, e := range entries {
		if e.current == "" || !strings.Contains(e.current, seg) {
			continue
		}
		legacy := strings.Replace(e.current, seg, string(filepath.Separator), 1)
		if info, err := os.Stat(legacy); err == nil && info.IsDir() {
			log.Warn("legacy un-namespaced state detected; the launcher no longer reads it",
				"kind", e.kind, "legacy_path", legacy, "active_path", e.current)
		}
	}
}

// joinSubpath returns filepath.Join(root, sub), or root unchanged when
// either argument is empty. Used to derive the per-artifact work-dir
// from the clone root + the profile's *_path setting.
func joinSubpath(root, sub string) string {
	if root == "" || sub == "" {
		return root
	}
	return filepath.Join(root, sub)
}

// runWeb starts the local HTTP server, prints the launch URL, opens the
// browser, and blocks until SIGINT/SIGTERM. Tearing down here cancels the
// per-investigation contexts so claude CLIs and port-forwards drain
// cleanly.
func runWeb(ctx context.Context, mcpBin string, paths profile.Paths, playbooksRepo, wikiRepo, sessionsRepo string, prof *profile.Profile, port int, launchBrowser bool) error {
	// Derive the docs server name from the profile's extra_mcps list so the
	// editor session's prompt still advertises the docs tools bullet.
	docsServerName := ""
	for _, m := range prof.ExtraMCPs {
		docsServerName = m.Alias
		break
	}
	// A non-zero --port binds 127.0.0.1:<port>; the default (0) leaves Addr
	// empty so server.New picks a random free port.
	addr := ""
	if port != 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", port)
	}
	srv, err := server.New(server.Options{
		Addr:                     addr,
		Provider:                 provider,
		MCPBinaryPath:            mcpBin,
		SessionsRoot:             paths.SessionsRoot,
		DocsServerName:           docsServerName,
		UserReposPath:            paths.UserReposFile,
		GitCacheDir:              paths.GitCacheDir,
		UserPlaybooksDir:         paths.UserPlaybooksDir,
		PluginPlaybooksDir:       joinSubpath(paths.UpstreamPlaybooksDir, prof.Defaults.PlaybooksPath),
		PluginPlaybooksCloneRoot: paths.UpstreamPlaybooksDir,
		SystemPlaybooksDir:       paths.SystemPlaybooksDir,
		PlaybooksRepo:            playbooksRepo,
		WikiPath:                 joinSubpath(paths.WikiDir, prof.Defaults.WikiPath),
		WikiCloneRoot:            paths.WikiDir,
		WikiRepo:                 wikiRepo,
		WikiProposalsPath:        paths.WikiProposalsDir,
		SessionsPath:             joinSubpath(paths.UpstreamSessionsDir, prof.Defaults.SessionsPath),
		SessionsCloneRoot:        paths.UpstreamSessionsDir,
		SessionsRepo:             sessionsRepo,
		SessionsProposalsPath:    paths.SessionsProposalsDir,
		CodefixProposalsPath:     paths.CodefixProposalsDir,
		UserWatchesPath:          paths.UserWatchesFile,
		Profile:                  prof,
		Version:                  version,
	})
	if err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	url := srv.URL()
	log.Info("triagent", "url", url)
	if launchBrowser {
		openBrowser(url)
	}
	log.Info("press Ctrl-C to stop")
	return srv.Run(signalCtx)
}

// openBrowser tries to launch the OS default browser pointing at url. Best
// effort — failures are silent because the URL is also printed to stdout
// for the operator to copy if auto-open doesn't work.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}

// locateMCPBinary finds the triagent-mcp binary. Looks for it adjacent to
// the triagent binary (same directory), then falls back to $PATH.
func locateMCPBinary(selfBin string) (string, error) {
	selfDir := filepath.Dir(selfBin)
	candidate := filepath.Join(selfDir, "triagent-mcp")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, nil
	}
	if p, err := exec.LookPath("triagent-mcp"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("could not locate triagent-mcp; tried %s and $PATH", candidate)
}
