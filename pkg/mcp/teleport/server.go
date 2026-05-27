// Package teleport implements the triagent-mcp `teleport` MCP server. It exposes
// two tools — list_clusters and login — for Kubernetes cluster discovery and
// per-cluster kubeconfig provisioning via Teleport.
package teleport

import (
	"context"
	"fmt"

	"github.com/sourcehawk/triagent/pkg/auth"
	authteleport "github.com/sourcehawk/triagent/pkg/auth/teleport"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the teleport MCP server.
type Options struct {
	// KubeconfigPath is the kubeconfig the launcher froze for this
	// session. list_clusters consults it to mark which Teleport clusters
	// already have a context provisioned; login writes new contexts into
	// it (via the SDK).
	KubeconfigPath string
	// Provider is injectable for tests. nil means use authteleport.NewProvider().
	Provider TeleportProvider
}

// TeleportProvider is the interface this MCP requires from the teleport
// Provider. Defining it locally lets tests stub it out.
type TeleportProvider interface {
	IsAuthenticated() bool
	ListClusters(ctx context.Context) ([]auth.Cluster, error)
	Login(ctx context.Context, cluster string) (*auth.LoginResult, error)
}

// Server holds the configured MCP server.
type Server struct {
	impl           *mcp.Server
	kubeconfigPath string
	provider       TeleportProvider
}

// New constructs a teleport MCP server. KubeconfigPath is required.
func New(opts Options) (*Server, error) {
	if opts.KubeconfigPath == "" {
		return nil, fmt.Errorf("kubeconfig path is required")
	}
	provider := opts.Provider
	if provider == nil {
		provider = authteleport.NewProvider(authteleport.Config{})
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-teleport",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:           impl,
		kubeconfigPath: opts.KubeconfigPath,
		provider:       provider,
	}
	s.registerOn(impl)
	return s, nil
}

// Run serves MCP requests over stdio until the client disconnects or ctx is
// cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// registerOn wires the teleport tools onto impl. Called from New and from
// wire tests inside package teleport.
func (s *Server) registerOn(impl *mcp.Server) {
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_clusters",
		Description: "List Kubernetes clusters reachable via Teleport. Marks clusters that already have a kubeconfig context provisioned.",
	}, telemetry.Wrap("list_clusters", s.listClusters))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "login",
		Description: "Provision kubeconfig credentials for a Teleport-managed Kubernetes cluster. Returns the kubeconfig context name; pair with triagent-k8s.switch_context to start using it.",
	}, telemetry.Wrap("login", s.login))
}
