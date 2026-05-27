package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sourcehawk/triagent/pkg/mcp/agentoperator"
	"github.com/sourcehawk/triagent/pkg/mcp/git"
	"github.com/sourcehawk/triagent/pkg/mcp/incidentio"
	"github.com/sourcehawk/triagent/pkg/mcp/k8s"
	mcpmeta "github.com/sourcehawk/triagent/pkg/mcp/meta"
	"github.com/sourcehawk/triagent/pkg/mcp/parallel"
	"github.com/sourcehawk/triagent/pkg/mcp/prom"
	"github.com/sourcehawk/triagent/pkg/mcp/sessions"
	"github.com/sourcehawk/triagent/pkg/mcp/signalingest"
	"github.com/sourcehawk/triagent/pkg/mcp/slack"
	"github.com/sourcehawk/triagent/pkg/mcp/strategies"
	"github.com/sourcehawk/triagent/pkg/mcp/teleport"
	"github.com/sourcehawk/triagent/pkg/mcp/wiki"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

// Environment variable names. Flags override env when both are set.
const (
	envKubeconfig       = "TRIAGENT_MCP_KUBECONFIG"
	envCRDsFile         = "TRIAGENT_MCP_CRDS_FILE"
	envCrossplaneGroups = "TRIAGENT_MCP_CROSSPLANE_GROUPS"
	envSessionDir                  = "TRIAGENT_MCP_SESSION_DIR"
	envUserPlaybooksDir             = "TRIAGENT_MCP_USER_PLAYBOOKS_DIR"
	envPluginPlaybooksDir           = "TRIAGENT_MCP_PLUGIN_PLAYBOOKS_DIR"
	envSystemPlaybooksDir           = "TRIAGENT_MCP_SYSTEM_PLAYBOOKS_DIR"
	envStrategiesSubagentModel      = "TRIAGENT_MCP_STRATEGIES_SUBAGENT_MODEL"
	envMCPConfigPath                = "TRIAGENT_MCP_CONFIG_PATH"
	envGitRepo               = "TRIAGENT_MCP_GIT_REPO"
	envGitCacheDir           = "TRIAGENT_MCP_GIT_CACHE_DIR"
	envGitClaudeBinary       = "TRIAGENT_MCP_GIT_CLAUDE_BINARY"
	envGitFilterPrereleases  = "TRIAGENT_MCP_GIT_FILTER_PRERELEASES"
	envWikiServePath         = "TRIAGENT_MCP_WIKI_PATH"
	envWikiServeProposalsPath = "TRIAGENT_MCP_WIKI_PROPOSALS_PATH"
	envWikiServeClaudeBinary = "TRIAGENT_MCP_WIKI_CLAUDE_BINARY"

	envSessionsProposalsPath = "TRIAGENT_MCP_SESSIONS_PROPOSALS_PATH"
	envSessionsClaudeBinary  = "TRIAGENT_MCP_SESSIONS_CLAUDE_BINARY"

	envSlackToken = "TRIAGENT_MCP_SLACK_TOKEN"

	envIncidentioToken = "TRIAGENT_MCP_INCIDENTIO_TOKEN"

	envPromURL    = "TRIAGENT_MCP_PROM_URL"
	envPromBearer = "TRIAGENT_MCP_PROM_BEARER"
	envPromBasic  = "TRIAGENT_MCP_PROM_BASIC"
)

type serveFlags struct {
	kind string

	// k8s flags
	kubeconfig       string
	crdsFile         string
	crossplaneGroups string

	// strategies flags
	sessionDir         string
	userPlaybooksDir   string
	pluginPlaybooksDir string
	systemPlaybooksDir string

	// git flags
	gitRepo               string
	gitCacheDir           string
	gitClaudeBinary       string
	gitFilterPrereleases  bool

	// wiki flags
	wikiPath          string
	wikiProposalsPath string
	wikiClaudeBinary  string

	// sessions flags
	sessionsProposalsPath string
	sessionsClaudeBinary  string

	// slack flags
	slackToken string

	// incidentio flags
	incidentioToken string

	// prom flags
	promURL    string
	promBearer string
	promBasic  string
}

