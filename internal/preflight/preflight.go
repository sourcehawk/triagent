// Package preflight runs all the per-session setup that must succeed before
// claude can start: provider auth check, the per-session MCP config, and
// detection of an externally-registered docs MCP server.
//
// The package has no UI dependencies. Both the TUI launcher and (eventually)
// the web server call Run() and consume the same Result.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/promforward"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
)

// Options describes a single preflight invocation.
type Options struct {
	// Provider is the cluster access provider. IsAuthenticated() is called to
	// verify the session before proceeding. If the provider also implements
	// auth.ReauthAdvisor, its advice is surfaced verbatim on failure.
	Provider  auth.Provider
	Ctx       context.Context
	Namespace string // free-text hint; not validated
	SessionDir     string // writable per-session directory
	MCPBinaryPath  string // absolute path to triagent-mcp
	DocsServerName string // empty = skip docs detection

	// LinkedRepos is the resolved list of GitHub repos to spawn `triagent-mcp
	// serve --kind=git` instances for. Empty means no git MCPs.
	LinkedRepos []repos.LinkedRepo

	// GitCacheDir is the cache root forwarded to each git MCP via
	// $TRIAGENT_MCP_GIT_CACHE_DIR. Empty means each triagent-mcp resolves its own
	// default ($XDG_CACHE_HOME/triagent-mcp/git or ~/.cache/triagent-mcp/git).
	GitCacheDir string

	// UserPlaybooksDir is the directory holding operator-customised
	// strategy playbooks; the launcher's editor writes there, the
	// strategies MCP layers them on top of the system set. Empty means
	// system-only.
	UserPlaybooksDir string

	// PluginPlaybooksDir is the launcher's clone of the upstream
	// playbooks repo. The strategies MCP loads YAML from here; the
	// editor's "push as PR" flow commits + pushes from here; the sync
	// endpoint runs git pull against it. Empty means no plugin set.
	PluginPlaybooksDir string

	// SystemPlaybooksDir is the launcher-bundled directory of locked
	// meta-playbooks. The launcher extracts its embedded `system/`
	// fileset to a known dir at startup and passes the path here.
	// The strategies MCP marks every entry from this dir as Locked
	// (cannot be overridden, edited, or tombstoned via the user dir).
	// Empty means no system set (tests / dev).
	SystemPlaybooksDir string

	// WikiPath is the launcher-managed clone of the upstream wiki repo.
	// When empty, the wiki MCP server is not registered with the agent.
	WikiPath string

	// WikiProposalsPath is the local dir holding wiki drafts. Empty
	// uses the wiki MCP's default ($HOME/.triagent/wiki-proposals).
	WikiProposalsPath string

	// Tokens drive the slack / incidentio MCP wiring. Either token
	// non-empty registers the corresponding `triagent-slack` / `triagent-incidentio`
	// MCP server for this investigation. Channel id, since, incident ref
	// flow through the system prompt and the agent threads them into
	// per-call args; they aren't passed to the MCP at boot any more.
	SlackToken      string
	IncidentioToken string

	// KubeconfigPath, when set, overrides the auto-detected
	// $KUBECONFIG / $HOME/.kube/config. Used by the rehydrate path
	// to feed the launcher-frozen path back through preflight.
	KubeconfigPath string

	// Profile is the loaded org profile for this session. When set and the
	// profile carries a k8s/kinds.json, writeMCPConfig extracts it to the
	// session dir and passes --crds-file=<path> to triagent-mcp. TRIAGENT_MCP_CRDS_FILE
	// env takes precedence over the profile-embedded path.
	Profile *profile.Profile

	// Telemetry plumbed into each triagent-mcp env block so the MCP servers
	// can POST tool-call telemetry back to the launcher. All optional;
	// if URL is empty the launcher is treated as headless and triagent-mcp
	// runs without telemetry (useful for `c1 mcp serve` smoke tests
	// outside the launcher). TraceID is whatever this launcher uses to
	// correlate events back to a session — the investigation id.
	TelemetryURL   string
	TraceID        string
	TelemetryToken string

	// PromTarget is the per-investigation resolved Prometheus target. When
	// non-nil (and PromDisabled is false) the prom MCP server entry is
	// emitted in the session MCP config. When nil, the prom MCP is skipped
	// regardless of any profile defaults (those should already be reflected
	// here by the caller).
	PromTarget *promforward.Target

	// PromDisabled, when true, explicitly suppresses the prom MCP server
	// entry even if PromTarget is non-nil. Set when the operator opts out
	// in the preflight form.
	PromDisabled bool

	// CloudProbe runs the read-only identity probe for one cloud source. Nil
	// uses the default prober (providers.ProbeSource), which constructs the
	// source's provider and shells its CLI; tests inject a stub. The probe
	// degrades, never blocks — a failed probe marks the source unavailable but
	// the session still starts.
	CloudProbe func(context.Context, profile.CloudSource) cloud.IdentityStatus
}

