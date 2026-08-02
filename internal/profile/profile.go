// Package profile is the runtime-loaded configuration unit that gives
// each org its own prompts, defaults, MCP list, and investigation
// inputs. See docs/superpowers/specs/2026-05-12-investigate-profile-abstraction-design.md.
package profile

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
)

// Profile is the in-memory shape of profile.yaml. Field tags match the
// schema in the spec; YAML uses snake_case throughout.
type Profile struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Base names an embedded profile this profile inherits from. Optional.
	// Resolved during loading; not used at runtime.
	Base string `yaml:"base,omitempty"`

	Auth                Auth                 `yaml:"auth"`
	Playbooks           Playbooks            `yaml:"playbooks"`
	Defaults            Defaults             `yaml:"defaults"`
	Paths               Paths                `yaml:"paths"`
	Slack               Slack                `yaml:"slack"`
	LinkedRepos         []LinkedRepo         `yaml:"linked_repos"`
	ExtraMCPs           []ExtraMCP           `yaml:"extra_mcps"`
	InvestigationInputs []InvestigationInput `yaml:"investigation_inputs"`
	Cloud               []CloudSource        `yaml:"cloud"`

	// PromptFiles declares prompt overrides by filename → path (relative
	// to the profile.yaml's directory). Loaded into Prompts at load time
	// after the conventional `prompts/` directory walk, so explicit
	// declarations win over same-named entries discovered by convention.
	// Use this to keep a flat profile dir instead of nesting under
	// `prompts/`.
	PromptFiles map[string]string `yaml:"prompt_files,omitempty"`

	// KindsFile is the path (relative to profile.yaml's directory) to a
	// k8s kinds.json override. Takes precedence over the conventional
	// `k8s/kinds.json` location.
	KindsFile string `yaml:"kinds_file,omitempty"`

	// NamespaceDerivation, when set, lets the launcher pre-compute namespace
	// hint(s) from the alert payload and inject them into the session's
	// system prompt at preflight, sparing the agent from calling
	// list_namespaces in the common case. See namespace_derivation.go for
	// the rendering semantics.
	NamespaceDerivation NamespaceDerivation `yaml:"namespace_derivation,omitempty"`

	// Models picks the LLM models for the main investigation session and
	// for sub-agent dispatches. Empty fields get defaults at load time —
	// Sonnet for investigation, Haiku for subagents. Per-call overrides
	// in subagent.Options.Model remain available for legitimately
	// Sonnet-grade sub-agent work (e.g. draft_pr).
	Models Models `yaml:"models,omitempty"`

	// Populated by the loader, not from YAML: the on-disk path to the
	// profile's optional k8s/kinds.json file (or "" if absent). For
	// embedded profiles, this is prefixed with `embedded:` so the
	// launcher knows to extract from the embed FS before pointing the
	// MCP at it.
	KindsPath string `yaml:"-"`

	// Populated by the loader: the prompt file contents, keyed by filename
	// (e.g. "system.md"). Empty when reading raw YAML in tests.
	Prompts map[string]string `yaml:"-"`
}

type Auth struct {
	Kind     string         `yaml:"kind"`
	Teleport TeleportConfig `yaml:"teleport,omitempty"`
}

type TeleportConfig struct {
	Proxy         string `yaml:"proxy"`
	AuthConnector string `yaml:"auth_connector"`
}

type Playbooks struct {
	Entrypoint string `yaml:"entrypoint"`
	Closing    string `yaml:"closing"`
}

type Defaults struct {
	PlaybooksRepo string `yaml:"playbooks_repo"`
	// PlaybooksPath is the subdir inside the upstream playbooks repo
	// that holds the `<type>/<id>.yaml` tree. Empty means the repo root
	// (legacy layout). The default profile ships "playbooks" so a
	// single shared upstream repo can host playbooks alongside wikis
	// and sessions under distinct top-level subdirs.
	PlaybooksPath string `yaml:"playbooks_path"`
	WikiRepo      string `yaml:"wiki_repo"`
	// WikiPath is the subdir inside the upstream wiki repo that holds
	// the `entries/` + `entities/` tree. Empty means the repo root
	// (legacy layout). Default profile ships "wikis".
	WikiPath     string `yaml:"wiki_path"`
	SessionsRepo string `yaml:"sessions_repo"`
	// SessionsPath is the subdir inside the upstream sessions repo
	// that holds per-investigation `<slug>/session.md` dirs. Empty
	// means the repo root (legacy layout). Default profile ships
	// "sessions".
	SessionsPath string     `yaml:"sessions_path"`
	Prometheus   Prometheus `yaml:"prometheus"`

	// Auto, when true, makes new investigations default to auto mode
	// (the operator-agent drives the chat). The per-investigation toggle
	// in the preflight form still wins.
	Auto bool `yaml:"auto"`

	// Offline, when true, disables boot-time `git clone` of the upstream
	// playbooks/wiki/sessions repos. Existing checkouts still load;
	// missing-or-empty dirs fail fast with a clear error so the
	// operator can pre-seed them manually. Use in air-gapped envs.
	Offline bool `yaml:"offline"`
}

type Prometheus struct {
	Service   string `yaml:"service"`
	Namespace string `yaml:"namespace"`
	Port      int    `yaml:"port"`
}

type Slack struct {
	ChannelPrefix string `yaml:"channel_prefix"`
}

