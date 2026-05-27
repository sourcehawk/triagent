// Package server hosts the local HTTP server that backs the browser UI for
// `triagent start`. It binds to 127.0.0.1 only, gates non-trivial
// endpoints on a per-launch random token, and serves the embedded Next.js
// static bundle for everything else.
//
// Phase 2 ships the skeleton: token auth, /api/health, and the static
// fileserver. Phase 3+ adds session/cluster/preflight endpoints.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math/bits"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/editor"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/promforward"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/internal/watches"
	"github.com/sourcehawk/triagent/internal/watches/sources"
	"github.com/sourcehawk/triagent/internal/web"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// tokenCookie carries the launch token after the first authenticated
	// request, so the SPA can fetch /api/* without dragging the token
	// through every URL.
	tokenCookie = "triagent_token"
	tokenQuery  = "token"
)

// Options configures a Server.
type Options struct {
	// Addr is the bind address. Empty means 127.0.0.1 with a random port.
	// Production callers should always leave this as the default — binding
	// to 0.0.0.0 would expose investigations over the LAN.
	Addr string

	// Provider is the cluster provider (Teleport in prod). Required for
	// /api/clusters and /api/login. The web flow does not function without
	// a provider — pass the same one cmd/start.go uses for the TUI.
	Provider auth.Provider

	// MCPBinaryPath is the absolute path to the triagent-mcp binary the per-session
	// MCP config refers to. Required.
	MCPBinaryPath string

	// SessionsRoot is the directory under which per-investigation session
	// dirs are created. Required.
	SessionsRoot string

	// DocsServerName is the optional docs MCP server name to detect via
	// `claude mcp list`. Empty disables docs detection.
	DocsServerName string

	// UserReposPath points at the user_repos.yaml file backing the
	// linked-repos feature. Empty paths skip the linked-repos surface
	// entirely (no MCP servers, no /api/repos endpoint).
	UserReposPath string

	// UserWatchesPath points at the user_watches.yaml file backing the
	// signal-watches feature. Empty disables the watches subsystem.
	UserWatchesPath string

	// GitCacheDir is forwarded to each triagent-mcp git server via env. Empty
	// lets each server resolve its own default ($XDG_CACHE_HOME/triagent-mcp/git).
	GitCacheDir string

	// UserPlaybooksDir holds operator-customised investigation playbooks
	// the editor writes to, and the strategies MCP layers on top of the
	// system set. Empty disables the editor's persistence (read-only).
	UserPlaybooksDir string

	// PluginPlaybooksDir is the work-dir under the launcher-managed
	// clone of the upstream playbooks repo where the `<type>/<id>.yaml`
	// tree lives. The strategies MCP loads YAML files from this
	// directory as the "plugin" set (overridable by user dir); the
	// editor's "push as PR" flow writes here.
	//
	// When defaults.playbooks_path is non-empty this points to
	// `<PluginPlaybooksCloneRoot>/<playbooks_path>` — git operations
	// against the clone go through PluginPlaybooksCloneRoot, file
	// reads/writes go through PluginPlaybooksDir. When playbooks_path
	// is empty both point at the same dir (legacy flat layout).
	PluginPlaybooksDir string

	// PluginPlaybooksCloneRoot is the clone root (the path `git clone`
	// wrote into) for the upstream playbooks repo. Used as the cwd for
	// git commands so branch switches survive even when the configured
	// subpath doesn't exist on the target ref yet. Equal to
	// PluginPlaybooksDir on flat-layout profiles.
	PluginPlaybooksCloneRoot string

	// SystemPlaybooksDir is the path the launcher extracted its
	// embedded `system/` fileset to at startup (typically under
	// ~/.config/triagent/system-playbooks/). The
	// strategies MCP loads YAML from here as the "system" set —
	// locked, not overridable by user-dir files. Empty for tests
	// or builds without the embed.
	SystemPlaybooksDir string

	// PlaybooksRepo is the GitHub `OWNER/REPO` for the upstream
	// playbooks repo (defaults to sourcehawk/triagent-playbooks).
	// Drives both the clone source URL and the PR upstream.
	PlaybooksRepo string

	// WikiPath is the work-dir under the launcher-managed clone of the
	// upstream wiki repo where the `entries/` + `entities/` trees live.
	// Approved wiki entries are committed here and pushed via gh as
	// PRs. Empty disables the wiki feature.
	//
	// When defaults.wiki_path is non-empty this points to
	// `<WikiCloneRoot>/<wiki_path>` — see PluginPlaybooksDir for the
	// work-dir/clone-root rationale.
	WikiPath string

	// WikiCloneRoot is the clone root for the wiki repo (the dir `git
	// clone` wrote into). Used as the cwd for git commands.
	WikiCloneRoot string

	// WikiRepo is the GitHub OWNER/REPO for the upstream wiki repo.
	// Used as the PR upstream when promoting wiki entries; mirrors
	// PlaybooksRepo.
	WikiRepo string

	// WikiProposalsPath is the local directory holding in-flight wiki
	// drafts. Defaults to ~/.triagent/wiki-proposals when empty. The MCP wiki
	// server and the launcher backend share this path.
	WikiProposalsPath string

	// CodefixProposalsPath is the local directory holding codefix
	// proposal payloads + discard-ledger entries. Defaults handled by
	// resolveCodefixProposalsPath.
	CodefixProposalsPath string

	// SessionsPath is the work-dir under the local clone of the upstream
	// sessions repo where `<slug>/session.md` dirs land. Empty disables
	// the sessions feature.
	//
	// When defaults.sessions_path is non-empty this points to
	// `<SessionsCloneRoot>/<sessions_path>`. See PluginPlaybooksDir for
	// the rationale on splitting work-dir from clone-root.
	SessionsPath string
	// SessionsCloneRoot is the clone root for the upstream sessions
	// repo (the dir `git clone` wrote into). Used as the cwd for git
	// commands. Distinct from SessionsRoot (which is the parent dir for
	// per-investigation local session dirs, not a git checkout).
	SessionsCloneRoot string
	// SessionsRepo is the GitHub owner/name slug for `gh pr create`.
	// Defaults to the profile's sessions_repo when SessionsPath is set and
	// this is empty.
	SessionsRepo string
	// SessionsProposalsPath is where `triagent-mcp --kind sessions propose-draft`
	// writes session.md drafts. Sourced from the profile's
	// paths.sessions_proposals_dir.
	SessionsProposalsPath string

	// CredentialsDir holds the operator's third-party tokens (Slack,
	// incident.io). Defaults to ~/.config/triagent/ when empty.
	// The single credentials.json file is mode 0600.
	CredentialsDir string

	// Profile is the loaded investigation profile. Supplies prompt content
	// (system / architecture / strategies / editor / wiki_editor) and
	// playbook IDs (suggested-entrypoint-playbook / suggested-closing-playbook).
	Profile *profile.Profile
}

