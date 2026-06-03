package preflight

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/promforward"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/gcp"
)

var envRefRe = regexp.MustCompile(`^\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}$`)

// interpolateEnv replaces a value of the form ${env:NAME} with the value
// of NAME from the process env. Returns an error if NAME is unset.
// Non-matching values pass through verbatim.
func interpolateEnv(alias, key, value string) (string, error) {
	m := envRefRe.FindStringSubmatch(value)
	if m == nil {
		return value, nil
	}
	v, ok := os.LookupEnv(m[1])
	if !ok {
		return "", fmt.Errorf("extra_mcp alias=%q env %q: missing required env var %s", alias, key, m[1])
	}
	return v, nil
}

// MCP server aliases registered in the per-session MCP config. They become
// part of the tool name as `mcp__<alias>__<tool>`, so the launcher's
// allowed-tools globs and the strategies playbooks reference these strings.
const (
	MCPAliasK8s        = "triagent-k8s"
	MCPAliasMeta       = "triagent-meta"
	MCPAliasStrategies = "triagent-strategies"
	MCPAliasTeleport   = "triagent-teleport"
	MCPAliasWiki       = "triagent-wiki"
	MCPAliasSlack      = "triagent-slack"
	MCPAliasIncidentio = "triagent-incidentio"
	MCPAliasParallel   = "triagent-parallel"
	MCPAliasProm       = "triagent-prom"
	// MCPAliasGitPrefix is prepended to a repo's effective alias to form
	// the per-repo MCP server alias (e.g. "triagent-git-zeebe").
	MCPAliasGitPrefix = "triagent-git-"
	// MCPAliasCloudPrefix is prepended to a cloud source's alias to form the
	// per-source MCP server alias (e.g. "triagent-cloud-prod-gcp"), mirroring
	// the per-repo git prefix.
	MCPAliasCloudPrefix = "triagent-cloud-"
)

// Env-var names the launcher injects into each triagent-mcp subcommand. These
// MUST match the names in cmd/triagent-mcp/serve.go and (for telemetry) in
// pkg/mcp/telemetry.
const (
	EnvCRDsFile         = "TRIAGENT_MCP_CRDS_FILE"
	EnvCrossplaneGroups = "TRIAGENT_MCP_CROSSPLANE_GROUPS"
	EnvSessionDir                 = "TRIAGENT_MCP_SESSION_DIR"
	EnvUserPlaybooksDir            = "TRIAGENT_MCP_USER_PLAYBOOKS_DIR"
	EnvPluginPlaybooksDir          = "TRIAGENT_MCP_PLUGIN_PLAYBOOKS_DIR"
	EnvSystemPlaybooksDir          = "TRIAGENT_MCP_SYSTEM_PLAYBOOKS_DIR"
	EnvStrategiesSubagentModel     = "TRIAGENT_MCP_STRATEGIES_SUBAGENT_MODEL"
	EnvGitRepo          = "TRIAGENT_MCP_GIT_REPO"
	EnvGitCacheDir      = "TRIAGENT_MCP_GIT_CACHE_DIR"

	EnvWikiPath          = "TRIAGENT_MCP_WIKI_PATH"
	EnvWikiProposalsPath = "TRIAGENT_MCP_WIKI_PROPOSALS_PATH"

	EnvSlackToken      = "TRIAGENT_MCP_SLACK_TOKEN"
	EnvIncidentioToken = "TRIAGENT_MCP_INCIDENTIO_TOKEN"

	EnvPromResolverURL = "TRIAGENT_MCP_PROM_RESOLVER_URL"
	EnvPromBearer      = "TRIAGENT_MCP_PROM_BEARER"
	EnvPromBasic       = "TRIAGENT_MCP_PROM_BASIC"

	EnvParallelUpstreams = "TRIAGENT_MCP_PARALLEL_UPSTREAMS"

	EnvTeleportProxy         = "TRIAGENT_MCP_TELEPORT_PROXY"
	EnvTeleportAuthConnector = "TRIAGENT_MCP_TELEPORT_AUTH_CONNECTOR"

	EnvKubeconfig = "KUBECONFIG"

	EnvTelemetryURL        = "TRIAGENT_MCP_TELEMETRY_URL"
	EnvTraceID             = "TRIAGENT_MCP_TRACE_ID"
	EnvTelemetryToken      = "TRIAGENT_MCP_TELEMETRY_TOKEN"
	EnvTelemetryToolPrefix = "TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX"

	// EnvMCPConfigPath points the strategies MCP at the launcher's
	// per-session mcp.json so its dispatch handler can forward that path
	// to subagent.Run. Dispatched playbooks (wiki_proposal,
	// playbook_proposal) need to call back into parent MCPs
	// (mcp__triagent-strategies__*, mcp__triagent-wiki__*); without this
	// the sub-agent gets the subagent package's empty-isolation default
	// and reports those tools as unreachable.
	EnvMCPConfigPath = "TRIAGENT_MCP_CONFIG_PATH"
)

