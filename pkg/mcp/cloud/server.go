package cloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

// baseEnvPassthrough is the minimal env every provider CLI needs regardless of
// cloud: PATH so the resolved binary can find its own dependencies, HOME so it
// can locate per-user config. Providers add their credential/impersonation
// names via Provider.EnvPassthrough.
var baseEnvPassthrough = []string{"PATH", "HOME"}

// Options configures the cloud-context MCP server.
type Options struct {
	// Provider is the cloud-specific backend (gcp or aws), injected behind the
	// Provider interface. Required; New errors when nil.
	Provider Provider
	// AllowlistPath optionally overrides the provider's embedded default command
	// allowlist. Empty means use the provider default. The launcher points this
	// at the profile-configured override via TRIAGENT_CLOUD_ALLOWLIST_PATH.
	AllowlistPath string
	// Scope is the set of projects/accounts/regions any run_cli argv may target.
	// Argv referencing a target outside the scope is rejected before exec. The
	// launcher fills it from TRIAGENT_CLOUD_SCOPE.
	Scope ScopeAllowlist
	// ExpectedIdentity is the identity the launcher pinned for this session,
	// threaded into the identity probe so it validates the resolved identity
	// against it. The launcher fills it from TRIAGENT_CLOUD_EXPECTED_IDENTITY.
	ExpectedIdentity string
}

// Server holds the configured cloud-context MCP server.
type Server struct {
	impl             *mcp.Server
	provider         Provider
	allowlist        *CommandAllowlist
	scope            ScopeAllowlist
	expectedIdentity string
	// activeTarget is the project (gcp) or account (aws) subsequent run_cli
	// commands run against, chosen via set_active_target from selectableTargets.
	// Empty means none chosen yet; subprocessEnv injects the provider's target
	// env only when set.
	activeTarget string
}

// New constructs a cloud-context MCP server. Provider is required. The command
// allowlist loads from Options.AllowlistPath (or the provider default when
// empty), always filtered through the base deny floor plus the provider's
// additions, so a too-broad override can never re-enable a floored command.
func New(opts Options) (*Server, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("cloud: Provider is required")
	}
	allow, err := loadAllowlist(opts.AllowlistPath, opts.Provider)
	if err != nil {
		return nil, fmt.Errorf("cloud: load command allowlist: %w", err)
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-cloud",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:             impl,
		provider:         opts.Provider,
		allowlist:        allow,
		scope:            opts.Scope,
		expectedIdentity: opts.ExpectedIdentity,
	}
	// A single selectable target is the active target from session start
	// (today's behavior); with several, the agent must choose via
	// set_active_target before run_cli will run.
	if sel := s.selectableTargets(context.Background()); len(sel) == 1 {
		s.activeTarget = sel[0].ID
	}
	s.registerOn(impl)
	return s, nil
}

// selectableTargets returns the set the agent may choose from: the provider's
// configured targets (aws accounts) when present, else the scope projects, else
// (unconstrained) the live inventory scopes.
func (s *Server) selectableTargets(ctx context.Context) []Target {
	if t := s.provider.ConfiguredTargets(); len(t) > 0 {
		return t
	}
	if len(s.scope.Projects) > 0 {
		out := make([]Target, 0, len(s.scope.Projects))
		for _, p := range s.scope.Projects {
			out = append(out, Target{ID: p, Name: p})
		}
		return out
	}
	inv, err := s.provider.Inventory(ctx, s.runValidated)
	if err != nil {
		return nil
	}
	out := make([]Target, 0, len(inv.Scopes))
	for _, sc := range inv.Scopes {
		out = append(out, Target(sc))
	}
	return out
}

// setActive validates id against the selectable set and pins it as the active
// target. An id outside the set is rejected, so the agent can never name a
// target the deployment did not configure.
func (s *Server) setActive(id string) error {
	for _, t := range s.selectableTargets(context.Background()) {
		if t.ID == id {
			s.activeTarget = id
			return nil
		}
	}
	return fmt.Errorf("target %q is not in the configured set", id)
}

// loadAllowlist resolves the command allowlist for a provider: the override path
// when given, else the provider's embedded default, always filtered through the
// base deny floor plus the provider's deny-floor additions.
func loadAllowlist(path string, p Provider) (*CommandAllowlist, error) {
	if path != "" {
		return LoadCommandAllowlist(path, p.DenyFloorAdditions())
	}
	// Filter the provider's in-memory default through the floor the same way a
	// loaded file would be, so the default can never advertise a floored command.
	return filterAllowlist(p.DefaultAllowlist(), p.DenyFloorAdditions()), nil
}

