package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type analyzeChannelIn struct {
	ChannelID       string `json:"channel_id" jsonschema:"Slack channel id (C…/D…/G…) to analyse; required. If you only have a name, call slack_get_channel_id first."`
	DesiredFindings string `json:"desired_findings" jsonschema:"what the parent agent wants from this channel — frame as concrete findings, not 'summarize'. e.g. 'list every service mentioned as degraded with start/end times', 'which on-callers responded and what actions did each take', 'what links to runbooks or dashboards were shared'"`
	SinceUnix       int64  `json:"since_unix,omitempty" jsonschema:"optional lower bound on message timestamps (Unix seconds); zero means no lower bound. Set this to scope to messages from a known incident-start time onwards."`
}

type analyzeChannelOut struct {
	Summary             string               `json:"summary"`
	Citations           []citations.Citation `json:"citations"`
	CitationsParseError string               `json:"citations_parse_error,omitempty"`
	PromptSent          string               `json:"prompt_sent"`
	ParentCount         int                  `json:"parent_count"`
	Truncated           bool                 `json:"truncated,omitempty"`
	OldestCovered       string               `json:"oldest_covered,omitempty"`
	NewestCovered       string               `json:"newest_covered,omitempty"`
	TimedOut            bool                 `json:"timed_out,omitempty"`
	RateLimited         bool                 `json:"rate_limited,omitempty"`
}

func (s *Server) handleAnalyzeChannel(ctx context.Context, _ *mcp.CallToolRequest, in analyzeChannelIn) (*mcp.CallToolResult, analyzeChannelOut, error) {
	channelID := strings.TrimSpace(in.ChannelID)
	if channelID == "" {
		return errorResult("channel_id is required"), analyzeChannelOut{Citations: []citations.Citation{}}, nil
	}
	if in.DesiredFindings == "" {
		return errorResult("desired_findings is required"), analyzeChannelOut{Citations: []citations.Citation{}}, nil
	}

	store, err := s.resolveStore(channelID)
	if err != nil {
		return errorResult(err.Error()), analyzeChannelOut{Citations: []citations.Citation{}}, nil
	}
	syncRes, err := store.Sync(ctx, syncFull, in.SinceUnix)
	if err != nil {
		return errorResult(err.Error()), analyzeChannelOut{Citations: []citations.Citation{}}, nil
	}

	prompt := fmt.Sprintf(`You are analysing an entire Slack channel. This message is your COMPLETE task — there is no prior conversation, no missing context, no original question to ask about. Everything you need is below: the working directory holds the cached channel content, and your findings request is stated explicitly.

Start by reading messages.md. It is chronological; each message is a "## ISO @user (ts)" heading followed by body text. Threaded parents have a "🧵 N replies → threads/<ts>.md" line — Read those thread files only when the parent looks relevant to the question.

Findings request: %s

When you make a concrete claim grounded in a specific thread or message, mark it with a numeric citation [N] (e.g. "[1]", "[2]") and add the corresponding entry to the citations block at the end. Reply under 600 words. If the materialised content does not contain the answer, say so directly rather than speculating.

Citation format — REQUIRED. End your response with a block in this exact form:

<<<CITATIONS
[
  {"kind":"slack_thread","channel_id":"%s","thread_ts":"<parent_ts>"},
  {"kind":"slack_thread","channel_id":"%s","thread_ts":"<parent_ts>","message_ts":"<reply_ts>"}
]
CITATIONS>>>

Every entry must include "kind":"slack_thread". Each [N] marker in your prose maps to citations[N-1] (1-based). Use thread-level citations (channel_id + thread_ts only) when citing a thread as a whole; add message_ts when pointing at one specific reply.

Self-verify before emitting the citations block: for each candidate thread_ts, Grep messages.md to confirm a heading containing that ts exists. For each candidate message_ts, Grep threads/<thread_ts>.md to confirm the reply heading exists. Drop any citation whose ts does not appear in those files.`,
		in.DesiredFindings,
		channelID,
		channelID)

	parentID := telemetry.CurrentToolID(ctx)
	res, runErr := s.runSubAgentWithCitations(ctx, prompt, parentID, channelID, store)
	if runErr != nil {
		return errorResult(runErr.Error()), analyzeChannelOut{
			Citations:     []citations.Citation{},
			PromptSent:    prompt,
			ParentCount:   syncRes.ParentCount,
			Truncated:     syncRes.Truncated,
			OldestCovered: syncRes.OldestTS,
			NewestCovered: syncRes.NewestTS,
			RateLimited:   syncRes.RateLimited,
		}, nil
	}
	return nil, analyzeChannelOut{
		Summary:             res.Summary,
		Citations:           res.Citations,
		CitationsParseError: res.CitationsParseError,
		PromptSent:          prompt,
		ParentCount:         syncRes.ParentCount,
		Truncated:           syncRes.Truncated,
		OldestCovered:       syncRes.OldestTS,
		NewestCovered:       syncRes.NewestTS,
		TimedOut:            res.TimedOut,
		RateLimited:         syncRes.RateLimited,
	}, nil
}