type mcpConfigInputs struct {
	Dir       string
	MCPBin    string
	Namespace string
	// Profile is the loaded org profile for this session. When set and the
	// profile carries a k8s/kinds.json, it is extracted to the session dir
	// and passed to triagent-mcp as --crds-file. TRIAGENT_MCP_CRDS_FILE env wins.
	Profile *profile.Profile
	LinkedRepos      []repos.LinkedRepo // each becomes a `triagent-git-<alias>` server entry
	// CloudSources are the profile's read-only cloud connections; each becomes a
	// `triagent-cloud-<alias>` server entry pinned to that source's identity.
	CloudSources       []profile.CloudSource
	GitCacheDir        string             // optional override for git repo cache root
	CloudCacheDir      string             // per-profile dir for the triagent-owned AWS config (<dir>/config)
	UserPlaybooksDir   string             // optional override-or-extend dir for strategies playbooks
	PluginPlaybooksDir string             // launcher-managed clone of the upstream playbooks repo (overridable)
	SystemPlaybooksDir string             // launcher-bundled meta-playbooks (locked, non-overridable)
	WikiPath          string             // launcher-managed clone of the upstream wiki repo
	WikiProposalsPath string             // local proposals dir for wiki drafts
	KubeconfigPath string // pinned KUBECONFIG path, frozen at preflight time
	// Token-only wiring: when SlackToken is non-empty the triagent-slack MCP
	// server is registered; same for IncidentioToken / triagent-incidentio.
	// Channel and incident scope are passed to the agent through the
	// system prompt and threaded into per-tool-call args, not through
	// MCP boot args.
	SlackToken      string
	IncidentioToken string
	TelemetryURL     string             // empty disables telemetry across the board
	TraceID          string             // opaque correlation key (this launcher's investigation id)
	TelemetryToken   string
	// PromTarget is the per-investigation resolved Prometheus target. When
	// non-nil (and PromDisabled is false), the prom MCP server entry is
	// emitted. Replaces the old profile-level gate so each investigation
	// carries its own target.
	PromTarget   *promforward.Target
	// PromDisabled explicitly suppresses the prom MCP even when PromTarget
	// is non-nil (operator checked "disable prom MCP" in the form).
	PromDisabled bool
}

// telemetryEnv returns the telemetry env vars to inject into a single
// triagent-mcp serve env block. Returns an empty map when telemetry isn't
// configured. The tool-prefix matches the alias used as the MCP server
// key in mcp.json so the launcher can attribute calls to the right
// server when reporting tool names.
func telemetryEnv(in mcpConfigInputs, alias string) map[string]string {
	if in.TelemetryURL == "" {
		return nil
	}
	out := map[string]string{
		EnvTelemetryURL:        in.TelemetryURL,
		EnvTraceID:             in.TraceID,
		EnvTelemetryToolPrefix: "mcp__" + alias + "__",
	}
	if in.TelemetryToken != "" {
		out[EnvTelemetryToken] = in.TelemetryToken
	}
	return out
}

