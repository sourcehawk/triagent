package cloud

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descListInventory = "List the cloud projects (GCP) or accounts (AWS) the pinned read-only identity can see, so you can orient before drilling in. Each entry carries the deployment's free-form tags (e.g. prod, payments) so you can judge which target an investigation belongs to. Read-only."

// ListInventoryInput is the input schema for list_inventory. It takes no
// parameters: the accessible scope is fixed by the pinned identity.
type ListInventoryInput struct{}

// ListInventoryOutput is the response schema for list_inventory: the provider's
// accessible scopes.
type ListInventoryOutput = Inventory

// listInventory projects the provider's accessible scopes. It execs through the
// validated-but-ungated run core: inventory is how the agent discovers which
// targets it may select, so it must not require an active target first (the same
// path selectableTargets uses).
func (s *Server) listInventory(ctx context.Context, _ *mcp.CallToolRequest, _ ListInventoryInput) (*mcp.CallToolResult, ListInventoryOutput, error) {
	inv, err := s.provider.Inventory(ctx, s.runValidated)
	if err != nil {
		return errorResult(fmt.Sprintf("list inventory: %v", err)), ListInventoryOutput{}, nil
	}
	return nil, inv, nil
}
