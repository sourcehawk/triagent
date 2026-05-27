// Package mcp provides the shared abstractions for MCP servers in
// triagent. Each MCP implementation lives in a sibling subpackage
// (pkg/mcp/k8s, pkg/mcp/git, etc.) and exposes a constructor returning
// a value that satisfies Server. The triagent-mcp multiplexer
// (cmd/triagent-mcp/) selects one implementation by --kind=<name>.
package mcp

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/mcp/toolspec"
)

// Server is the contract every MCP implementation satisfies.
// Run blocks until ctx is cancelled or the stdio peer disconnects.
type Server interface {
	Run(ctx context.Context) error
	ToolSpecs() []toolspec.ToolSpec
}