// mergeEnv copies src into dst (dst wins on collision) and returns dst.
func mergeEnv(dst, src map[string]string) map[string]string {
	for k, v := range src {
		if _, ok := dst[k]; !ok {
			dst[k] = v
		}
	}
	return dst
}

// kubeEnv returns the always-on kube isolation env for every triagent-mcp
// server in this session: KUBECONFIG (frozen at preflight). Returned as
// a fresh map so callers can mergeEnv it without aliasing.
func kubeEnv(in mcpConfigInputs) map[string]string {
	out := map[string]string{}
	if in.KubeconfigPath != "" {
		out[EnvKubeconfig] = in.KubeconfigPath
	}
	return out
}

// cloudSourceEnv builds the subprocess env for one triagent-cloud-<alias>
// server: the provider selector, the optional allowlist-override path, the
// JSON-encoded scope the cloud package decodes, the pinned identity the probe
// validates against, and the per-provider credential env the CLI authenticates
// with.
//
// The pinned identity is uniform: TRIAGENT_CLOUD_EXPECTED_IDENTITY carries it
// for both clouds, and the probe validates the resolved identity against it. The
// credential env differs by mechanism: GCP impersonates the assumed identity
// directly (CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT). AWS carries its accounts
// and source_profile (TRIAGENT_CLOUD_AWS_ACCOUNTS, _SOURCE_PROFILE): the
// subprocess generates an assume-role profile per account and pins AWS_PROFILE
// per run_cli from the active target, so no static profile selector belongs in
// this env. The env-name constants come from the provider packages, never raw
// literals.
func cloudSourceEnv(src profile.CloudSource, cloudCacheDir string) (map[string]string, error) {
	env := map[string]string{
		cloud.EnvProvider: src.Provider,
	}
	// The expected identity is provider-specific: gcp's impersonated service
	// account, or aws's default (first) account's role_arn — the server validates
	// each active aws account against its own role, and falls back to this default
	// only before a target is chosen.
	if src.Provider == "aws" {
		if len(src.Accounts) > 0 {
			env[cloud.EnvExpectedIdentity] = src.Accounts[0].RoleARN
		}
	} else {
		env[cloud.EnvExpectedIdentity] = src.AssumedIdentity
	}
	if src.CommandAllowlistPath != "" {
		env[cloud.EnvAllowlistPath] = src.CommandAllowlistPath
	}
	scopeRaw, err := json.Marshal(src.Scope)
	if err != nil {
		return nil, fmt.Errorf("cloud source %q: encode scope: %w", src.Alias, err)
	}
	env[cloud.EnvScope] = string(scopeRaw)

	switch src.Provider {
	case "gcp":
		env[gcp.EnvImpersonate] = src.AssumedIdentity
		if len(src.Projects) > 0 {
			projectsRaw, err := json.Marshal(src.Projects)
			if err != nil {
				return nil, fmt.Errorf("cloud source %q: encode projects: %w", src.Alias, err)
			}
			env[cloud.EnvGCPProjects] = string(projectsRaw)
		}
	case "aws":
		accountsRaw, err := json.Marshal(src.Accounts)
		if err != nil {
			return nil, fmt.Errorf("cloud source %q: encode accounts: %w", src.Alias, err)
		}
		env[cloud.EnvAWSAccounts] = string(accountsRaw)
		env[cloud.EnvAWSSourceProfile] = src.SourceProfile
		env[cloud.EnvAWSAlias] = src.Alias
		// Point the subprocess at the triagent-owned config (the aws CLI reads
		// it, the provider generates into it) and name the operator config to
		// copy from, so ~/.aws/config is never written.
		if target := awsManagedConfigPath(cloudCacheDir); target != "" {
			env[cloud.EnvAWSConfigFile] = target
			env[cloud.EnvAWSSourceConfig] = operatorAWSConfigPath()
		}
	}
	return env, nil
}

