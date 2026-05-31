// Package cloud implements the read-only cloud-context MCP server the
// triagent-mcp binary exposes to Claude. One package serves both GCP and AWS:
// the cloud-specific behaviour sits behind the Provider interface, selected at
// launch by --provider and plugged in from pkg/mcp/cloud/providers/<name>.
//
// The server is read-only by construction. run_cli never touches a shell, every
// invocation is validated against a positive command allowlist plus a hardcoded
// deny floor the config can never re-enable, and the cloud identity is pinned by
// the deployment through harness-controlled env the agent cannot reach.
package cloud

import "context"

// Provider is the cloud-specific seam every tool calls through. Selecting
// --provider chooses the concrete gcp or aws implementation, injected behind
// this interface (the teleport DI pattern). Implementations live in
// pkg/mcp/cloud/providers/<name>, never in this package.
type Provider interface {
	// Name reports the provider identifier ("gcp" | "aws").
	Name() string
	// Binary is the resolved absolute path to the provider CLI (gcloud/aws).
	Binary() string
	// DefaultAllowlist is the provider's embedded default command allowlist.
	DefaultAllowlist() *CommandAllowlist
	// DenyFloorAdditions contributes provider-specific subcommands and flags to
	// the always-on deny floor. The base floor lives in this package; providers
	// only add to it, never relax it.
	DenyFloorAdditions() DenyFloor
	// EnvPassthrough lists the environment variable NAMES this provider's CLI
	// needs forwarded from the launcher-controlled process env to the subprocess
	// (base credentials, the pinned-identity impersonation target, config dirs).
	// The harness forwards only these plus a minimal base set; every other parent
	// env var is dropped, so ambient launcher secrets never reach the CLI.
	EnvPassthrough() []string
	// Inventory projects the provider's accessible scopes (projects for gcp,
	// accounts for aws). It execs only through run, never directly.
	Inventory(ctx context.Context, run RunFunc) (Inventory, error)
	// Identity is the read-only whoami: which pinned identity is active and
	// whether it is valid. expected is the identity the launcher pinned for this
	// session (the impersonation target for gcp, the expected role ARN for aws,
	// empty when none is pinned); the provider validates the resolved identity
	// against it. It execs only through run, never directly.
	Identity(ctx context.Context, run RunFunc, expected string) (IdentityStatus, error)
	// ConfiguredTargets is the deployment-configured selectable set the provider
	// itself knows (aws: its accounts list). Empty when the set comes from the
	// server's scope/inventory instead (gcp).
	ConfiguredTargets() []Target
	// ActiveTargetEnv returns the env var(s) that pin the CLI to targetID for the
	// next invocation: gcp CLOUDSDK_CORE_PROJECT, aws AWS_PROFILE. The agent never
	// supplies these; the server sets them per-exec.
	ActiveTargetEnv(targetID string) []string
	// ExpectedIdentity returns the identity the active target must resolve to when
	// the provider pins it per-target (aws: the account's read-only role ARN), so
	// session_status validates the active target against the right identity on
	// switch. ok is false when the provider's identity is uniform across targets
	// (gcp: one impersonated service account spans its projects), leaving the
	// server to validate against the session's pinned identity instead.
	ExpectedIdentity(targetID string) (string, bool)
}

// Target is one selectable project (gcp) or account (aws) the agent may make
// active via set_active_target.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RunFunc is the harness exec core, injected into providers so they never exec
// directly. It carries the no-shell guarantee: argv tokens reach the provider
// binary via execve, never a shell.
type RunFunc func(ctx context.Context, argv []string) (CLIResult, error)

// Inventory is the projected list of accessible scopes the agent uses to orient.
type Inventory struct {
	Scopes []Scope `json:"scopes"`
}

// Scope is one project (gcp) or account (aws) the pinned identity can read.
type Scope struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// IdentityStatus is the single struct the identity probe returns. The
// connections array, the session_status tool, and the preflight gate all render
// from it, so they cannot disagree. JSON tags are a downstream contract.
type IdentityStatus struct {
	Provider        string `json:"provider"`
	AssumedIdentity string `json:"assumed_identity"`
	Valid           bool   `json:"valid"`
	Hint            string `json:"hint,omitempty"`
	// ActiveTarget is the project (gcp) or account (aws) run_cli currently runs
	// against. The probe leaves it empty; the server fills it from its
	// active-target state so session_status reports the identity and the target
	// together.
	ActiveTarget string `json:"active_target,omitempty"`
}

// CLIResult is the result of one run_cli invocation. It carries the provider
// CLI's raw stdout (and stderr), each capped at the output limit with Truncated
// set when the output exceeded it. The bytes are not otherwise shaped or
// redacted; callers must not assume any projection beyond truncation.
type CLIResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr,omitempty"`
	Truncated bool   `json:"truncated"`
	ExitCode  int    `json:"exit_code"`
}