// Server owns the listener, the HTTP server, the launch token, and the
// per-launch investigation + editor managers. All sessions are torn
// down on graceful shutdown so port-forwards and claude CLIs don't
// leak.
type Server struct {
	httpServer    *http.Server
	listener      net.Listener
	token         string
	manager       *Manager
	editorMgr     *editor.Manager
	cancelManager context.CancelFunc
	watchesMgr    *watches.Manager
}

// New constructs a Server, opening the listener immediately so URL is
// available before Run starts. The caller closes the server via Run's
// context.
func New(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	if opts.MCPBinaryPath == "" {
		return nil, fmt.Errorf("MCPBinaryPath is required")
	}
	if opts.SessionsRoot == "" {
		return nil, fmt.Errorf("SessionsRoot is required")
	}

	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("generate launch token: %w", err)
	}

	frontFS, err := web.FS()
	if err != nil {
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}

	// Bind the listener first; everything below is plumbing we'd need to
	// unwind on a bind failure, so doing it in this order avoids leaking
	// the parent context when net.Listen fails.
	listener, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	// Per-investigation contexts derive from this one. Cancelled inside
	// Run() at shutdown so claude CLIs and port-forwards drain cleanly.
	parentCtx, cancelManager := context.WithCancel(context.Background())
	// Guard against early-return paths leaking the context. The defer is
	// cleared once the Server struct takes ownership of cancelManager.
	// Cancel is idempotent so firing it on both paths is safe.
	cancelOwned := false
	defer func() {
		if !cancelOwned {
			cancelManager()
		}
	}()
	manager := NewManager(parentCtx, opts.SessionsRoot)
	editorMgr := editor.NewManager(parentCtx, opts.SessionsRoot)

	if err := manager.Restore(); err != nil {
		// Non-fatal: a corrupt session shouldn't block fresh investigations.
		// Log to stderr and proceed; the bad sessions just don't appear in
		// the sidebar.
		fmt.Fprintf(os.Stderr, "investigate: restore previous sessions: %v\n", err)
	}
	// Backfill PushedAt for any restored session whose slug already
	// exists in the upstream sessions clone (e.g. pushed from another
	// machine and synced down, or pushed from this machine before the
	// per-session persistence path was reliable). Best-effort; a
	// missing sessions clone or a permission error is silently ignored.
	manager.ReconcileUpstreamPushed(opts.SessionsPath)
	// Best-effort PR-state refresh: one `gh pr list` against the
	// upstream sessions repo, mapped to investigations by PushURL.
	// Lets the UI show "merged" / "closed" pills and bring the push
	// button back when the operator closed without merging. Failures
	// are routine (gh not authed, offline, no sessions repo) — log
	// and continue; the frontend can re-trigger the refresh later.
	if opts.SessionsPath != "" {
		repo := SessionsRepoFor(opts.Profile, opts.SessionsRepo)
		// One-shot refresh at startup so the UI lights up correctly
		// on first paint; the periodic refresh below keeps it fresh
		// without the operator having to click anything.
		go func() {
			refreshCtx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
			defer cancel()
			if changed, err := manager.RefreshPRStates(refreshCtx, repo); err != nil {
				fmt.Fprintf(os.Stderr, "investigate: refresh PR states: %v\n", err)
			} else if changed > 0 {
				fmt.Fprintf(os.Stderr, "investigate: refreshed %d PR state(s)\n", changed)
			}
		}()
		// Background ticker — every 5 min fold in any merge / close
		// that happened since the last refresh. Tied to parentCtx so
		// it dies cleanly on Server.Run() shutdown.
		go func() {
			t := time.NewTicker(5 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-parentCtx.Done():
					return
				case <-t.C:
					refreshCtx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
					_, err := manager.RefreshPRStates(refreshCtx, repo)
					cancel()
					if err != nil {
						// Don't spam stderr on every routine failure
						// (gh might be temporarily unauth'd, network
						// flap, etc.) — only log if this is the first
						// failure of the session, but keep it cheap.
						fmt.Fprintf(os.Stderr, "investigate: periodic PR refresh: %v\n", err)
					}
				}
			}
		}()
	}
	// Initialise the user playbooks dir as a git repo if it isn't one
	// yet. Saves later in the session need somewhere to commit; this
	// is the single source-of-truth for the playbook history axis.
	if err := gitInitUserPlaybooksDir(parentCtx, opts.UserPlaybooksDir); err != nil {
		return nil, err
	}
	// Pre-load embedded playbooks + tool catalog in-process. Failures
	// don't block startup — the editor surfaces "metadata unavailable"
	// rather than the launcher refusing to come up.
	cache := &metaCache{}
	if meta, err := loadMeta(parentCtx, opts.PluginPlaybooksDir, opts.SystemPlaybooksDir); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: load meta: %v\n", err)
	} else {
		cache.set(meta)
		// Visibility on the new system-playbooks plumbing — if zero
		// loaded against a configured dir, the operator's clone is
		// either missing, empty, or in the old flat layout.
		fmt.Fprintf(os.Stderr, "investigate: loaded %d system playbooks from %s\n",
			len(meta.Playbooks), opts.PluginPlaybooksDir)
		if len(meta.Playbooks) == 0 && opts.PluginPlaybooksDir != "" {
			fmt.Fprintf(os.Stderr, "triagent: WARNING — no system playbooks loaded. The dir %s should hold <type>/<id>.yaml subdirectories. Re-clone with: rm -rf %s && triagent start\n",
				opts.PluginPlaybooksDir, opts.PluginPlaybooksDir)
		}
	}

	// Probe optional dev-tool capabilities (currently: gh CLI). Logged at startup so the operator
	// immediately sees which features are wired; the result is also
	// exposed via /api/capabilities so the frontend can disable buttons
	// whose underlying tool isn't ready.
	//
	// Resolve linked repos up front so checkCapabilities can include the
	// codefix gate. Errors are non-fatal: a bad user file shouldn't
	// prevent the server from starting; just treat it as no repos for this
	// probe (the real resolution at preflight time will surface any errors).
	startupUser, _ := repos.LoadUserRepos(opts.UserReposPath)
	linkedAtStartup := repos.Resolve(repos.ProfileDefaults(opts.Profile), startupUser)
	playbooksSubpath, wikiSubpath, sessionsSubpath := "", "", ""
	if opts.Profile != nil {
		playbooksSubpath = opts.Profile.Defaults.PlaybooksPath
		wikiSubpath = opts.Profile.Defaults.WikiPath
		sessionsSubpath = opts.Profile.Defaults.SessionsPath
	}
	caps := checkCapabilities(parentCtx, opts.PluginPlaybooksDir, opts.PlaybooksRepo, playbooksSubpath, opts.WikiPath, opts.WikiRepo, wikiSubpath, opts.SessionsPath, opts.SessionsRepo, sessionsSubpath, len(linkedAtStartup) > 0)
	logCapabilities(caps)

	credsDir := opts.CredentialsDir
	if credsDir == "" {
		// Default to the launcher's per-plugin config home (same shape as
		// SystemPlaybooksDir / WikiPath defaults). Best-effort: fall back
		// to the sessions root if the OS can't tell us a config dir.
		if home, err := os.UserConfigDir(); err == nil {
			credsDir = filepath.Join(home, "triagent")
		} else {
			credsDir = opts.SessionsRoot
		}
	}
	connMgr := connections.NewWithDir(credsDir)

	// Per-launch ingest bearer token. Used by the ingestion-agent MCP
	// (triagent-mcp serve --kind=signal-ingest) to authenticate its loopback
	// POSTs to /api/watches/{id}/ingest/*. 32 bytes of entropy is plenty;
	// hex-encoded so it's safe to ship in env vars.
	ingestTokenBuf := make([]byte, 32)
	if _, err := rand.Read(ingestTokenBuf); err != nil {
		return nil, fmt.Errorf("mint ingest token: %w", err)
	}
	ingestToken := hex.EncodeToString(ingestTokenBuf)

	// Launcher loopback URL the ingestion-agent MCP posts back to.
	// Derived from the bound listener so we don't have to guess a port.
	launcherLoopback := "http://" + listener.Addr().String()

	// Build the watches subsystem after connMgr so we can register the
	// slack source when an operator has a slack token on file. The
	// github source needs no credential (it shells out to `gh`), so it
	// always registers. Tasks 1.13–1.16 + 2.1 of the signal-watches
	// design build this up.
	var watchesMgr *watches.Manager
	if opts.UserWatchesPath != "" {
		sourcesReg := watches.NewSourceRegistry()
		sourcesReg.Register(sources.NewGitHub())
		if slackTok, _ := connMgr.GetSlackToken(); slackTok != "" {
			sourcesReg.Register(sources.NewSlack(sources.NewLauncherSlackAdapter("", slackTok)))
		}
		ingestor := &watches.ClaudeIngestor{
			ClaudeBinary: "claude",
			BuildEnv:     func(_ string) []string { return os.Environ() },
			Semaphore:    watches.DefaultIngestSemaphore(),
			BuildMCPConfig: func(watchID, dataDir string) (string, error) {
				return watches.WriteMCPConfig(watches.MCPConfigOpts{
					Dir:           dataDir,
					MCPBinaryPath: opts.MCPBinaryPath,
					LauncherURL:   launcherLoopback,
					LauncherToken: ingestToken,
					WatchID:       watchID,
					WikiPath:      opts.WikiPath,
					WikiProposals: opts.WikiProposalsPath,
				})
			},
			BuildPrompt: func(w watches.Watch) watches.PromptInputs {
				return watches.PromptInputs{
					WatchName:          w.Name,
					WatchKind:          string(w.Source.Kind),
					SourceDescription:  watchSourceDescription(w),
					CustomInstructions: w.Ingest.CustomInstructions,
					WikiAvailable:      opts.WikiPath != "",
				}
			},
			Publisher: watchesPubShim{m: manager},
		}
		// createFromWatch is the seam Manager.SpawnFromSignal and
		// Manager.ManualStartFromSignal use to actually create an
		// investigation. Mirrors handlePreflight's flow: makeSessionDir
		// → preflight.Run → Register, plus the originating-watch /
		// signal back-references so the SessionView chip + spawner
		// terminal hook work. After Register we kick the main claude
		// session (so watch-spawned sessions actually run without
		// requiring a human to open the SessionView first) and, when
		// the signal asked for auto mode, fire EnableAuto via the
		// Manager's StartFromWatch helper — without that, an auto
		// watch's investigation would sit idle until someone opened
		// the tab.
		createFromWatch := func(ctx context.Context, cr watches.CreateRequest) (string, error) {
			sessionDir, err := makeSessionDir(opts.SessionsRoot)
			if err != nil {
				return "", fmt.Errorf("session dir: %w", err)
			}
			invID, err := NewID()
			if err != nil {
				return "", fmt.Errorf("generate id: %w", err)
			}
			userList, _ := repos.LoadUserRepos(opts.UserReposPath)
			linked := repos.Resolve(repos.ProfileDefaults(opts.Profile), userList)
			slackTok, _ := connMgr.GetSlackToken()
			ioTok, _ := connMgr.GetIncidentioToken()
			telemetryURL := "http://" + listener.Addr().String() + "/api/internal/tool-events"
			res, err := preflight.Run(preflight.Options{
				Provider:           opts.Provider,
				Profile:            opts.Profile,
				Ctx:                ctx,
				Namespace:          "",
				SessionDir:         sessionDir,
				MCPBinaryPath:      opts.MCPBinaryPath,
				DocsServerName:     opts.DocsServerName,
				LinkedRepos:        linked,
				GitCacheDir:        opts.GitCacheDir,
				UserPlaybooksDir:   opts.UserPlaybooksDir,
				PluginPlaybooksDir: opts.PluginPlaybooksDir,
				SystemPlaybooksDir: opts.SystemPlaybooksDir,
				WikiPath:           opts.WikiPath,
				WikiProposalsPath:  resolveWikiProposalsPath(opts.WikiProposalsPath),
				SlackToken:         slackTok,
				IncidentioToken:    ioTok,
				TelemetryURL:       telemetryURL,
				TraceID:            invID,
				TelemetryToken:     token,
			})
			if err != nil {
				return "", fmt.Errorf("preflight: %w", err)
			}
			inv := &Investigation{
				ID:                   invID,
				Notes:                cr.Briefing,
				MCPConfigPath:        res.MCPConfigPath,
				KubeconfigPath:       res.KubeconfigPath,
				DocsPrefix:           res.DocsPrefix,
				SessionDir:           sessionDir,
				SlackMCPEnabled:      slackTok != "",
				IncidentioMCPEnabled: ioTok != "",
				LinkedRepos:          linked,
				OriginatingWatchID:   cr.WatchID,
				OriginatingSignal:    &OriginatingSignal{WatchID: cr.WatchID, SignalID: cr.SignalID},
				Profile:              opts.Profile,
			}
			inv.Author = resolveGitAuthor()
			if watchesMgr != nil {
				if w, ok := watchesMgr.Get(cr.WatchID); ok {
					inv.Label = watches.FormatWatchTitle(w.Name, cr.Briefing)
				}
			}
			if _, err := manager.Register(inv); err != nil {
				return "", fmt.Errorf("register investigation: %w", err)
			}
			if br := strings.TrimSpace(cr.Briefing); br != "" {
				// In the auto-start path the briefing is authored by the
				// watch's ingestion agent, not a human — tag it Origin
				// "operator" so SessionView renders the same pink/Bot
				// chrome it uses for later AutoOperator send_message
				// turns. Manual-start (operator clicked through the
				// dialog) stays as a human turn.
				origin := ""
				if cr.AutoMode && !cr.ManuallyStarted {
					origin = "operator"
				}
				inv.publish(EventEnvelope{Kind: envKindUser, Origin: origin, Text: br})
			}

			// Build AutoOptions when the watch asked for auto mode. The
			// operator MCP config is written here (same shape as the
			// preflight path) so the operator agent can POST back to
			// /auto/send-message etc. Failure to write degrades to manual
			// mode rather than aborting the spawn — the investigation
			// itself is still useful as a non-auto session.
			var autoOpts *AutoOptions
			if cr.AutoMode {
				opPath, oerr := buildOperatorMCPConfig(inv.SessionDir, opts.MCPBinaryPath, telemetryURL, token, inv.ID)
				if oerr != nil {
					fmt.Fprintf(os.Stderr, "investigate: auto mode disabled for %s (cannot write op-mcp.json): %v\n", inv.ID, oerr)
				} else {
					autoOpts = &AutoOptions{
						OperatorMCPConfigPath: opPath,
						OperatorCwd:           filepath.Join(inv.SessionDir, "auto"),
						Briefing:              autoBriefing(inv, ""),
						Env: []string{
							"TRIAGENT_MCP_TELEMETRY_URL=" + telemetryURL,
							"TRIAGENT_MCP_TELEMETRY_TOKEN=" + token,
							"TRIAGENT_MCP_TRACE_ID=" + inv.ID,
						},
						Profile: opts.Profile,
					}
				}
			}

			// Kick the main claude session and (when set) the auto
			// operator. StartFromWatch handles its own error logging so a
			// transient Start failure doesn't mark the queued signal as
			// failed — the operator can still open the session and retry
			// manually.
			manager.StartFromWatch(inv, inv.Start, autoOpts)
			return invID, nil
		}

		wm, werr := watches.NewManager(watches.ManagerOpts{
			UserWatchesPath: opts.UserWatchesPath,
			Sources:         sourcesReg,
			Ingestor:        ingestor,
			Create:          createFromWatch,
			Publisher:       watchesPubShim{m: manager},
			// Reconciliation seam. The per-watch Spawner reads queue.json's
			// Running list on construction; without IsLive, archives or
			// deletes that happened while no Spawner existed (lazy
			// construction, empty queue at the time) would leak those slots
			// forever — every subsequent signal would refuse to pop because
			// "capacity is full" against ghosts. Returning false for a
			// missing inv covers deletes; the Archived check covers the
			// archive path.
			IsLive: func(invID string) bool {
				inv := manager.Get(invID)
				if inv == nil {
					return false
				}
				return !inv.Snapshot().Archived
			},
		})
		if werr != nil {
			return nil, fmt.Errorf("watches manager: %w", werr)
		}
		wm.Start(parentCtx)
		if rerr := wm.RehydrateOrphans(); rerr != nil {
			fmt.Fprintf(os.Stderr, "investigate: watches rehydrate: %v\n", rerr)
		}
		watchesMgr = wm
	}

	// M6.4: when an investigation that was spawned from a watch reaches a
	// terminal state, release the per-watch Spawner slot so the next queued
	// signal can proceed. The hook only fires when OriginatingWatchID is set,
	// so normal preflight-created investigations are unaffected.
	if watchesMgr != nil {
		manager.SetTerminalHook(func(invID, watchID string) {
			watchesMgr.NotifyTerminal(watchID, invID)
		})
	}

	var promForwarder *promforward.Manager
	if opts.Profile != nil && opts.Profile.Defaults.Prometheus.Service != "" {
		target := promforward.Target{
			Service:   opts.Profile.Defaults.Prometheus.Service,
			Namespace: opts.Profile.Defaults.Prometheus.Namespace,
			Port:      opts.Profile.Defaults.Prometheus.Port,
		}
		promForwarder = promforward.NewManager(promforward.Options{
			KubeBuilder: func(invID, ctxName string) (*rest.Config, kubernetes.Interface, error) {
				inv := manager.Get(invID)
				if inv == nil {
					return nil, nil, fmt.Errorf("investigation %q not found", invID)
				}
				cfg, err := buildKubeConfigForContext(inv.KubeconfigPath, ctxName)
				if err != nil {
					return nil, nil, err
				}
				cs, err := kubernetes.NewForConfig(cfg)
				if err != nil {
					return nil, nil, err
				}
				return cfg, cs, nil
			},
			Target: target,
		})
		manager.SetCloseHook(func(invID string) {
			promForwarder.Stop(invID)
		})
	}

	api := &apiHandlers{
		opts:         opts,
		manager:      manager,
		editorMgr:    editorMgr,
		connections:  connMgr,
		prof:         opts.Profile,
		capabilities: caps,
		metaCache:    cache,
		mcpHealth:    newMCPHealth(),
		codefixGh:    realCodefixGhRunner{},
		watches:      watchesMgr,
		// Loopback URL triagent-mcp posts telemetry to. The listener is bound
		// before this point so its address is known. Token is the same
		// per-launch secret the SPA cookie carries — triagent-mcp inherits it
		// via the per-session MCP env block.
		telemetryURL:   "http://" + listener.Addr().String() + "/api/internal/tool-events",
		telemetryToken: token,
		ingestToken:    ingestToken,
		// Use the launcher's resolved MCPBinaryPath (set by
		// locateMCPBinary at startup) — every other triagent-mcp
		// subprocess in this package already uses it. Bare "triagent-mcp"
		// on $PATH was a footgun: the binary lives alongside the
		// launcher, not necessarily on the user's PATH.
		sessionDrafter:     defaultSessionDrafter(opts.MCPBinaryPath),
		architectureWorker: repos.NewArchitectureWorker(opts.MCPBinaryPath, opts.GitCacheDir),
		generationContext:  manager.ParentContext,
		promForwarder:      promForwarder,
	}
	api.architectureWorker.SetEventPublisher(repoSummaryPublisher{manager: manager})

	// Ensure the triagent-proposal label exists on every linked repo so
	// create_github_issue (which always attaches it) doesn't fail on
	// a repo where the label hasn't been seeded yet. Async +
	// best-effort: failures only show up in logs, and the codefix
	// capability gate already keeps the button disabled when gh
	// isn't authed.
	go api.ensureCodefixLabels(parentCtx, linkedAtStartup)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", handleHealth)
	api.register(mux)
	mux.Handle("/", spaFileServer(frontFS))

	cancelOwned = true
	// HTTP request logging is opt-in: set TRIAGENT_DEBUG=1 to get one
	// line per response in the launcher terminal. Default-off keeps normal
	// runs quiet — the env var is the escape hatch when the SPA shows an
	// unexplained 404 / "page couldn't load" and we need request-flow
	// evidence to localise the failure.
	handler := authMiddleware(token, mux)
	if os.Getenv("TRIAGENT_DEBUG") != "" {
		handler = requestLogger(handler)
	}
	return &Server{
		httpServer: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		},
		listener:      listener,
		token:         token,
		manager:       manager,
		editorMgr:     editorMgr,
		cancelManager: cancelManager,
		watchesMgr:    watchesMgr,
	}, nil
}