// awsManagedConfigPath is the triagent-owned AWS config file for a profile:
// <CloudCacheDir>/config. Empty when no cache dir is configured (the provider
// then declines to generate rather than writing a surprising path).
func awsManagedConfigPath(cloudCacheDir string) string {
	if cloudCacheDir == "" {
		return ""
	}
	return filepath.Join(cloudCacheDir, "config")
}

// operatorAWSConfigPath is the operator's own AWS config the managed file copies
// from: $AWS_CONFIG_FILE when the operator set one in the launcher's env, else
// $HOME/.aws/config.
func operatorAWSConfigPath() string {
	if v := os.Getenv(cloud.EnvAWSConfigFile); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".aws", "config")
}

// resolveKindsFile returns the --crds-file path to pass to triagent-mcp's k8s
// server, or "" when no override is in effect. Precedence:
//  1. TRIAGENT_MCP_CRDS_FILE env (operator-direct override).
//  2. Embedded profile kinds.json — extracted to sessionDir so triagent-mcp can
//     read it as a plain file.
//  3. Filesystem-profile kinds.json path — passed directly.
//  4. Absent — empty string; triagent-mcp falls back to its trimmed baseline.
func resolveKindsFile(prof *profile.Profile, sessionDir string) (string, error) {
	if env := os.Getenv(EnvCRDsFile); env != "" {
		return env, nil
	}
	if prof == nil {
		return "", nil
	}
	if strings.HasPrefix(prof.KindsPath, "embedded:") {
		data, err := profile.EmbeddedKinds(prof.Name)
		if err != nil || data == nil {
			return "", nil
		}
		dst := filepath.Join(sessionDir, "kinds.json")
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return "", fmt.Errorf("extract embedded kinds: %w", err)
		}
		return dst, nil
	}
	if prof.KindsPath != "" {
		return prof.KindsPath, nil
	}
	return "", nil
}

