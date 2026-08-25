// Package sessions wraps a single claude.Session with the orchestration
// around it: building the allowed-tools glob from the per-session MCP
// aliases, building the system prompt from the cluster context, and
// exposing claude's stream-json events on a single channel that callers
// can drain however they want (Bubbletea Cmd, SSE handler, …).
//
// The package has no UI dependencies. Both the TUI and (eventually) the
// web server drive sessions through this surface.
package sessions

import (
	"context"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/prompts"
)

// Options describes how to launch one session. They mirror the bits of
// preflight.Result and program-level Deps that affect what claude sees.
type Options struct {
	Namespace        string
	UserNotes        string
	IncidentURL      string // operator-supplied incident.io incident URL; empty when not set
	SlackChannelURL  string // operator-supplied slack channel/thread URL; empty when not set
	SlackChannelID   string
	SlackChannelName string
	MCPConfigPath    string
	SlackMCPAvailable      bool // triagent-slack MCP wired (slack token linked)
	IncidentioMCPAvailable bool // triagent-incidentio MCP wired (incidentio token linked)
	LinkedRepos            []repos.LinkedRepo
	Profile                *profile.Profile // supplies system.md / architecture.md / strategies.md + playbook IDs

	// LaunchCwd, when non-empty, is the cwd we launch claude from.
	// Defaults to the operator's terminal cwd; rehydrate sets it to
	// the investigation's SessionDir so claude can find its prior
	// on-disk session by --resume <id>.
	LaunchCwd string

	// KubeconfigPath, when non-empty, is exported as KUBECONFIG in
	// every claude / triagent-mcp / kubectl subprocess. Pinned so the
	// operator's shell-level KUBECONFIG/KUBE_CONTEXT changes don't
	// leak into us.
	KubeconfigPath string

	// AlertKind is the canonical alert title (when known from the source MCP).
	AlertKind string
	// ProjectID is the deployment / tenant identifier (when known).
	ProjectID string
	// Cluster is the kube-context / cluster name (when known).
	Cluster string

	// Playbook is the playbook id the operator selected at session
	// start. When set, the session walks that playbook instead of the
	// profile's guided investigation flow. Empty = profile defaults.
	Playbook string
}

// promptEnv projects the options into the prompt environment Build
// renders into the opening system prompt.
func (o Options) promptEnv() prompts.Env {
	clusterID := clusterIDFromNamespace(o.Namespace)
	return prompts.Env{
		UserNotes: o.UserNotes,
		InputValues: map[string]map[string]any{
			"cluster_id":    {"value": clusterID},
			"incident_url":  {"value": o.IncidentURL},
			"slack_channel": {"id": o.SlackChannelID, "name": o.SlackChannelName, "url": o.SlackChannelURL},
			"notes":         {"value": o.UserNotes},
		},
		SlackMCPAvailable:      o.SlackMCPAvailable,
		IncidentioMCPAvailable: o.IncidentioMCPAvailable,
		LinkedRepos:            o.LinkedRepos,
		Playbook:               o.Playbook,
	}
}

// Session is a thin wrapper around claude.Session with prompt + allowed-tools
// already configured.
type Session struct {
	inner *claude.Session
	opts  Options
}

// childEnv returns the env-overrides applied to every claude/triagent-mcp
// subprocess for this session. Empty entries are dropped so child
// inherit-from-launcher behaviour kicks in for unset values.
func (o Options) childEnv() []string {
	var out []string
	if o.KubeconfigPath != "" {
		out = append(out, "KUBECONFIG="+o.KubeconfigPath)
	}
	return out
}

// alertPayloadFields extracts alert-payload fields from the operator's notes
// for namespace_derivation. The minimal v1 implementation reads three keys
// the launcher already parses upstream — alert_kind, project_id, cluster.
// Empty when notes are unstructured. Future signal MCPs can grow this map.
func (o Options) alertPayloadFields() map[string]string {
	out := map[string]string{}
	if o.AlertKind != "" {
		out["alert_kind"] = o.AlertKind
	}
	if o.ProjectID != "" {
		out["project_id"] = o.ProjectID
	}
	if o.Cluster != "" {
		out["cluster"] = o.Cluster
	}
	return out
}