func serveCmd() *cobra.Command {
	f := &serveFlags{}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run one of the triagent-mcp MCP servers over stdio",
		Long: "Run one of the triagent-mcp MCP servers over stdio.\n\n" +
			"Select the server via --kind. Supported kinds:\n" +
			"  k8s, teleport, strategies, git, wiki, slack, incidentio, sessions, meta, agent-operator, signal-ingest, parallel, prom",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), resolveFlags(f))
		},
	}
	cmd.Flags().StringVar(&f.kind, "kind", "", "which MCP server to run: k8s, teleport, strategies, git, wiki, slack, incidentio, sessions, meta, agent-operator, signal-ingest, parallel, or prom (required)")

	// k8s flags
	cmd.Flags().StringVar(&f.kubeconfig, "kubeconfig", "", "path to kubeconfig (defaults to $"+envKubeconfig+", then $KUBECONFIG, then ~/.kube/config) [kind=k8s]")
	cmd.Flags().StringVar(&f.crdsFile, "crds-file", "", "JSON file overriding the embedded resource allow-list (defaults to $"+envCRDsFile+") [kind=k8s]")
	cmd.Flags().StringVar(&f.crossplaneGroups, "crossplane-groups", "", "comma-separated glob patterns for Crossplane provider API groups (defaults to $"+envCrossplaneGroups+", then '*.upbound.io,*.crossplane.io') [kind=k8s]")

	// strategies flags
	cmd.Flags().StringVar(&f.sessionDir, "session-dir", "", "directory the strategies walker uses to snapshot state; also required for k8s streaming tools (defaults to $"+envSessionDir+") [kind=strategies,k8s]")
	cmd.Flags().StringVar(&f.userPlaybooksDir, "user-playbooks-dir", "", "directory holding operator-customised playbooks layered over the plugin set (defaults to $"+envUserPlaybooksDir+") [kind=strategies]")
	cmd.Flags().StringVar(&f.pluginPlaybooksDir, "plugin-playbooks-dir", "", "directory the launcher cloned the upstream playbooks repo to; loaded as the 'plugin' set, overridable by user dir (defaults to $"+envPluginPlaybooksDir+") [kind=strategies]")
	cmd.Flags().StringVar(&f.systemPlaybooksDir, "system-playbooks-dir", "", "directory of launcher-bundled meta-playbooks; loaded as the 'system' set, locked + non-overridable (defaults to $"+envSystemPlaybooksDir+") [kind=strategies]")

	// git flags
	cmd.Flags().StringVar(&f.gitRepo, "repo", "", "GitHub repo as owner/name; required (defaults to $"+envGitRepo+") [kind=git]")
	cmd.Flags().StringVar(&f.gitCacheDir, "cache-dir", "", "cache root for cloned repos (defaults to $"+envGitCacheDir+", then $XDG_CACHE_HOME/triagent-mcp/git) [kind=git]")
	cmd.Flags().StringVar(&f.gitClaudeBinary, "claude-binary", "", "path to the claude CLI used by sub-agent tools (defaults to $"+envGitClaudeBinary+", then `claude` on $PATH) [kind=git]")
	cmd.Flags().BoolVar(&f.gitFilterPrereleases, "filter-prereleases", true, "hide semver prerelease tags (1.2.3-rc1 / 1.2.3-mytesttag) from latest_tags by default; -SNAPSHOT and non-semver tags pass through. Override per-call via the tool's include_prereleases input. Defaults to $"+envGitFilterPrereleases+" when set, else true [kind=git]")

	// wiki flags
	cmd.Flags().StringVar(&f.wikiPath, "wiki-path", "", "vault checkout path (defaults to $"+envWikiServePath+") [kind=wiki]")
	cmd.Flags().StringVar(&f.wikiProposalsPath, "wiki-proposals-path", "", "proposals dir (defaults to $"+envWikiServeProposalsPath+") [kind=wiki]")
	cmd.Flags().StringVar(&f.wikiClaudeBinary, "wiki-claude-binary", "", "claude CLI for the propose_wiki_draft sub-agent (defaults to $"+envWikiServeClaudeBinary+", then `claude` on $PATH) [kind=wiki]")

	// sessions flags
	cmd.Flags().StringVar(&f.sessionsProposalsPath, "sessions-proposals-path", "", "proposals dir for the session drafter (defaults to $"+envSessionsProposalsPath+") [kind=sessions]")
	cmd.Flags().StringVar(&f.sessionsClaudeBinary, "sessions-claude-binary", "", "claude CLI for the propose_session_draft sub-agent (defaults to $"+envSessionsClaudeBinary+", then `claude` on $PATH) [kind=sessions]")

	// slack flags
	cmd.Flags().StringVar(&f.slackToken, "slack-token", "", "Slack user/bot token (xoxp-/xoxb-); required (defaults to $"+envSlackToken+") [kind=slack]")

	// incidentio flags
	cmd.Flags().StringVar(&f.incidentioToken, "incidentio-token", "", "incident.io API key; required (defaults to $"+envIncidentioToken+") [kind=incidentio]")

	// prom flags
	cmd.Flags().StringVar(&f.promURL, "prom-url", "", "Prometheus base URL (defaults to $"+envPromURL+") [kind=prom]")
	cmd.Flags().StringVar(&f.promBearer, "prom-bearer", "", "Authorization: Bearer token for Prometheus (defaults to $"+envPromBearer+") [kind=prom]")
	cmd.Flags().StringVar(&f.promBasic, "prom-basic", "", "Basic auth credentials user:pass for Prometheus (defaults to $"+envPromBasic+") [kind=prom]")

	return cmd
}

