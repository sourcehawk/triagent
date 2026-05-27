package parallel

import "strings"

// Allowlist gates which (server, tool) pairs the broker will dispatch.
// Hardcoded — adding a tool is a code change, not a config change. See
// the spec's "Allowlist" section for the rationale (avoid batching cheap
// tools, bound blast radius).
type Allowlist struct {
	entries []allowEntry
}

// allowEntry pairs a server-name pattern with the exact tool name it
// authorises. The pattern is either a literal match or a prefix-match
// when it ends in "*". No other glob syntax is supported; the only real
// use case today is "triagent-git-*" for per-repo aliases.
type allowEntry struct {
	serverPattern string
	tool          string
}

// DefaultAllowlist returns every sub-agent-backed tool the triagent-mcp
// servers expose — i.e. each tool that internally spawns a focused
// `claude` sub-process. These are the slow, expensive calls the broker
// is built to parallelize. Cheap deterministic tools are intentionally
// absent: batching them burns more on round-trip + telemetry overhead
// than it saves in wall-clock.
func DefaultAllowlist() Allowlist {
	return Allowlist{entries: []allowEntry{
		// triagent-git (one server per linked repo). draft_pr has side
		// effects (branch push + PR creation); each invocation mints
		// a unique branch so parallel calls don't collide on the same
		// repo, but be aware that a batch can open multiple PRs at once.
		{"triagent-git-*", "analyze_change"},
		{"triagent-git-*", "correlate_with_findings"},
		{"triagent-git-*", "draft_pr"},
		// triagent-wiki — proposal drafter.
		{"triagent-wiki", "propose_wiki_draft"},
		// triagent-sessions — investigation-note drafter.
		{"triagent-sessions", "propose_session_draft"},
		// triagent-slack — channel/thread summarisation, both sub-agent-backed.
		{"triagent-slack", "summarize_thread"},
		{"triagent-slack", "analyze_channel"},
	}}
}

// Allows reports whether the given (server, tool) pair is dispatchable.
// Empty server or tool is always rejected.
func (a Allowlist) Allows(server, tool string) bool {
	if server == "" || tool == "" {
		return false
	}
	for _, e := range a.entries {
		if e.tool != tool {
			continue
		}
		if matchPattern(e.serverPattern, server) {
			return true
		}
	}
	return false
}

// matchPattern implements the trivial "literal | prefix*" matcher
// described on allowEntry. The trailing `*` requires at least one
// character to follow the prefix so "triagent-git-*" never matches the bare
// "triagent-git" alias.
func matchPattern(pattern, s string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return len(s) > len(prefix) && strings.HasPrefix(s, prefix)
	}
	return pattern == s
}
