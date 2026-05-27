package teleport

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LoginInput is the input schema for the login tool.
type LoginInput struct {
	Cluster string `json:"cluster" jsonschema:"The Teleport cluster name to provision kubeconfig credentials for. Must match one returned by list_clusters."`
}

// LoginOutput is the response schema for the login tool.
type LoginOutput struct {
	ContextName    string `json:"contextName"`
	KubeconfigPath string `json:"kubeconfigPath"`
}

// login provisions kubeconfig credentials for the named Teleport cluster.
// Returns the new kubeconfig context name; the caller should pass it to
// triagent-k8s.switch_context to start querying that cluster.
func (s *Server) login(ctx context.Context, _ *mcp.CallToolRequest, in LoginInput) (*mcp.CallToolResult, LoginOutput, error) {
	if !s.provider.IsAuthenticated() {
		return errorResult(authRequiredMessage), LoginOutput{}, nil
	}
	if in.Cluster == "" {
		return errorResult("cluster is required"), LoginOutput{}, nil
	}
	res, err := s.provider.Login(ctx, in.Cluster)
	if err != nil {
		return errorResult(fmt.Sprintf("login %q: %v", in.Cluster, err)), LoginOutput{}, nil
	}
	return nil, LoginOutput{
		ContextName:    res.ContextName,
		KubeconfigPath: s.kubeconfigPath,
	}, nil
}