func resolveFlags(f *serveFlags) serveFlags {
	out := *f
	if out.kubeconfig == "" {
		out.kubeconfig = os.Getenv(envKubeconfig)
	}
	if out.crdsFile == "" {
		out.crdsFile = os.Getenv(envCRDsFile)
	}
	if out.crossplaneGroups == "" {
		out.crossplaneGroups = os.Getenv(envCrossplaneGroups)
	}
	if out.sessionDir == "" {
		out.sessionDir = os.Getenv(envSessionDir)
	}
	if out.userPlaybooksDir == "" {
		out.userPlaybooksDir = os.Getenv(envUserPlaybooksDir)
	}
	if out.pluginPlaybooksDir == "" {
		out.pluginPlaybooksDir = os.Getenv(envPluginPlaybooksDir)
	}
	if out.systemPlaybooksDir == "" {
		out.systemPlaybooksDir = os.Getenv(envSystemPlaybooksDir)
	}
	if out.gitRepo == "" {
		out.gitRepo = os.Getenv(envGitRepo)
	}
	if out.gitCacheDir == "" {
		out.gitCacheDir = os.Getenv(envGitCacheDir)
	}
	if out.gitClaudeBinary == "" {
		out.gitClaudeBinary = os.Getenv(envGitClaudeBinary)
	}
	if out.wikiPath == "" {
		out.wikiPath = os.Getenv(envWikiServePath)
	}
	if out.wikiProposalsPath == "" {
		out.wikiProposalsPath = os.Getenv(envWikiServeProposalsPath)
	}
	if out.wikiClaudeBinary == "" {
		out.wikiClaudeBinary = os.Getenv(envWikiServeClaudeBinary)
	}
	if out.sessionsProposalsPath == "" {
		out.sessionsProposalsPath = os.Getenv(envSessionsProposalsPath)
	}
	if out.sessionsClaudeBinary == "" {
		out.sessionsClaudeBinary = os.Getenv(envSessionsClaudeBinary)
	}
	if out.slackToken == "" {
		out.slackToken = os.Getenv(envSlackToken)
	}
	if out.incidentioToken == "" {
		out.incidentioToken = os.Getenv(envIncidentioToken)
	}
	if out.promURL == "" {
		out.promURL = os.Getenv(envPromURL)
	}
	if out.promBearer == "" {
		out.promBearer = os.Getenv(envPromBearer)
	}
	if out.promBasic == "" {
		out.promBasic = os.Getenv(envPromBasic)
	}
	// Bool env override: only consider when the operator hasn't passed
	// the flag explicitly. Cobra preserves the flag default (true) when
	// unset, so we can't distinguish "operator passed --filter-prereleases=true"
	// from "default kicked in" — but the env override only runs when the
	// env var is non-empty, so the operator can opt out via env without
	// clobbering an explicit flag.
	if v := os.Getenv(envGitFilterPrereleases); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false", "no", "off":
			out.gitFilterPrereleases = false
		case "1", "true", "yes", "on":
			out.gitFilterPrereleases = true
		}
	}
	return out
}

