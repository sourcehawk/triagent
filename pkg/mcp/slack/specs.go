package slack

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the slack server's tool catalog. Each tool's input
// shape is reflected from its Go struct (and jsonschema tags).
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{Server: "triagent-slack", Name: "slack_get_channel_id", Description: "Resolve a Slack channel name to its `C…` id. Use first when you only have a name; every other slack tool needs the id.", Inputs: toolspec.FromStruct(getChannelIDIn{})},
		{Server: "triagent-slack", Name: "slack_channel_overview", Description: "Cheap first-look at a Slack channel: metadata, bookmarks, and a cache peek (recency window, parent count, thread density, distinct user count). Always the OPENING move on a channel.", Inputs: toolspec.FromStruct(channelOverviewIn{})},
		{Server: "triagent-slack", Name: "slack_search_messages", Description: "Case-insensitive substring search across a channel's cached content. Returns hits with thread_ts so you can follow up with summarize_thread.", Inputs: toolspec.FromStruct(searchIn{})},
		{Server: "triagent-slack", Name: "summarize_thread", Description: "Spawn a focused sub-agent to read a single Slack thread and answer a specific findings request. Returns summary + citations.", Inputs: toolspec.FromStruct(summarizeThreadIn{})},
		{Server: "triagent-slack", Name: "analyze_channel", Description: "Spawn a focused sub-agent to read an entire channel and answer a specific findings request. Returns summary + citations. Use when the answer spans multiple threads or the relevant thread is unknown.", Inputs: toolspec.FromStruct(analyzeChannelIn{})},
	}
}
