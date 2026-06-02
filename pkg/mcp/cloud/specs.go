package cloud

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the cloud server's tool catalog with each tool's input shape
// introspected from its Go struct (and its jsonschema tags).
//
// Order mirrors the registration order in server.go's registerOn().
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{
			Server:      "triagent-cloud",
			Name:        "list_inventory",
			Description: descListInventory,
			Inputs:      toolspec.FromStruct(ListInventoryInput{}),
		},
		{
			Server:      "triagent-cloud",
			Name:        "session_status",
			Description: descSessionStatus,
			Inputs:      toolspec.FromStruct(SessionStatusInput{}),
		},
		{
			Server:      "triagent-cloud",
			Name:        "set_active_target",
			Description: descSetActiveTarget,
			Inputs:      toolspec.FromStruct(SetActiveTargetInput{}),
		},
		{
			Server:      "triagent-cloud",
			Name:        "run_cli",
			Description: descRunCLI,
			Inputs:      toolspec.FromStruct(RunCLIInput{}),
		},
		{
			Server:      "triagent-cloud",
			Name:        "list_allowed_commands",
			Description: descListAllowedCommands,
			Inputs:      toolspec.FromStruct(ListAllowedCommandsInput{}),
		},
	}
}