func runServe(ctx context.Context, f serveFlags) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch f.kind {
	case "k8s":
		return runK8s(ctx, f)
	case "teleport":
		return runTeleport(ctx, f)
	case "strategies":
		return runStrategies(ctx, f)
	case "git":
		return runGit(ctx, f)
	case "wiki":
		return runWiki(ctx, f)
	case "slack":
		return runSlack(ctx, f)
	case "incidentio":
		return runIncidentio(ctx, f)
	case "sessions":
		return runSessions(ctx, f)
	case "meta":
		return runMeta(ctx, f)
	case "agent-operator":
		return runAgentOperator(ctx, f)
	case "signal-ingest":
		return runSignalIngest(ctx, f)
	case "parallel":
		return runParallel(ctx, f)
	case "prom":
		return runProm(ctx, f)
	case "":
		return fmt.Errorf("--kind is required (one of: k8s, teleport, strategies, git, wiki, slack, incidentio, sessions, meta, agent-operator, signal-ingest, parallel, prom)")
	default:
		return fmt.Errorf("unknown --kind %q (want one of: k8s, teleport, strategies, git, wiki, slack, incidentio, sessions, meta, agent-operator, signal-ingest, parallel, prom)", f.kind)
	}
}

func runK8s(ctx context.Context, f serveFlags) error {
	kubePath := resolveKubeconfigPath(f.kubeconfig)
	srv, err := k8s.New(ctx, k8s.Options{
		KubeconfigPath:          kubePath,
		AllowlistPath:           f.crdsFile,
		CrossplaneGroupPatterns: splitCSV(f.crossplaneGroups),
		SessionDir:              f.sessionDir,
	})
	if err != nil {
		return fmt.Errorf("build k8s mcp server: %w", err)
	}
	for _, w := range srv.Warnings() {
		log.Warn("mcp serve --kind=k8s", "warning", w)
	}
	log.Info("mcp serve --kind=k8s starting", "kubeconfig", kubePath)
	return srv.Run(ctx)
}

func runTeleport(ctx context.Context, f serveFlags) error {
	kubePath := resolveKubeconfigPath(f.kubeconfig)
	srv, err := teleport.New(teleport.Options{KubeconfigPath: kubePath})
	if err != nil {
		return fmt.Errorf("build teleport mcp server: %w", err)
	}
	log.Info("mcp serve --kind=teleport starting", "kubeconfig", kubePath)
	return srv.Run(ctx)
}

// resolveKubeconfigPath returns the kubeconfig path with the same precedence
// the clientcmd loading rules apply: explicit flag → TRIAGENT_MCP_KUBECONFIG →
// KUBECONFIG → $HOME/.kube/config.
func resolveKubeconfigPath(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv(envKubeconfig); env != "" {
		return env
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.kube/config"
	}
	return ""
}

