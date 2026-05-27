package preflight

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sourcehawk/triagent/internal/repos"
)

// EditorMCPInputs collects the bits WriteEditorMCPConfig needs. The
// editor surface is narrower than a full investigation — no k8s, no
// prom (those need a live cluster) — but DOES include the strategies
// MCP plus the launcher's linked-repo triagent-git servers, since
// understanding what a controller actually does is exactly the
// research that informs what failure modes a playbook should cover.
// Callers thread telemetry the same way the full preflight does so
// tool-event telemetry routes back to the launcher.
type EditorMCPInputs struct {
	Dir                string // writable per-session directory
	MCPBin             string
	UserPlaybooksDir   string
	PluginPlaybooksDir string
	SystemPlaybooksDir string
	// LinkedRepos is the resolved list of GitHub repos to spawn `triagent-mcp
	// serve --kind=git` instances for. Mirrors the investigation flow's
	// shape so the same docs/operator/zeebe MCP servers are available
	// to the editor agent.
	LinkedRepos []repos.LinkedRepo
	GitCacheDir string
	// WikiPath / WikiProposalsPath, when both set, register the wiki
	// MCP server. Required for wiki-subject editor sessions; safe to
	// leave empty for playbook-subject sessions.
	WikiPath          string
	WikiProposalsPath string
	// Token-only wiring: when SlackToken / IncidentioToken is non-empty
	// the corresponding `triagent-slack` / `triagent-incidentio` MCP server is
	// registered. Channel/incident scope is conveyed to the agent via
	// the system prompt and threaded into per-tool-call args.
	SlackToken      string
	IncidentioToken string
	TelemetryURL    string
	TraceID         string // editor session id; same env as investigation telemetry
	TelemetryToken  string
}

// WriteEditorMCPConfig emits a per-editor-session mcp.json with the
// strategies MCP + every linked-repo triagent-git MCP. Returns the path
// written. Mirrors writeMCPConfig but skips k8s and prom: the editor
// agent has no cluster context, but it CAN read the controller / SDK
// code that drives the failure modes the playbook documents.
func WriteEditorMCPConfig(in EditorMCPInputs) (string, error) {
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
	tel := mcpConfigInputs{
		TelemetryURL:   in.TelemetryURL,
		TraceID:        in.TraceID,
		TelemetryToken: in.TelemetryToken,
	}
	mergeEnv(strategiesEnv, telemetryEnv(tel, MCPAliasStrategies))

	servers := map[string]any{
		MCPAliasStrategies: map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=strategies"},
			"env":     strategiesEnv,
		},
	}

	for _, repo := range in.LinkedRepos {
		alias := MCPAliasGitPrefix + repo.EffectiveAlias()
		gitEnv := map[string]string{
			EnvGitRepo: repo.Owner + "/" + repo.Name,
		}
		if in.GitCacheDir != "" {
			gitEnv[EnvGitCacheDir] = in.GitCacheDir
		}
		mergeEnv(gitEnv, telemetryEnv(tel, alias))
		servers[alias] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=git", "--repo", repo.Owner + "/" + repo.Name},
			"env":     gitEnv,
		}
	}

	if in.WikiPath != "" && in.WikiProposalsPath != "" {
		wikiEnv := map[string]string{
			EnvWikiPath:          in.WikiPath,
			EnvWikiProposalsPath: in.WikiProposalsPath,
		}
		mergeEnv(wikiEnv, telemetryEnv(tel, MCPAliasWiki))
		servers[MCPAliasWiki] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=wiki"},
			"env":     wikiEnv,
		}
	}

	if in.SlackToken != "" {
		slackEnv := map[string]string{
			EnvSlackToken: in.SlackToken,
		}
		mergeEnv(slackEnv, telemetryEnv(tel, MCPAliasSlack))
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
		mergeEnv(ioEnv, telemetryEnv(tel, MCPAliasIncidentio))
		servers[MCPAliasIncidentio] = map[string]any{
			"command": in.MCPBin,
			"args":    []string{"serve", "--kind=incidentio"},
			"env":     ioEnv,
		}
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
