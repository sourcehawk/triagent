package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type summarizeThreadIn struct {
	ChannelID       string `json:"channel_id" jsonschema:"Slack channel id (C…/D…/G…) the thread lives in; required. Use slack_get_channel_id first if you only have a name."`
	ThreadTS        string `json:"thread_ts" jsonschema:"the parent message's ts; required"`
	DesiredFindings string `json:"desired_findings" jsonschema:"what the parent agent wants from this thread (e.g. 'what was the root cause and which services were affected', 'who took which actions and in what order', 'what links to dashboards/PRs were shared')"`
}

type summarizeThreadOut struct {
	ThreadTS            string               `json:"thread_ts"`
	Summary             string               `json:"summary"`
	Citations           []citations.Citation `json:"citations"`
	CitationsParseError string               `json:"citations_parse_error,omitempty"`
	PromptSent          string               `json:"prompt_sent"`
	TimedOut            bool                 `json:"timed_out,omitempty"`
	RateLimited         bool                 `json:"rate_limited,omitempty"`
}

func (s *Server) handleSummarizeThread(ctx context.Context, _ *mcp.CallToolRequest, in summarizeThreadIn) (*mcp.CallToolResult, summarizeThreadOut, error) {
	channelID := strings.TrimSpace(in.ChannelID)
	if channelID == "" {
		return errorResult("channel_id is required"), summarizeThreadOut{Citations: []citations.Citation{}}, nil
	}
	if in.ThreadTS == "" {
		return errorResult("thread_ts is required"), summarizeThreadOut{Citations: []citations.Citation{}}, nil
	}
	if in.DesiredFindings == "" {
		return errorResult("desired_findings is required"), summarizeThreadOut{Citations: []citations.Citation{}}, nil
	}

	store, err := s.resolveStore(channelID)
	if err != nil {
		return errorResult(err.Error()), summarizeThreadOut{Citations: []citations.Citation{}}, nil
	}
	_, rateLimited, err := store.SyncThread(ctx, in.ThreadTS)
	if err != nil {
		return errorResult(err.Error()), summarizeThreadOut{Citations: []citations.Citation{}}, nil
	}

	prompt := fmt.Sprintf(`You are analysing one Slack thread. This message is your COMPLETE task — there is no prior conversation, no missing context, no original question to ask about. Everything you need is below: the working directory holds the cached thread content, and your findings request is stated explicitly.

Read threads/%s.md. It is chronological; each message is a "## ISO @user (ts)" heading followed by body text. The first heading is the parent message; subsequent ones are replies.

Findings request: %s

When you make a concrete claim grounded in a specific message, mark it with a numeric citation [N] (e.g. "[1]", "[2]") and add the corresponding entry to the citations block at the end. Reply under 400 words. If the materialised content does not contain the answer, say so directly rather than speculating.

Citation format — REQUIRED. End your response with a block in this exact form:

<<<CITATIONS
[
  {"kind":"slack_thread","channel_id":"%s","thread_ts":"%s"},
  {"kind":"slack_thread","channel_id":"%s","thread_ts":"%s","message_ts":"<reply_ts>"}
]
CITATIONS>>>

Every entry must include "kind":"slack_thread". Each [N] marker in your prose maps to citations[N-1] (1-based). Use thread-level citations (channel_id + thread_ts only) when citing the thread as a whole; add message_ts when pointing at one specific reply.

Self-verify before emitting the citations block: this tool resolved one thread, so cite from %s only. For each candidate message_ts, run Grep on threads/%s.md to confirm the heading exists. Drop any citation whose ts does not appear.`,
		in.ThreadTS, in.DesiredFindings,
		channelID, in.ThreadTS,
		channelID, in.ThreadTS,
		in.ThreadTS, in.ThreadTS)

	parentID := telemetry.CurrentToolID(ctx)
	res, runErr := s.runSubAgentWithCitations(ctx, prompt, parentID, channelID, store)
	if runErr != nil {
		return errorResult(runErr.Error()), summarizeThreadOut{
			Citations:   []citations.Citation{},
			ThreadTS:    in.ThreadTS,
			PromptSent:  prompt,
			RateLimited: rateLimited,
		}, nil
	}
	return nil, summarizeThreadOut{
		ThreadTS:            in.ThreadTS,
		Summary:             res.Summary,
		Citations:           res.Citations,
		CitationsParseError: res.CitationsParseError,
		PromptSent:          prompt,
		TimedOut:            res.TimedOut,
		RateLimited:         rateLimited,
	}, nil
}
