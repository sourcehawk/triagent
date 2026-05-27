package git

import (
	"context"
	"strings"
)

// defaultBranch returns the linked repo's default branch name (e.g.
// "main", "master", "develop"), resolved once via `gh repo view --json
// defaultBranchRef --jq .defaultBranchRef.name` and cached on the Server
// for the rest of the process lifetime.
//
// Why cache: tool calls invoke this whenever they want to rewrite a bare
// default-branch reference to its remote-tracking counterpart. Re-shelling
// to gh on every tool call would add ~hundreds of ms per call against a
// value that effectively never changes mid-process.
//
// Why fall back to "main": a transient gh failure (auth, network, rate
// limit) shouldn't make every git tool fail with a default-branch lookup
// error. Falling back gives the agent the same behaviour we had before
// detection existed. The fallback is cached too so we don't retry on
// every subsequent tool call within the same process.
func (s *Server) defaultBranch(ctx context.Context) (string, error) {
	s.defaultBranchOnce.Do(func() {
		stdout, _, err := s.gh.Run(ctx, "repo", "view", s.repoFull(),
			"--json", "defaultBranchRef",
			"--jq", ".defaultBranchRef.name")
		name := strings.TrimSpace(string(stdout))
		if err != nil || name == "" {
			s.defaultBranchName = "main"
			return
		}
		s.defaultBranchName = name
	})
	return s.defaultBranchName, nil
}