// systemPromptAddendum is appended to the agent's system prompt via
// --append-system-prompt. The agent's --allowedTools whitelist excludes
// Claude Code's built-in planning tools (TodoWrite et al.), but its
// training-time system prompt still tells it to use them for multi-step
// work — so on a multi-step terminal_advice (e.g. wiki → playbook →
// codefix) the agent rationalises why it isn't creating a todo list and
// leaks "Walker continues to track progress; skipping TodoWrite…" into
// the operator's chat. This addendum pre-empts that rationalisation.
const systemPromptAddendum = `The triagent-strategies walker is your task tracker. You do not have TodoWrite or any other Claude Code planning tool — they are not in your --allowedTools whitelist. Do not reference them in your replies. When a step's terminal_advice lists follow-on steps (e.g. "run wiki, then playbook, then codefix"), execute them in order without announcing the decision to skip a planning tool.`

// k8sAuthTeleportGuidance steers a Teleport deployment: clusters are
// provisioned on demand via the triagent-teleport MCP, which is only wired
// when the profile's auth.kind is "teleport".
const k8sAuthTeleportGuidance = `Kubernetes auth: triagent-k8s tools require an active context. Before your FIRST triagent-k8s.* call this session — including list_resource_kinds, get_resource, list_resources, get_logs, list_events — establish a context: call triagent-teleport.list_clusters (filter by the alert's cluster name), pick the matching entry, then call triagent-teleport.login if CurrentlyLoggedIn is false, and finally triagent-k8s.switch_context with the KubeContext value the list_clusters response gave you. Do this proactively; do not let the first k8s call fail and then react. After that initial setup any triagent-k8s call works until you switch_context to another cluster.`

// k8sAuthKubeconfigGuidance steers a kubeconfig deployment: the contexts
// already live in the kubeconfig, so there is no Teleport step — naming the
// Teleport MCP here would send the agent after tools that aren't wired.
const k8sAuthKubeconfigGuidance = `Kubernetes auth: triagent-k8s tools require an active context. Before your FIRST triagent-k8s.* call this session — including list_resource_kinds, get_resource, list_resources, get_logs, list_events — bind one: call triagent-k8s.list_contexts, pick the context matching the alert's cluster, and call triagent-k8s.switch_context with its name. Do this proactively; do not let the first k8s call fail and then react. After that initial setup any triagent-k8s call works until you switch_context to another cluster.`

// k8sAuthGuidance returns the auth recipe that matches the deployment's
// auth.kind. Anything that is not an explicit teleport profile gets the
// kubeconfig recipe, since that is the path whose tools are wired.
func k8sAuthGuidance(prof *profile.Profile) string {
	if prof != nil && prof.Auth.Kind == "teleport" {
		return k8sAuthTeleportGuidance
	}
	return k8sAuthKubeconfigGuidance
}

// renderSystemPromptAddendum appends namespace hints from the profile to the
// base addendum. Empty / no-match cases return the base unchanged. The
// rendered line uses a compact format that the investigation agent reads as
// a starting point — when present, the agent should skip list_namespaces
// in the common case.
func renderSystemPromptAddendum(base string, cfg profile.NamespaceDerivation, fields map[string]string) string {
	hints := profile.RenderNamespaceHints(cfg, fields)
	if len(hints) == 0 {
		return base
	}
	return base + "\n\nSuggested namespace(s): " + strings.Join(hints, ", ")
}

// New constructs a Session but does not start claude. Call Start to launch
// the conversation. Splitting construction from Start lets the caller emit
// "starting…" UI before the (slower) process spawn happens.
func New(opts Options) (*Session, error) {
	namespaceCfg := profile.NamespaceDerivation{}
	if opts.Profile != nil {
		namespaceCfg = opts.Profile.NamespaceDerivation
	}
	var investigationModel string
	if opts.Profile != nil {
		investigationModel = opts.Profile.Models.Investigation
	}
	inner, err := claude.NewSession(
		opts.MCPConfigPath,
		allowedTools(opts.Profile, opts.LinkedRepos, opts.SlackMCPAvailable, opts.IncidentioMCPAvailable),
		claude.SessionOpts{
			Cwd:                opts.LaunchCwd,
			Env:                opts.childEnv(),
			AppendSystemPrompt: renderSystemPromptAddendum(systemPromptAddendum+"\n\n"+k8sAuthGuidance(opts.Profile), namespaceCfg, opts.alertPayloadFields()),
			Model:              investigationModel,
		},
	)
	if err != nil {
		return nil, err
	}
	return &Session{inner: inner, opts: opts}, nil
}