// Run serves MCP requests over stdio until the client disconnects or ctx is
// cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// run is the harness exec core bound to this server's provider binary, scope,
// and allowlist. Tools exec only through this RunFunc, never directly: it gates
// on an active target being chosen when several are selectable, then validates
// argv before handing it to the no-shell exec core.
func (s *Server) run(ctx context.Context, argv []string) (CLIResult, error) {
	if s.activeTarget == "" && len(s.selectableTargets(ctx)) > 1 {
		return CLIResult{}, errNoActiveTarget
	}
	return s.runValidated(ctx, argv)
}

// runValidated is the exec core without the active-target gate: it validates
// argv against the allowlist and scope, then execs under the subprocess env.
// selectableTargets derives the target set through this path so deriving the
// set never re-enters the active-target check (which itself consults
// selectableTargets) — inventory shelled during derivation cannot recurse.
func (s *Server) runValidated(ctx context.Context, argv []string) (CLIResult, error) {
	if err := validateArgv(argv, s.allowlist, s.scope); err != nil {
		return CLIResult{}, err
	}
	return execCLI(ctx, s.provider.Binary(), argv, s.subprocessEnv(), defaultOutputLimit)
}

// errNoActiveTarget is returned by run when several targets are selectable but
// none is active, so a command never runs against an unintended default. It is
// surfaced to the agent as an actionable run_cli tool error.
var errNoActiveTarget = errors.New("no active target; call set_active_target to choose one")

// expectedIdentityForActive is the identity the probe validates the session
// against: the active target's own identity when the provider pins it per-target
// (aws: the account's role ARN), else the session's pinned identity (gcp, where
// one impersonated service account spans every project). This is what lets
// session_status report Valid for any selected account, not just the default.
func (s *Server) expectedIdentityForActive() string {
	if s.activeTarget != "" {
		if exp, ok := s.provider.ExpectedIdentity(s.activeTarget); ok {
			return exp
		}
	}
	return s.expectedIdentity
}

// subprocessEnv builds the explicit, minimal environment for a provider CLI
// invocation: only the base names plus the provider's declared passthrough
// names, read from the launcher-controlled process env. Everything else is
// dropped, so the launcher's ambient secrets never reach the CLI.
//
// The active-target env (gcp CLOUDSDK_CORE_PROJECT, aws AWS_PROFILE) overrides
// any ambient value for the same name carried through passthrough: the ambient
// entry is dropped before the MCP-controlled value is appended, so a duplicate
// can never let the CLI resolve to the ambient target instead of the
// set_active_target choice.
func (s *Server) subprocessEnv() []string {
	env := minimalEnv(s.provider.EnvPassthrough())
	if s.activeTarget != "" {
		active := s.provider.ActiveTargetEnv(s.activeTarget)
		env = append(dropEnvNames(env, envNames(active)), active...)
	}
	return env
}

// envNames returns the variable names ("NAME" from "NAME=value") of env entries.
func envNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok {
			names = append(names, name)
		}
	}
	return names
}

// dropEnvNames returns env without any entry whose variable name is in names.
func dropEnvNames(env, names []string) []string {
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		drop[n] = true
	}
	out := env[:0]
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && drop[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// minimalEnv returns the subprocess environment built from os.Environ() filtered
// to the base passthrough names plus the provider-declared ones — everything
// else (the launcher's ambient secrets) is dropped. Both the run_cli harness and
// the identity probe build their subprocess env here so neither can leak the
// parent environment.
func minimalEnv(passthrough []string) []string {
	keep := make(map[string]bool, len(baseEnvPassthrough)+len(passthrough))
	for _, name := range baseEnvPassthrough {
		keep[name] = true
	}
	for _, name := range passthrough {
		keep[name] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if ok && keep[name] {
			env = append(env, kv)
		}
	}
	return env
}

// registerOn wires the cloud tools onto impl. Called from New and from the wire
// test inside the package. Registration order mirrors ToolSpecs().
func (s *Server) registerOn(impl *mcp.Server) {
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_inventory",
		Description: descListInventory,
	}, telemetry.Wrap("list_inventory", s.listInventory))
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "session_status",
		Description: descSessionStatus,
	}, telemetry.Wrap("session_status", s.sessionStatus))
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "set_active_target",
		Description: descSetActiveTarget,
	}, telemetry.Wrap("set_active_target", s.setActiveTarget))
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "run_cli",
		Description: descRunCLI,
	}, telemetry.Wrap("run_cli", s.runCLI))
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_allowed_commands",
		Description: descListAllowedCommands,
	}, telemetry.Wrap("list_allowed_commands", s.listAllowedCommands))
}
