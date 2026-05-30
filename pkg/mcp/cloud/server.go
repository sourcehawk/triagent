package cloud

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the cloud-context MCP server.
type Options struct {
	// Provider is the cloud-specific backend (gcp or aws), injected behind the
	// Provider interface. Required; New errors when nil.
	Provider Provider
}

// Server holds the configured cloud-context MCP server.
type Server struct {
	impl     *mcp.Server
	provider Provider
}

// New constructs a cloud-context MCP server. Provider is required.
func New(opts Options) (*Server, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("cloud: Provider is required")
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-cloud",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:     impl,
		provider: opts.Provider,
	}
	s.registerOn(impl)
	return s, nil
}

// Run serves MCP requests over stdio until the client disconnects or ctx is
// cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// registerOn wires the cloud tools onto impl. Called from New and from wire
// tests inside the package.
func (s *Server) registerOn(impl *mcp.Server) {}