// URL returns the launch URL with the token attached as a query param.
// Loading it sets the auth cookie; subsequent requests can drop the param.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?%s=%s", s.listener.Addr(), tokenQuery, s.token)
}

// Token returns the per-launch token. Used by the launcher to print it
// alongside the URL for diagnostic purposes.
func (s *Server) Token() string { return s.token }

// Run serves until ctx is cancelled. Returns nil on clean shutdown. Every
// live investigation is torn down before Run returns so port-forwards don't
// outlive the process.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.httpServer.Serve(s.listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		s.cancelManager()
		s.manager.Shutdown()
		s.editorMgr.Shutdown()
		if s.watchesMgr != nil {
			s.watchesMgr.Stop()
		}
		return nil
	case err := <-errCh:
		s.cancelManager()
		s.manager.Shutdown()
		s.editorMgr.Shutdown()
		if s.watchesMgr != nil {
			s.watchesMgr.Stop()
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// requestLogger emits one line per HTTP response with method, path,
// status, and duration. Wraps the entire handler stack so static asset
// hits, 404s, and SPA fallbacks all show up. Diagnostic — keep until
// the wiki backfill 404 mystery is closed, then it's safe to remove.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		log.Printf("HTTP %s %s -> %d (%s)", r.Method, path, rec.status, time.Since(start))
	})
}

