package server

import (
	"context"
	"fmt"
	"os/exec"
)

// defaultSessionDrafter returns the production sessionDrafter seam. It shells
// out to `triagent-mcp sessions-draft` which runs the propose_session_draft
// sub-agent without an MCP transport.
//
// mcpBin must be a fully-resolved path (the launcher's MCPBinaryPath, set by
// locateMCPBinary at startup). Bare "triagent-mcp" is rejected because the
// binary lives alongside the launcher, not necessarily on the user's $PATH —
// the wiki / playbook subprocess paths use the resolved binary too.
func defaultSessionDrafter(mcpBin string) sessionDrafter {
	return func(ctx context.Context, metadataPath, eventsPath, outPath string) error {
		if mcpBin == "" {
			return fmt.Errorf("triagent-mcp binary path is empty (server.Options.MCPBinaryPath not set)")
		}
		cmd := exec.CommandContext(ctx, mcpBin, "sessions-draft",
			"--metadata", metadataPath,
			"--events", eventsPath,
			"--out", outPath,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("triagent-mcp sessions-draft: %w (%s)", err, out)
		}
		return nil
	}
}
