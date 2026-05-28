package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// defaultSessionDrafter returns the production sessionDrafter seam. It shells
// out to `triagent-mcp sessions-draft` which runs the propose_session_draft
// sub-agent without an MCP transport.
//
// mcpBin must be a fully-resolved path (the launcher's MCPBinaryPath, set by
// locateMCPBinary at startup). Bare "triagent-mcp" is rejected because the
// binary lives alongside the launcher, not necessarily on the user's $PATH —
// the wiki / playbook subprocess paths use the resolved binary too.
//
// Wraps the spawn in a small ETXTBSY retry loop for parity with the
// other mcpBin subprocess sites; see runCmdWithETXTBSYRetry.
func defaultSessionDrafter(mcpBin string) sessionDrafter {
	return func(ctx context.Context, metadataPath, eventsPath, outPath string) error {
		if mcpBin == "" {
			return fmt.Errorf("triagent-mcp binary path is empty (server.Options.MCPBinaryPath not set)")
		}
		const maxAttempts = 5
		backoff := 10 * time.Millisecond
		var combined bytes.Buffer
		for attempt := 1; ; attempt++ {
			combined.Reset()
			cmd := exec.CommandContext(ctx, mcpBin, "sessions-draft",
				"--metadata", metadataPath,
				"--events", eventsPath,
				"--out", outPath,
			)
			cmd.Stdout = &combined
			cmd.Stderr = &combined
			err := cmd.Run()
			if err == nil {
				return nil
			}
			if !errors.Is(err, syscall.ETXTBSY) || attempt >= maxAttempts {
				return fmt.Errorf("triagent-mcp sessions-draft: %w (%s)", err, combined.Bytes())
			}
			time.Sleep(backoff)
			backoff *= 2
		}
	}
}
