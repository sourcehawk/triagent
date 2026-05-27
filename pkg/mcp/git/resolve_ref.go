package git

import (
	"context"
	"strings"
)

// resolveRef rewrites a bare ref name to its remote-tracking counterpart
// when one exists in the cache clone, so that subsequent git commands
// operate on the freshly-fetched origin tip rather than a stale local
// branch ref. Tags, SHAs, and already-qualified refs (origin/foo,
// refs/tags/foo) pass through unchanged.
//
// Why: EnsureClone runs `git fetch --prune --tags`, which advances
// `refs/remotes/origin/*` but does NOT fast-forward local branches.
// Any tool that resolves a bare branch name (e.g. "main", "feature/foo")
// against the cache clone reads stale state. By probing for the
// remote-tracking ref and prefixing `origin/` when it exists, we make
// every git tool's view current with the last fetch.
//
// Behavior:
//   - "" → "origin/<defaultBranch>"
//   - <name> with refs/remotes/origin/<name> present → "origin/<name>"
//   - anything with "origin/" or "refs/" prefix → unchanged (caller is
//     already explicit)
//   - anything else (tag, SHA, typo) → unchanged; the downstream git
//     command surfaces the agent's input verbatim in its error
//
// Branch-vs-tag collision: when both `origin/<name>` and a tag of the
// same name exist, we prefer the branch — matches `git checkout <name>`
// semantics. Tags are immutable so the conceptual "value" is identical
// either way.
func (s *Server) resolveRef(ctx context.Context, repoDir, ref string) (string, error) {
	if ref == "" {
		db, err := s.defaultBranch(ctx)
		if err != nil {
			return "", err
		}
		return "origin/" + db, nil
	}
	if strings.HasPrefix(ref, "origin/") || strings.HasPrefix(ref, "refs/") {
		return ref, nil
	}
	if err := runGit(ctx, repoDir, "rev-parse", "--verify", "--quiet",
		"refs/remotes/origin/"+ref); err == nil {
		return "origin/" + ref, nil
	}
	return ref, nil
}
