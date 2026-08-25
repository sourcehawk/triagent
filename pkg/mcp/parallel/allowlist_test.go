package parallel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultAllowlist_AllowsSlowSubagentTools(t *testing.T) {
	t.Parallel()
	a := DefaultAllowlist()
	require.True(t, a.Allows("triagent-git-alerts", "analyze_change"))
	require.True(t, a.Allows("triagent-git-example-operator", "analyze_change"))
	require.True(t, a.Allows("triagent-git-anything", "correlate_with_findings"))
	require.True(t, a.Allows("triagent-git-alerts", "research_codebase"))
	require.True(t, a.Allows("triagent-git-alerts", "draft_pr"))
	require.True(t, a.Allows("triagent-wiki", "propose_wiki_draft"))
	require.True(t, a.Allows("triagent-sessions", "propose_session_draft"))
	require.True(t, a.Allows("triagent-slack", "summarize_thread"))
	require.True(t, a.Allows("triagent-slack", "analyze_channel"))
}

func TestDefaultAllowlist_RejectsCheapAndUnknownTools(t *testing.T) {
	t.Parallel()
	a := DefaultAllowlist()
	cases := []struct {
		server, tool string
	}{
		{"triagent-k8s", "get_pod"},
		{"triagent-prom", "query"},
		{"triagent-git-alerts", "latest_tags"},         // git server, but cheap
		{"triagent-git-alerts", "search_log"},          // ditto
		{"triagent-git-alerts", "search_issues"},       // ditto
		{"triagent-wiki", "wiki_search"},               // wiki, but cheap
		{"triagent-wiki", "validate_wiki"},             // wiki, but cheap
		{"triagent-slack", "slack_search_messages"},    // slack, but cheap
		{"triagent-slack", "slack_channel_overview"},   // slack, but cheap
		{"triagent-sessions", "propose_wiki_draft"},    // tool exists on triagent-wiki, not triagent-sessions
		{"other-server", "analyze_change"},             // unknown server alias prefix
		{"", "analyze_change"},                         // empty server
		{"triagent-git-alerts", ""},                    // empty tool
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.server+"/"+tc.tool, func(t *testing.T) {
			t.Parallel()
			require.False(t, a.Allows(tc.server, tc.tool))
		})
	}
}

func TestDefaultAllowlist_GitGlobMatchesPerRepoAliases(t *testing.T) {
	t.Parallel()
	a := DefaultAllowlist()
	for _, alias := range []string{"triagent-git-a", "triagent-git-zeebe", "triagent-git-example-operator", "triagent-git-foo-bar-baz"} {
		require.Truef(t, a.Allows(alias, "analyze_change"), "expected match for %s", alias)
	}
	require.False(t, a.Allows("triagent-git", "analyze_change"), "bare triagent-git (no repo suffix) must not match")
	require.False(t, a.Allows("triagent-gitter", "analyze_change"), "near-prefix must not match")
}
