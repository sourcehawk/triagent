package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sessionIDFile is the per-worktree filename that holds the claude
// conversation id from the most recent draft_pr sub-agent invocation.
// Reused on the next draft_pr call against the same issue so the
// sub-agent --resume's the prior conversation instead of starting cold.
const sessionIDFile = ".triagent-session-id"

// findExistingWorktreeForIssue searches `git worktree list` output for a
// worktree whose branch matches the triagent-proposal/<issueNum>-* shape this
// MCP mints in newWorktreeBranch. Returns the worktree path and branch
// name if found. Empty + false when no in-flight worktree exists for
// this issue (the common first-call case).
func (s *Server) findExistingWorktreeForIssue(ctx context.Context, repoDir string, issueNum int) (wtPath, branch string, found bool) {
	out, err := gitOutput(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", "", false
	}
	prefix := fmt.Sprintf("triagent-proposal/%d-", issueNum)
	// Porcelain shape:
	//   worktree <path>
	//   HEAD <sha>
	//   branch refs/heads/<name>
	//
	//   worktree <path>
	//   ...
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var path, br string
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "worktree "); ok {
				path = v
			} else if v, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				br = v
			}
		}
		if path == "" || br == "" {
			continue
		}
		if !strings.HasPrefix(br, prefix) {
			continue
		}
		// Verify the worktree dir still exists (git's bookkeeping can
		// outlive a manually-rm'd dir until prune runs).
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		return path, br, true
	}
	return "", "", false
}

// readPersistedSessionID reads the per-worktree session id file. Empty
// string when the file is missing or unreadable — caller treats that as
// "no prior session, start fresh".
func readPersistedSessionID(wt string) string {
	b, err := os.ReadFile(filepath.Join(wt, sessionIDFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writePersistedSessionID stores the latest claude session id in the
// worktree so the next draft_pr call against the same issue can resume
// the conversation. Best-effort: any I/O error is silently swallowed so
// a transient disk hiccup doesn't break the in-flight call.
func writePersistedSessionID(wt, id string) {
	if wt == "" || id == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(wt, sessionIDFile), []byte(id), 0o600)
}