func runStrategies(ctx context.Context, f serveFlags) error {
	srv, err := strategies.New(strategies.Options{
		SessionDir:         f.sessionDir,
		PluginPlaybooksDir: f.pluginPlaybooksDir,
		SystemPlaybooksDir: f.systemPlaybooksDir,
		UserPlaybooksDir:   f.userPlaybooksDir,
		Models:             strategies.DispatchModels{Subagent: os.Getenv(envStrategiesSubagentModel)},
		MCPConfigPath:      os.Getenv(envMCPConfigPath),
		// SubAgentRunner: nil → defaults to subagent.Run.
		// ParentSessionState: nil → graceful degradation (no parent findings/summary).
	})
	if err != nil {
		return fmt.Errorf("build strategies mcp server: %w", err)
	}
	log.Info("mcp serve --kind=strategies starting",
		"session_dir", f.sessionDir,
		"plugin_playbooks_dir", f.pluginPlaybooksDir,
		"system_playbooks_dir", f.systemPlaybooksDir,
		"user_playbooks_dir", f.userPlaybooksDir,
	)
	return srv.Run(ctx)
}

// runMeta wires the meta MCP. It needs only the telemetry env handshake;
// no per-kind flags. Telemetry env vars are read by mcpmeta.New's Options
// at startup time.
func runMeta(ctx context.Context, _ serveFlags) error {
	srv, err := mcpmeta.New(mcpmeta.Options{
		LauncherURL:   os.Getenv("TRIAGENT_MCP_TELEMETRY_URL"),
		LauncherToken: os.Getenv("TRIAGENT_MCP_TELEMETRY_TOKEN"),
		TraceID:       os.Getenv("TRIAGENT_MCP_TRACE_ID"),
	})
	if err != nil {
		return fmt.Errorf("build meta mcp server: %w", err)
	}
	log.Info("mcp serve --kind=meta starting")
	return srv.Run(ctx)
}

// runAgentOperator wires the agent-operator MCP. Like meta, it needs only
// the launcher telemetry handshake (URL + token + trace id) so the
// operator agent's tools can POST back to the investigate launcher.
func runAgentOperator(ctx context.Context, _ serveFlags) error {
	log.Info("mcp serve --kind=agent-operator starting")
	srv, err := agentoperator.New(agentoperator.Options{
		LauncherURL:   os.Getenv("TRIAGENT_MCP_TELEMETRY_URL"),
		LauncherToken: os.Getenv("TRIAGENT_MCP_TELEMETRY_TOKEN"),
		TraceID:       os.Getenv("TRIAGENT_MCP_TRACE_ID"),
	})
	if err != nil {
		return fmt.Errorf("agent-operator MCP: %w", err)
	}
	return srv.Run(ctx)
}

// runSignalIngest wires the signal-ingest MCP for the short-lived
// ingestion agent the watches subsystem spawns per poll batch. TraceID
// is the watch id, set by the launcher when constructing the MCP config.
func runSignalIngest(ctx context.Context, _ serveFlags) error {
	log.Info("mcp serve --kind=signal-ingest starting")
	srv, err := signalingest.New(signalingest.Options{
		LauncherURL:   os.Getenv("TRIAGENT_MCP_TELEMETRY_URL"),
		LauncherToken: os.Getenv("TRIAGENT_MCP_TELEMETRY_TOKEN"),
		TraceID:       os.Getenv("TRIAGENT_MCP_TRACE_ID"),
	})
	if err != nil {
		return fmt.Errorf("signal-ingest MCP: %w", err)
	}
	return srv.Run(ctx)
}

