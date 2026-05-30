package cloud

import (
	"context"
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
}

// Server holds the configured cloud-context MCP server.
type Server struct {
	impl      *mcp.Server
	provider  Provider
	allowlist *CommandAllowlist
	scope     ScopeAllowlist
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
		impl:      impl,
		provider:  opts.Provider,
		allowlist: allow,
		scope:     opts.Scope,
	}
	s.registerOn(impl)
	return s, nil
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
// and allowlist. Providers and tools exec only through this RunFunc, never
// directly: it validates argv before handing it to the no-shell exec core.
func (s *Server) run(ctx context.Context, argv []string) (CLIResult, error) {
	if err := validateArgv(argv, s.allowlist, s.scope); err != nil {
		return CLIResult{}, err
	}
	return execCLI(ctx, s.provider.Binary(), argv, s.subprocessEnv(), defaultOutputLimit)
}

// subprocessEnv builds the explicit, minimal environment for a provider CLI
// invocation: only the base names plus the provider's declared passthrough
// names, read from the launcher-controlled process env. Everything else is
// dropped, so the launcher's ambient secrets never reach the CLI.
func (s *Server) subprocessEnv() []string {
	keep := make(map[string]bool, len(baseEnvPassthrough))
	for _, name := range baseEnvPassthrough {
		keep[name] = true
	}
	for _, name := range s.provider.EnvPassthrough() {
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
		Name:        "run_cli",
		Description: descRunCLI,
	}, telemetry.Wrap("run_cli", s.runCLI))
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_allowed_commands",
		Description: descListAllowedCommands,
	}, telemetry.Wrap("list_allowed_commands", s.listAllowedCommands))
}