// statusRecorder captures the eventual response status without disturbing
// the underlying ResponseWriter. WriteHeader is the only hook needed —
// writes that skip WriteHeader implicitly use 200, which is the default.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// authMiddleware enforces the launch token. Requests carrying it as a
// query param (?token=…) get the token cookie set and a redirect to the
// same URL without the param, so the SPA's reload doesn't leave the
// secret in the address bar. Subsequent requests authenticate via cookie.
//
// The /api/internal/* surface is exempted: those endpoints are called
// by triagent-mcp subprocesses (no cookies, no query token) and use a Bearer
// header instead, checked at the handler level.
func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/internal/") {
			next.ServeHTTP(w, r)
			return
		}
		// Ingestion-agent loopback: the triagent-signal-ingest MCP subprocess
		// POSTs to /api/watches/<id>/ingest/* with a per-launch bearer
		// token, not the SPA launch-token cookie. Exempt those paths
		// from the cookie check — handler-level checkIngestBearer is
		// the actual auth gate.
		if isWatchIngestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if q := r.URL.Query().Get(tokenQuery); q != "" {
			if q != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     tokenCookie,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			// Strip the token from the URL and redirect to the canonical form.
			redirected := *r.URL
			q := redirected.Query()
			q.Del(tokenQuery)
			redirected.RawQuery = q.Encode()
			http.Redirect(w, r, redirected.String(), http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(tokenCookie)
		if err != nil || c.Value != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isWatchIngestPath matches the ingestion-agent loopback shape:
// /api/watches/<id>/ingest/<segment> (history, start-investigation,
// report-unclear, dismiss-items). Callers exempt these from the
// SPA cookie check because the MCP subprocess uses a bearer header
// the handler itself validates.
func isWatchIngestPath(p string) bool {
	const prefix = "/api/watches/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	rest := p[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return false
	}
	return strings.HasPrefix(rest[slash+1:], "ingest/")
}

// newToken returns a 32-byte hex-encoded random token. Used as the
// per-launch shared secret between the printed URL and the SPA.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"version": "0.1.0",
	})
}