// CloudSourceStatus is one cloud source's preflight outcome: its alias and the
// identity-probe result. A source with Valid:false started the session degraded
// — visibly unavailable, with Hint pointing at the fix.
type CloudSourceStatus struct {
	Alias string
	cloud.IdentityStatus
}

// Result holds the artifacts a successful preflight produces.
type Result struct {
	MCPConfigPath  string
	DocsPrefix     string // e.g. "mcp__example-docs__"; empty when not registered
	KubeconfigPath string // resolved + frozen path; mirrored back to caller for persistence
	// CloudSources is the per-source identity-probe outcome for each profile
	// cloud source. A failed probe degrades that source, never the session.
	CloudSources []CloudSourceStatus
}

// Run performs the full preflight sequence. On any failure, in-flight
// resources are torn down before the error returns.
func Run(opts Options) (*Result, error) {
	if opts.Ctx == nil {
		opts.Ctx = context.Background()
	}
	kubeconfigPath, err := freezeKubeconfig(opts.SessionDir, opts.KubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("freeze kubeconfig: %w", err)
	}

	if opts.Provider == nil {
		return nil, errors.New("preflight: Provider is required")
	}
	if !opts.Provider.IsAuthenticated() {
		// Try auto-login if the provider supports it (e.g. Teleport tsh
		// browser SSO). The call blocks until the operator finishes the
		// flow or cancels.
		if ea, ok := opts.Provider.(auth.Authenticator); ok {
			if err := ea.EnsureAuthenticated(opts.Ctx); err != nil {
				if adv, ok := opts.Provider.(auth.ReauthAdvisor); ok {
					return nil, fmt.Errorf("auth failed: %w — %s", err, adv.ReauthAdvice())
				}
				return nil, fmt.Errorf("auth failed: %w", err)
			}
		} else {
			if adv, ok := opts.Provider.(auth.ReauthAdvisor); ok {
				return nil, errors.New(adv.ReauthAdvice())
			}
			return nil, auth.ErrAuthExpired
		}
	}

	// Probe the cloud sources before writing the MCP config so a failed probe
	// disables the source rather than merely reporting it: only sources whose
	// probe is Valid are wired as MCP servers. The full set (valid and degraded)
	// stays in Result.CloudSources so the status surface still shows the
	// degraded ones with their hint. The probe degrades, never blocks.
	cloudStatuses := probeCloudSources(opts.Ctx, cloudSources(opts.Profile), opts.CloudProbe)

	mcpPath, err := writeMCPConfig(mcpConfigInputs{
		Dir:            opts.SessionDir,
		MCPBin:         opts.MCPBinaryPath,
		Namespace:      opts.Namespace,
		KubeconfigPath: kubeconfigPath,
		Profile:        opts.Profile,
		LinkedRepos:        opts.LinkedRepos,
		CloudSources:       validCloudSources(cloudSources(opts.Profile), cloudStatuses),
		GitCacheDir:        opts.GitCacheDir,
		UserPlaybooksDir:   opts.UserPlaybooksDir,
		PluginPlaybooksDir: opts.PluginPlaybooksDir,
		SystemPlaybooksDir: opts.SystemPlaybooksDir,
		WikiPath:           opts.WikiPath,
		WikiProposalsPath:  opts.WikiProposalsPath,
		SlackToken:         opts.SlackToken,
		IncidentioToken:    opts.IncidentioToken,
		TelemetryURL:       opts.TelemetryURL,
		TraceID:            opts.TraceID,
		TelemetryToken:     opts.TelemetryToken,
		PromTarget:         opts.PromTarget,
		PromDisabled:       opts.PromDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	docsPrefix := ""
	if detectDocsServer(opts.DocsServerName) {
		docsPrefix = "mcp__" + opts.DocsServerName + "__"
	}

	return &Result{
		MCPConfigPath:  mcpPath,
		DocsPrefix:     docsPrefix,
		KubeconfigPath: kubeconfigPath,
		CloudSources:   cloudStatuses,
	}, nil
}

// validCloudSources returns the subset of sources whose probe came back Valid,
// keyed by alias. A degraded source is dropped here so it is never wired as an
// MCP server, while it remains in Result.CloudSources for the status surface.
func validCloudSources(sources []profile.CloudSource, statuses []CloudSourceStatus) []profile.CloudSource {
	valid := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		valid[s.Alias] = s.Valid
	}
	out := make([]profile.CloudSource, 0, len(sources))
	for _, src := range sources {
		if valid[src.Alias] {
			out = append(out, src)
		}
	}
	return out
}

// probeCloudSources runs the identity probe for each cloud source and returns
// its per-source status. It degrades, never blocks: a failed probe marks the
// source unavailable with a hint, and the session proceeds regardless. probe
// defaults to the real prober (providers.ProbeSource) when nil.
func probeCloudSources(ctx context.Context, sources []profile.CloudSource, probe func(context.Context, profile.CloudSource) cloud.IdentityStatus) []CloudSourceStatus {
	if len(sources) == 0 {
		return nil
	}
	if probe == nil {
		probe = DefaultCloudProbe
	}
	out := make([]CloudSourceStatus, 0, len(sources))
	for _, src := range sources {
		out = append(out, CloudSourceStatus{
			Alias:          src.Alias,
			IdentityStatus: probe(ctx, src),
		})
	}
	return out
}

// DefaultCloudProbe is the real prober: it maps a profile cloud source to the
// providers package's neutral Source and runs ProbeSource, which constructs the
// provider and shells its whoami CLI. A construction error degrades to an
// invalid status, never a session-fatal error. The session preflight gate and
// the connections panel both probe through it, so the two surfaces resolve the
// same identity and can never disagree.
func DefaultCloudProbe(ctx context.Context, src profile.CloudSource) cloud.IdentityStatus {
	return providers.ProbeSource(ctx, cloudProbeSource(src))
}

// cloudProbeSource maps a profile cloud source to the providers package's
// neutral probe Source, threading the aws multi-account fields (alias, source
// profile, accounts) so a multi-account source probes its default account's
// generated profile rather than an empty AWS_PROFILE.
func cloudProbeSource(src profile.CloudSource) providers.Source {
	accounts := make([]aws.Account, 0, len(src.Accounts))
	for _, a := range src.Accounts {
		accounts = append(accounts, aws.Account{ID: a.AccountID, RoleARN: a.RoleARN})
	}
	return providers.Source{
		Provider:        src.Provider,
		AssumedIdentity: src.AssumedIdentity,
		Profile:         src.Profile,
		Alias:           src.Alias,
		SourceProfile:   src.SourceProfile,
		Accounts:        accounts,
	}
}

// cloudSources returns the profile's read-only cloud connections, or nil when
// no profile is loaded. Each becomes a triagent-cloud-<alias> MCP server.
func cloudSources(prof *profile.Profile) []profile.CloudSource {
	if prof == nil {
		return nil
	}
	return prof.Cloud
}

// freezeKubeconfig writes a session-private copy of the operator's
// kubeconfig into sessionDir and returns its path. Every MCP we spawn for
// this session receives KUBECONFIG pointing at the copy, so agent-side
// writes (e.g. triagent-teleport's login adding a context and flipping
// current-context) stay inside the session and never touch ~/.kube/config.
//
// Rehydrate is detected by sourcePath already being the frozen path: in
// that case the on-disk copy is preserved as-is, so contexts the agent
// logged into during a prior launcher run survive a restart.
func freezeKubeconfig(sessionDir, sourcePath string) (string, error) {
	if sessionDir == "" {
		return "", errors.New("session dir is required")
	}
	frozenPath := filepath.Join(sessionDir, "kubeconfig.yaml")
	if sourcePath == frozenPath {
		return frozenPath, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if sourcePath != "" {
		loadingRules.ExplicitPath = sourcePath
	}
	cfg, err := loadingRules.Load()
	if err != nil {
		return "", fmt.Errorf("load source kubeconfig: %w", err)
	}
	if err := clientcmd.WriteToFile(*cfg, frozenPath); err != nil {
		return "", fmt.Errorf("write frozen kubeconfig to %s: %w", frozenPath, err)
	}
	return frozenPath, nil
}