type Models struct {
	Investigation string `yaml:"investigation,omitempty"`
	Subagent      string `yaml:"subagent,omitempty"`
}

type LinkedRepo struct {
	Owner       string `yaml:"owner"`
	Name        string `yaml:"name"`
	Alias       string `yaml:"alias"`
	Description string `yaml:"description"`
}

type ExtraMCP struct {
	Alias        string            `yaml:"alias"`
	Description  string            `yaml:"description"`
	Command      string            `yaml:"command,omitempty"`
	Args         []string          `yaml:"args,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	AllowedTools []string          `yaml:"allowed_tools,omitempty"`
}

// CloudSource is a deployment-configured, read-only cloud connection the
// launcher wires per session as a triagent-cloud-<alias> MCP server. It is
// configured in the profile, never entered in the connections panel: the agent
// can read the pinned identity but cannot select or escalate it.
//
// The pinned identity is provider-specific. GCP spans its projects with one
// impersonated service account (AssumedIdentity, shown in the connections
// panel), selecting among them by Scope.Projects. AWS has no single assumed
// identity: it pins a list of accounts, each {account_id, role_arn}, and
// triagent generates a per-account assume-role profile layering each role over
// SourceProfile; the agent selects among them via set_active_target, and each
// account validates against its own role_arn. A single-account AWS source is
// simply a one-entry Accounts list — there is no separate single-account shape.
type CloudSource struct {
	Alias    string `yaml:"alias"`
	Provider string `yaml:"provider"` // "gcp" | "aws"
	// AssumedIdentity is the gcp impersonated service-account email. Required for
	// gcp; must be empty for aws (whose identity is per-account, in Accounts).
	AssumedIdentity string `yaml:"assumed_identity,omitempty"`
	// SourceProfile is the operator's SSO base profile the generated per-account
	// assume-role profiles layer their role_arn over. Required for aws.
	SourceProfile string `yaml:"source_profile,omitempty"`
	// Accounts is the aws account set, each entry a {account_id, role_arn, tags}
	// that becomes a generated assume-role profile the agent may make active.
	// Required for aws (a single-account source is a one-entry list); unused by gcp.
	Accounts []CloudAccount `yaml:"accounts,omitempty"`
	// Projects is the gcp selectable project set, each a {id, tags}. Optional:
	// when empty the agent selects among the projects list_inventory surfaces
	// live. Unused by aws.
	Projects []CloudProject       `yaml:"projects,omitempty"`
	Scope    cloud.ScopeAllowlist `yaml:"scope,omitempty"`
	// CommandAllowlistPath points the cloud MCP at a run_cli allowlist override
	// file; empty uses the provider's embedded default. A relative path resolves
	// against the profile.yaml's directory at load time (absolutized so the MCP
	// subprocess can read it from any cwd).
	CommandAllowlistPath string `yaml:"command_allowlist_path,omitempty"`
}

// CloudAccount is one aws account in a cloud source: the account id the agent
// selects by, the read-only role_arn triagent assumes into it from the source's
// SourceProfile, and the deployment's free-form tags surfaced by list_inventory
// so the agent can judge which account an investigation belongs to.
type CloudAccount struct {
	AccountID string   `yaml:"account_id" json:"account_id"`
	RoleARN   string   `yaml:"role_arn" json:"role_arn"`
	Tags      []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// CloudProject is one gcp project the agent may select: the project id and the
// deployment's free-form tags surfaced by list_inventory.
type CloudProject struct {
	ID   string   `yaml:"id" json:"id"`
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

type InvestigationInput struct {
	ID          string      `yaml:"id"`
	Label       string      `yaml:"label"`
	Type        string      `yaml:"type"`
	Optional    bool        `yaml:"optional"`
	Placeholder string      `yaml:"placeholder,omitempty"`
	Hint        string      `yaml:"hint,omitempty"`
	PromptKeys  []PromptKey `yaml:"prompt_keys,omitempty"`
}

type PromptKey struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	If    string `yaml:"if,omitempty"`
}

// NamespaceDerivation configures alert-payload → namespace string rendering.
// Either Template (simple form) or Rules (branchy form) is honoured;
// Rules takes precedence when both are set.
type NamespaceDerivation struct {
	Template string          `yaml:"template,omitempty"`
	Rules    []NamespaceRule `yaml:"rules,omitempty"`
}

// NamespaceRule is one when/template pair evaluated top-to-bottom; the first
// rule whose `when` predicate matches the alert-payload field map renders
// `template`. Supported predicate shapes (kept deliberately small):
//   - `${field} == 'literal'`
//   - `${field} != ''`
type NamespaceRule struct {
	When     string `yaml:"when"`
	Template string `yaml:"template"`
}

// Parse reads a profile.yaml stream into a Profile. Does not load prompts
// or k8s/kinds.json — those are populated by the directory loader.
func Parse(r io.Reader) (*Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode profile.yaml: %w", err)
	}
	return &p, nil
}

// ApplyDefaults fills in zero-valued profile fields that need a baked-in
// default. Called from the loader after Parse but before the profile is
// handed to the launcher.
func (p *Profile) ApplyDefaults() {
	if p.Models.Investigation == "" {
		p.Models.Investigation = "claude-opus-5"
	}
	if p.Models.Subagent == "" {
		p.Models.Subagent = "claude-sonnet-5"
	}
}