// writeMCPConfig emits a per-session MCP config that registers the triagent-mcp
// servers: k8s, strategies, wiki (when both WikiPath and WikiProposalsPath
// are non-empty), and one git server per linked repo — all backed by the
// triagent-mcp binary via `serve --kind=...`. Each server gets its own env-var
// scope so the dispatcher can pick its config without flag wiring.
func writeMCPConfig(in mcpConfigInputs) (string, error) {
	k8sEnv := map[string]string{}
	for _, passthrough := range []string{EnvCrossplaneGroups} {
		if v := os.Getenv(passthrough); v != "" {
			k8sEnv[passthrough] = v
		}
	}
	k8sEnv[EnvSessionDir] = in.Dir
	mergeEnv(k8sEnv, telemetryEnv(in, MCPAliasK8s))
	mergeEnv(k8sEnv, kubeEnv(in))

	kindsFile, err := resolveKindsFile(in.Profile, in.Dir)
	if err != nil {
		return "", fmt.Errorf("resolve kinds file: %w", err)
	}

	strategiesEnv := map[string]string{
		EnvSessionDir: in.Dir,
	}
	if in.UserPlaybooksDir != "" {
		strategiesEnv[EnvUserPlaybooksDir] = in.UserPlaybooksDir
	}
	if in.PluginPlaybooksDir != "" {
		strategiesEnv[EnvPluginPlaybooksDir] = in.PluginPlaybooksDir
	}
	if in.SystemPlaybooksDir != "" {
		strategiesEnv[EnvSystemPlaybooksDir] = in.SystemPlaybooksDir
	}
	if in.Profile != nil && in.Profile.Models.Subagent != "" {
		strategiesEnv[EnvStrategiesSubagentModel] = in.Profile.Models.Subagent
	}
	// Point the strategies MCP at the mcp.json this function is about to
	// write so its dispatch handler can forward the path to subagent.Run.
	// Kept in sync with the path constructed below at write time.
	strategiesEnv[EnvMCPConfigPath] = filepath.Join(in.Dir, "mcp.json")
	mergeEnv(strategiesEnv, telemetryEnv(in, MCPAliasStrategies))
	mergeEnv(strategiesEnv, kubeEnv(in))

	// meta MCP — always wired. Has no per-kind config; the telemetry
	// env handshake is what lets it reach the launcher's internal
	// label endpoint, so the only env we need is what telemetryEnv()
	// returns.
	metaEnv := map[string]string{}
	mergeEnv(metaEnv, telemetryEnv(in, MCPAliasMeta))
	mergeEnv(metaEnv, kubeEnv(in))

	k8sArgs := []string{"serve", "--kind=k8s"}
	if kindsFile != "" {
		k8sArgs = append(k8sArgs, "--crds-file="+kindsFile)
	}

	servers := map[string]any{
		MCPAliasK8s: map[string]any{
			"command": in.MCPBin,
			"args":    k8sArgs,
			"env":     k8sEnv,
		},
		MCPAliasMeta: map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=meta"},
			"env":     metaEnv,
		},
		MCPAliasStrategies: map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=strategies"},
			"env":     strategiesEnv,
		},
	}

	// Teleport tooling (list_clusters, login) only makes sense for a
	// teleport deployment. On a kubeconfig deployment those tools have
	// nothing to act on, and exposing them lures the agent into a tsh
	// login loop instead of reaching for the k8s MCP's switch_context.
	if in.Profile != nil && in.Profile.Auth.Kind == "teleport" {
		teleportEnv := map[string]string{}
		mergeEnv(teleportEnv, telemetryEnv(in, MCPAliasTeleport))
		mergeEnv(teleportEnv, kubeEnv(in))
		// Thread the deployment's connection parameters so the subprocess
		// builds a provider that logs in against the right proxy/connector
		// instead of the SDK's empty defaults.
		if proxy := in.Profile.Auth.Teleport.Proxy; proxy != "" {
			teleportEnv[EnvTeleportProxy] = proxy
		}
		if connector := in.Profile.Auth.Teleport.AuthConnector; connector != "" {
			teleportEnv[EnvTeleportAuthConnector] = connector
		}
		servers[MCPAliasTeleport] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=teleport"},
			"env":     teleportEnv,
		}
	}

	if in.WikiPath != "" && in.WikiProposalsPath != "" {
		wikiEnv := map[string]string{
			EnvWikiPath:          in.WikiPath,
			EnvWikiProposalsPath: in.WikiProposalsPath,
		}
		mergeEnv(wikiEnv, telemetryEnv(in, MCPAliasWiki))
		mergeEnv(wikiEnv, kubeEnv(in))
		servers[MCPAliasWiki] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=wiki"},
			"env":     wikiEnv,
		}
	}

	for _, repo := range in.LinkedRepos {
		alias := MCPAliasGitPrefix + repo.EffectiveAlias()
		gitEnv := map[string]string{
			EnvGitRepo: repo.Owner + "/" + repo.Name,
		}
		if in.GitCacheDir != "" {
			gitEnv[EnvGitCacheDir] = in.GitCacheDir
		}
		mergeEnv(gitEnv, telemetryEnv(in, alias))
		mergeEnv(gitEnv, kubeEnv(in))
		servers[alias] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=git", "--repo", repo.Owner + "/" + repo.Name},
			"env":     gitEnv,
		}
	}

	for _, src := range in.CloudSources {
		alias := MCPAliasCloudPrefix + src.Alias
		cloudEnv, err := cloudSourceEnv(src, in.CloudCacheDir)
		if err != nil {
			return "", err
		}
		mergeEnv(cloudEnv, telemetryEnv(in, alias))
		mergeEnv(cloudEnv, kubeEnv(in))
		servers[alias] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=cloud", "--provider=" + src.Provider},
			"env":     cloudEnv,
		}
	}

	if in.SlackToken != "" {
		slackEnv := map[string]string{
			EnvSlackToken: in.SlackToken,
		}
		mergeEnv(slackEnv, telemetryEnv(in, MCPAliasSlack))
		mergeEnv(slackEnv, kubeEnv(in))
		servers[MCPAliasSlack] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=slack"},
			"env":     slackEnv,
		}
	}

	if in.IncidentioToken != "" {
		ioEnv := map[string]string{
			EnvIncidentioToken: in.IncidentioToken,
		}
		mergeEnv(ioEnv, telemetryEnv(in, MCPAliasIncidentio))
		mergeEnv(ioEnv, kubeEnv(in))
		servers[MCPAliasIncidentio] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=incidentio"},
			"env":     ioEnv,
		}
	}

	if !in.PromDisabled && in.PromTarget != nil && in.PromTarget.Service != "" && in.TelemetryURL != "" {
		promEnv := map[string]string{}
		if origin := telemetryOrigin(in.TelemetryURL); origin != "" {
			promEnv[EnvPromResolverURL] = origin + "/api/internal/prom/" + in.TraceID + "/endpoint"
		}
		if v := os.Getenv(EnvPromBearer); v != "" {
			promEnv[EnvPromBearer] = v
		}
		if v := os.Getenv(EnvPromBasic); v != "" {
			promEnv[EnvPromBasic] = v
		}
		mergeEnv(promEnv, telemetryEnv(in, MCPAliasProm))
		mergeEnv(promEnv, kubeEnv(in))
		servers[MCPAliasProm] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=prom"},
			"env":     promEnv,
		}
	}

	// Spawn-mode extra MCPs from the profile. These are emitted before the
	// parallel broker so they appear in the upstreams map that the broker
	// receives.
	if in.Profile != nil {
		for _, m := range in.Profile.ExtraMCPs {
			if m.Command == "" {
				continue // reference mode: no server entry
			}
			env := map[string]string{}
			for k, v := range m.Env {
				resolved, err := interpolateEnv(m.Alias, k, v)
				if err != nil {
					return "", err
				}
				env[k] = resolved
			}
			server := map[string]any{
				"command": m.Command,
				"args":    m.Args,
			}
			if len(env) > 0 {
				server["env"] = env
			}
			servers[m.Alias] = server
		}
	}

	// Parallel broker: receives a slim copy of every other server's
	// command/args/env so it can spawn its own client connections. Added
	// last so the upstreams map is complete when we marshal.
	parallelEnv := map[string]string{}
	mergeEnv(parallelEnv, telemetryEnv(in, MCPAliasParallel))
	mergeEnv(parallelEnv, kubeEnv(in))
	upstreamsRaw, err := json.Marshal(parallelUpstreams(servers))
	if err != nil {
		return "", err
	}
	parallelEnv[EnvParallelUpstreams] = string(upstreamsRaw)
	servers[MCPAliasParallel] = map[string]any{
		"command": in.MCPBin,
		"args":    []string{"serve", "--kind=parallel"},
		"env":     parallelEnv,
	}

	cfg := map[string]any{"mcpServers": servers}
	path := filepath.Join(in.Dir, "mcp.json")
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// telemetryOrigin extracts scheme://host[:port] from a launcher
// telemetry URL like "http://127.0.0.1:8080/api/internal/tool-events".
// Empty input → empty result. Used to build the prom resolver URL
// without pulling pkg/mcp/meta into preflight.
func telemetryOrigin(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// parallelUpstreams projects the assembled servers map into the slim
// shape the parallel broker reads from its EnvParallelUpstreams. We strip
// any entry whose key matches MCPAliasParallel — the broker never needs
// to call itself (defensive: in practice the broker is added after this
// call, so the strip is a no-op today).
func parallelUpstreams(servers map[string]any) map[string]any {
	out := make(map[string]any, len(servers))
	for alias, raw := range servers {
		if alias == MCPAliasParallel {
			continue
		}
		out[alias] = raw
	}
	return out
}
