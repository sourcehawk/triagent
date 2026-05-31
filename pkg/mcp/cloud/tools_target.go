package cloud

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descSetActiveTarget = "Choose which project (GCP) or account (AWS) subsequent run_cli commands run against, from the configured set shown by list_inventory. You cannot choose a target outside that set. Read-only."

// SetActiveTargetInput is the input schema for set_active_target.
type SetActiveTargetInput struct {
	Target string `json:"target" jsonschema:"The project id (GCP) or account id (AWS) to activate, from list_inventory."`
}

// SetActiveTargetOutput is the response schema for set_active_target: the new
// target's session_status, so the agent immediately sees whether it is valid.
type SetActiveTargetOutput = IdentityStatus

// setActiveTarget pins the active target after validating it against the
// selectable set, then re-probes so the returned status reflects the new
// target. A target outside the set is rejected before anything changes.
func (s *Server) setActiveTarget(ctx context.Context, _ *mcp.CallToolRequest, in SetActiveTargetInput) (*mcp.CallToolResult, SetActiveTargetOutput, error) {
	if err := s.setActive(in.Target); err != nil {
		return errorResult(fmt.Sprintf("set_active_target rejected: %v", err)), SetActiveTargetOutput{}, nil
	}
	st, _ := Probe(ctx, s.provider, s.expectedIdentity, s.subprocessEnv())
	return nil, st, nil
}
