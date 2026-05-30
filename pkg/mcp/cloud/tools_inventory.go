package cloud

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descListInventory = "List the cloud projects (GCP) or accounts (AWS) the pinned read-only identity can see, so you can orient before drilling in. Read-only."

// ListInventoryInput is the input schema for list_inventory. It takes no
// parameters: the accessible scope is fixed by the pinned identity.
type ListInventoryInput struct{}

// ListInventoryOutput is the response schema for list_inventory: the provider's
// accessible scopes.
type ListInventoryOutput = Inventory

// listInventory projects the provider's accessible scopes. The provider execs
// only through the server's validated run core.
func (s *Server) listInventory(ctx context.Context, _ *mcp.CallToolRequest, _ ListInventoryInput) (*mcp.CallToolResult, ListInventoryOutput, error) {
	inv, err := s.provider.Inventory(ctx, s.run)
	if err != nil {
		return errorResult(fmt.Sprintf("list inventory: %v", err)), ListInventoryOutput{}, nil
	}
	return nil, inv, nil
}
