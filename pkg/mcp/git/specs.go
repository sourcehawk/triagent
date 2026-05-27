package git

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the git server's tool catalog. Each tool's
// input shape is reflected from its Go struct (and jsonschema tags).
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{Server: "triagent-git", Name: "latest_tags", Description: "Most recent tags / releases for the linked repo.", Inputs: toolspec.FromStruct(latestTagsIn{})},
		{Server: "triagent-git", Name: "commit_summary", Description: "Summarise commits in a date or revision range.", Inputs: toolspec.FromStruct(commitSummaryIn{})},
		{Server: "triagent-git", Name: "diff_summary", Description: "Diff summary between two revisions.", Inputs: toolspec.FromStruct(diffSummaryIn{})},
		{Server: "triagent-git", Name: "search_log", Description: "Search the git log for commits matching a query.", Inputs: toolspec.FromStruct(searchLogIn{})},
		{Server: "triagent-git", Name: "analyze_change", Description: "Run a sub-agent to investigate the impact of a specific change.", Inputs: toolspec.FromStruct(analyzeChangeIn{})},
		{Server: "triagent-git", Name: "correlate_with_findings", Description: "Correlate recorded findings with recent changes in the linked repo.", Inputs: toolspec.FromStruct(correlateIn{})},
		{Server: "triagent-git", Name: "get_repo_architecture_summary", Description: "Read the cached architecture summary for this repo. Cheap deterministic call — the summary was generated upfront by a sub-agent at connection time. Call this first when investigating a question that touches this repo: it surfaces canonical names, top-level structure, common breakage patterns, and the domain language to literal-match against incident reports. Returns {exists, generated_at, content, hint, error}; when exists=false, the hint points at the connection-time description in the LinkedRepos section as the fallback layer.", Inputs: toolspec.FromStruct(getArchitectureSummaryIn{})},
	}
}