// RestoreSessionID forwards to the underlying claude.Session. Used by
// the rehydrate path so the next call uses --resume <id> instead of
// starting a fresh conversation.
func (s *Session) RestoreSessionID(id string) {
	s.inner.RestoreSessionID(id)
}

// clusterIDFromNamespace extracts the cluster ID from a workload
// namespace following the convention "<clusterID>-zeebe". Returns "" if
// the namespace doesn't carry the suffix.
func clusterIDFromNamespace(ns string) string {
	const suffix = "-zeebe"
	if strings.HasSuffix(ns, suffix) && len(ns) > len(suffix) {
		return strings.TrimSuffix(ns, suffix)
	}
	return ""
}

// Start launches a fresh claude conversation with the system prompt built
// from Options. Events are delivered on the returned channel; the channel
// closes when claude exits or ctx is cancelled.
func (s *Session) Start(ctx context.Context) (<-chan claude.Event, error) {
	prompt := prompts.Build(s.opts.promptEnv(), s.opts.Profile)
	return s.inner.Start(ctx, prompt)
}

// Resume continues the most-recent conversation with a follow-up prompt.
// Must be called after Start has captured a session id.
func (s *Session) Resume(ctx context.Context, prompt string) (<-chan claude.Event, error) {
	if s.inner.SessionID() == "" {
		return nil, fmt.Errorf("no prior session to resume")
	}
	return s.inner.Resume(ctx, prompt)
}

// ID returns the captured claude session id, or empty until Start has
// produced its first system event.
func (s *Session) ID() string { return s.inner.SessionID() }

// allowedTools is the glob list passed to `claude --allowedTools`. It mirrors
// the MCP aliases written by preflight.writeMCPConfig and adds the extra MCP
// servers from the profile + per-repo git servers + optional slack / incidentio when wired.
// Order doesn't matter — claude unions the patterns.
func allowedTools(prof *profile.Profile, linkedRepos []repos.LinkedRepo, slackAvail, incidentioAvail bool) []string {
	out := []string{
		"mcp__" + preflight.MCPAliasK8s + "__*",
		"mcp__" + preflight.MCPAliasStrategies + "__*",
		"mcp__" + preflight.MCPAliasMeta + "__*",
		"mcp__" + preflight.MCPAliasParallel + "__*",
		// triagent-wiki is wired whenever WikiPath/WikiProposalsPath are
		// set, which is the standard launcher configuration. Including it
		// here silences the per-call approval prompts. Tools from non-wired
		// MCPs simply won't be reachable at execution time.
		"mcp__" + preflight.MCPAliasWiki + "__*",
	}
	// triagent-teleport is wired only for a teleport deployment (mcpconfig.go
	// gates it on auth.kind); allowlist it on the same condition so a
	// kubeconfig deployment doesn't carry a glob for tools that never exist.
	if prof != nil && prof.Auth.Kind == "teleport" {
		out = append(out, "mcp__"+preflight.MCPAliasTeleport+"__*")
	}
	if prof != nil {
		for _, m := range prof.ExtraMCPs {
			if len(m.AllowedTools) > 0 {
				out = append(out, m.AllowedTools...)
			} else {
				out = append(out, "mcp__"+m.Alias+"__*")
			}
		}
	}
	for _, r := range linkedRepos {
		out = append(out, "mcp__"+preflight.MCPAliasGitPrefix+r.EffectiveAlias()+"__*")
	}
	if slackAvail {
		out = append(out, "mcp__"+preflight.MCPAliasSlack+"__*")
	}
	if incidentioAvail {
		out = append(out, "mcp__"+preflight.MCPAliasIncidentio+"__*")
	}
	return out
}