// spaFileServer returns a handler that serves the embedded frontend as a SPA
// (single-page application) with a nearest-parent fallback. For static assets
// (_next/…, *.html, *.json, *.txt) it serves the file directly. For paths
// that resolve to a missing file it:
//
//  1. Tries the exact path and <path>/index.html.
//  2. Tries progressively-underscored variants of the path to match
//     Next.js dynamic-route placeholder shells. For example
//     /sessions/abc123 → tries sessions/_/index.html.
//     When a placeholder shell is found its content is served while the
//     browser URL remains the original path so useParams() picks up the
//     real ID.
//  3. Falls back to walking up to the nearest parent index.html, then to
//     the root index.html as a last resort.
func spaFileServer(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always serve static assets and explicitly-generated pages directly.
		p := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(p, "_next/") {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Normalise trailing slash so /mcp and /mcp/ resolve identically.
		// `embed.FS` (and any io/fs that uses fs.ValidPath) rejects paths
		// with trailing or double slashes, which previously broke the
		// candidate matching for trailing-slash URLs and silently fell
		// through to the dynamic-route placeholder logic — serving e.g.
		// `mcp/_/index.html` (the [server] placeholder) instead of the
		// canonical `mcp/index.html`.
		p = strings.TrimSuffix(p, "/")

		// Empty path is the root "/" — let http.FileServer's own root
		// resolution handle it (avoids the dynamic-placeholder loop
		// matching nothing useful).
		if p == "" {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Resolve the cleaned path (with trailingSlash: true Next.js always
		// generates <path>/index.html, so try that first). On match we
		// rewrite the request to the directory form ("/<dir>/") so
		// http.FileServer reads index.html out of the directory naturally.
		// Rewriting to "/<dir>/index.html" instead would tickle
		// http.FileServer's canonicalisation rule that 301-redirects
		// /index.html → /, which against this same handler creates a
		// redirect loop.
		candidates := []string{
			p + "/index.html",
			p,
		}
		for _, c := range candidates {
			if f, err := fsys.Open(c); err == nil {
				_ = f.Close()
				r2 := r.Clone(r.Context())
				if strings.HasSuffix(c, "/index.html") {
					r2.URL.Path = "/" + strings.TrimSuffix(c, "index.html")
				} else {
					r2.URL.Path = "/" + c
				}
				fileServer.ServeHTTP(w, r2)
				return
			}
		}

		// Try dynamic-route placeholder shells. For a path like
		// "watches/<id>/edit" the matching shell is "watches/_/edit/index.html"
		// — the dynamic segment is in the MIDDLE, not the tail. So we
		// iterate every non-empty subset of segments (by popcount,
		// fewest substitutions first) and try each as a candidate shell.
		// For n ≤ 8 segments this is at most 255 attempts, trivial.
		// First hit wins. We serve the shell content but rewrite the
		// request URL to the shell path so http.FileServer finds the
		// file; the browser's address bar stays at the original URL
		// because this is just an internal rewrite of the in-flight
		// request, not a redirect.
		segments := strings.Split(p, "/")
		if n := len(segments); n > 0 && n <= 8 {
			// Generate all 2^n - 1 non-empty subsets, sorted by popcount
			// ascending (and by mask value within each popcount band) so
			// the shell with the fewest dynamic segments matches first —
			// a static page wins over a dynamic one when both exist.
			type mask struct {
				bits   uint
				popcnt int
			}
			masks := make([]mask, 0, 1<<n-1)
			for b := uint(1); b < (1 << n); b++ {
				masks = append(masks, mask{bits: b, popcnt: bits.OnesCount(b)})
			}
			sort.SliceStable(masks, func(i, j int) bool {
				if masks[i].popcnt != masks[j].popcnt {
					return masks[i].popcnt < masks[j].popcnt
				}
				return masks[i].bits < masks[j].bits
			})
			for _, m := range masks {
				underscored := make([]string, n)
				copy(underscored, segments)
				for j := 0; j < n; j++ {
					if m.bits&(1<<j) != 0 {
						underscored[j] = "_"
					}
				}
				candidate := strings.Join(underscored, "/") + "/index.html"
				if f, err := fsys.Open(candidate); err == nil {
					_ = f.Close()
					shellDir := strings.Join(underscored, "/")
					r2 := r.Clone(r.Context())
					r2.URL.Path = "/" + shellDir + "/"
					fileServer.ServeHTTP(w, r2)
					return
				}
			}
		}

		// Walk up to the nearest parent that has an index.html.
		dir := p
		for {
			idx := strings.LastIndex(dir, "/")
			if idx < 0 {
				break
			}
			dir = dir[:idx]
			candidate := dir + "/index.html"
			if f, err := fsys.Open(candidate); err == nil {
				_ = f.Close()
				// Rewrite the request to serve the parent index.html.
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/" + dir + "/"
				fileServer.ServeHTTP(w, r2)
				return
			}
		}

		// Fallback: root index.html (the main SPA shell).
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// watchesPubShim adapts the watches.Publisher interface to
// Manager's global event publish helpers (Task 2.8). Lives at file
// scope so server.go is the only place that knows about both sides.
type watchesPubShim struct{ m *Manager }

func (s watchesPubShim) PublishWatchStatus(id, status string, lastPolledAt time.Time, errorCount, running, queued int) {
	s.m.PublishWatchStatus(WatchStatusEvent{
		WatchID:      id,
		Status:       status,
		LastPolledAt: lastPolledAt.UTC().Format(time.RFC3339),
		ErrorCount:   errorCount,
		Running:      running,
		Queued:       queued,
	})
}

func (s watchesPubShim) PublishSignalCreated(watchID, signalID, outcome, investigationID string, manual bool) {
	s.m.PublishSignalCreated(SignalCreatedEvent{
		WatchID:         watchID,
		SignalID:        signalID,
		Outcome:         outcome,
		InvestigationID: investigationID,
		ManuallyStarted: manual,
	})
}

func (s watchesPubShim) PublishItemCaptured(watchID, itemID, signalID string, filtered bool) {
	s.m.PublishItemCaptured(ItemCapturedEvent{
		WatchID:  watchID,
		ItemID:   itemID,
		SignalID: signalID,
		Filtered: filtered,
	})
}

func (s watchesPubShim) PublishIngestRunStarted(watchID, runID string, startedAt time.Time, itemCount int) {
	s.m.PublishIngestRunStarted(IngestRunStartedEvent{
		WatchID:   watchID,
		RunID:     runID,
		StartedAt: startedAt.UTC().Format(time.RFC3339),
		ItemCount: itemCount,
	})
}

func (s watchesPubShim) PublishIngestRunFinished(watchID, runID, status string, durationMs int64, errorMsg string) {
	s.m.PublishIngestRunFinished(IngestRunFinishedEvent{
		WatchID:    watchID,
		RunID:      runID,
		Status:     status,
		DurationMs: durationMs,
		Error:      errorMsg,
	})
}

// repoSummaryPublisher adapts Manager.publishGlobalEvent to the
// repos.EventPublisher interface. The repos package can't depend on
// the server package's GlobalEventEnvelope (layering); this sits at
// the boundary and translates.
type repoSummaryPublisher struct {
	manager *Manager
}

func (p repoSummaryPublisher) PublishStarted(owner, name string) {
	now := time.Now().UTC()
	p.manager.publishGlobalEvent(GlobalEventEnvelope{
		Kind: globalKindRepoSummaryState,
		RepoSummary: &RepoSummaryStatePayload{
			Owner:     owner,
			Name:      name,
			Phase:     "started",
			StartedAt: &now,
		},
	})
}

func (p repoSummaryPublisher) PublishSuccess(owner, name string, generatedAt time.Time, byteCount int, kind string) {
	at := generatedAt
	p.manager.publishGlobalEvent(GlobalEventEnvelope{
		Kind: globalKindRepoSummaryState,
		RepoSummary: &RepoSummaryStatePayload{
			Owner:       owner,
			Name:        name,
			Phase:       "success",
			GeneratedAt: &at,
			ByteCount:   byteCount,
			Kind:        kind,
		},
	})
}

func (p repoSummaryPublisher) PublishError(owner, name string, msg string) {
	p.manager.publishGlobalEvent(GlobalEventEnvelope{
		Kind: globalKindRepoSummaryState,
		RepoSummary: &RepoSummaryStatePayload{
			Owner: owner,
			Name:  name,
			Phase: "error",
			Error: msg,
		},
	})
}

// watchSourceDescription builds the human-readable source line that the
// ingestion-agent prompt template renders next to the watch kind. For
// github_issues that's "owner/repo[ · labels:…]"; for slack_channel it's
// "#channelName" (falling back to the raw channel ID when no display
// name is set).
func watchSourceDescription(w watches.Watch) string {
	switch w.Source.Kind {
	case watches.SourceGitHubIssues:
		if len(w.Source.Labels) == 0 {
			return w.Source.Owner + "/" + w.Source.Repo
		}
		return w.Source.Owner + "/" + w.Source.Repo + " · labels: " + strings.Join(w.Source.Labels, ", ")
	case watches.SourceSlackChannel:
		if w.Source.ChannelName != "" {
			return "#" + w.Source.ChannelName
		}
		return w.Source.ChannelID
	}
	return string(w.Source.Kind)
}
