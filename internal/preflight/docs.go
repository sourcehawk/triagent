package preflight

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// detectDocsServer reports whether name is registered in the user's
// `claude mcp list`. Bounded timeout so a stuck claude CLI doesn't hang us.
func detectDocsServer(name string) bool {
	if name == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "mcp", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, name) {
			return true
		}
	}
	return false
}
