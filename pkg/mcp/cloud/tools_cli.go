package cloud

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const descRunCLI = "Run one read-only provider CLI command (gcloud/aws). Supply the argument tokens as an array, never a single string — there is no shell. Only allowlisted subcommands run; identity flags, credential-reading subcommands, and out-of-scope targets are rejected. See list_allowed_commands for what is permitted."

const descListAllowedCommands = "List the provider CLI subcommands run_cli permits, with the investigative axis each serves. This is exactly what run_cli enforces, so what is advertised is what is allowed."

// RunCLIInput is the input schema for run_cli. Argv is a typed array of argument
// tokens, never a single command string: the harness never tokenizes, so there
// is no in-house splitter to fool and shell metacharacters are inert.
type RunCLIInput struct {
	Argv []string `json:"argv" jsonschema:"The provider CLI argument tokens as an array (for example [\"compute\",\"firewall-rules\",\"list\",\"--project\",\"prod\"]). Do not include the binary name or pass a single string."`
}

// RunCLIOutput is the response schema for run_cli: the shaped CLI result, never
// raw API JSON beyond the captured stdout the provider emitted.
type RunCLIOutput = CLIResult

// runCLI validates the argv against the allowlist, deny floor, and scope, then
// execs it through the no-shell core. A rejected argv is a tool error returned
// before any exec; a non-zero CLI exit is a normal result the agent sees.
func (s *Server) runCLI(ctx context.Context, _ *mcp.CallToolRequest, in RunCLIInput) (*mcp.CallToolResult, RunCLIOutput, error) {
	res, err := s.run(ctx, in.Argv)
	if err != nil {
		return errorResult(fmt.Sprintf("run_cli rejected: %v", err)), RunCLIOutput{}, nil
	}
	return nil, res, nil
}

// ListAllowedCommandsInput is the input schema for list_allowed_commands. It
// takes no parameters.
type ListAllowedCommandsInput struct{}

// ListAllowedCommandsOutput is the response schema for list_allowed_commands.
type ListAllowedCommandsOutput struct {
	Commands []Command `json:"commands"`
}

// listAllowedCommands returns the same CommandAllowlist run_cli enforces, so the
// catalog and the gate can never disagree.
func (s *Server) listAllowedCommands(_ context.Context, _ *mcp.CallToolRequest, _ ListAllowedCommandsInput) (*mcp.CallToolResult, ListAllowedCommandsOutput, error) {
	return nil, ListAllowedCommandsOutput{Commands: s.allowlist.Commands}, nil
}

// errorResult builds an MCP error result whose Content carries msg.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