// runParallel wires the parallel MCP. It needs no per-kind flags; the
// upstream registry comes from $TRIAGENT_MCP_PARALLEL_UPSTREAMS which preflight
// writes (see investigate/internal/preflight/mcpconfig.go).
func runParallel(ctx context.Context, _ serveFlags) error {
	srv, err := parallel.NewFromEnv()
	if err != nil {
		return fmt.Errorf("build parallel mcp server: %w", err)
	}
	log.Info("mcp serve --kind=parallel starting")
	return srv.Run(ctx)
}

func runProm(ctx context.Context, f serveFlags) error {
	if f.promURL == "" {
		return fmt.Errorf("--prom-url is required (or set $%s)", envPromURL)
	}
	srv, err := prom.New(prom.Options{
		Endpoint:  f.promURL,
		Bearer:    f.promBearer,
		BasicAuth: f.promBasic,
	})
	if err != nil {
		return fmt.Errorf("build prom mcp server: %w", err)
	}
	log.Info("mcp serve --kind=prom starting", "endpoint", f.promURL)
	return srv.Run(ctx)
}

func runGit(ctx context.Context, f serveFlags) error {
	if f.gitRepo == "" {
		return fmt.Errorf("--repo is required (owner/name) (set --repo or $%s)", envGitRepo)
	}
	srv, err := git.New(git.Options{
		Repo:              f.gitRepo,
		CacheDir:          f.gitCacheDir,
		ClaudeBinary:      f.gitClaudeBinary,
		FilterPrereleases: f.gitFilterPrereleases,
	})
	if err != nil {
		return fmt.Errorf("build git mcp server: %w", err)
	}
	log.Info("mcp serve --kind=git starting",
		"repo", f.gitRepo,
		"cache_dir", f.gitCacheDir,
		"filter_prereleases", f.gitFilterPrereleases,
	)
	return srv.Run(ctx)
}

func runSlack(ctx context.Context, f serveFlags) error {
	srv, err := slack.New(slack.Options{
		Token: f.slackToken,
	})
	if err != nil {
		return fmt.Errorf("build slack mcp server: %w", err)
	}
	log.Info("mcp serve --kind=slack starting")
	return srv.Run(ctx)
}

func runIncidentio(ctx context.Context, f serveFlags) error {
	srv, err := incidentio.New(ctx, incidentio.Options{
		Token: f.incidentioToken,
	})
	if err != nil {
		return fmt.Errorf("build incidentio mcp server: %w", err)
	}
	log.Info("mcp serve --kind=incidentio starting")
	return srv.Run(ctx)
}

func runWiki(ctx context.Context, f serveFlags) error {
	if f.wikiPath == "" {
		return fmt.Errorf("--wiki-path is required (or set $%s)", envWikiServePath)
	}
	if f.wikiProposalsPath == "" {
		return fmt.Errorf("--wiki-proposals-path is required (or set $%s)", envWikiServeProposalsPath)
	}
	srv, err := wiki.New(wiki.Options{
		VaultPath:     f.wikiPath,
		ProposalsPath: f.wikiProposalsPath,
		ClaudeBinary:  f.wikiClaudeBinary,
	})
	if err != nil {
		return fmt.Errorf("build wiki mcp server: %w", err)
	}
	log.Info("mcp serve --kind=wiki starting",
		"vault", f.wikiPath,
		"proposals", f.wikiProposalsPath,
	)
	return srv.Run(ctx)
}

func runSessions(ctx context.Context, f serveFlags) error {
	if f.sessionsProposalsPath == "" {
		return fmt.Errorf("--sessions-proposals-path is required (or set $%s)", envSessionsProposalsPath)
	}
	srv, err := sessions.New(sessions.Options{
		ProposalsPath: f.sessionsProposalsPath,
		ClaudeBinary:  f.sessionsClaudeBinary,
	})
	if err != nil {
		return fmt.Errorf("build sessions mcp server: %w", err)
	}
	log.Info("mcp serve --kind=sessions starting", "proposals", f.sessionsProposalsPath)
	return srv.Run(ctx)
}

// splitCSV parses a comma-separated glob list. Empty entries are dropped.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
