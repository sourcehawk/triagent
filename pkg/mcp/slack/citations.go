package slack

import (
	"context"
	"fmt"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
)

// slackValidator is the per-call existence checker for slack_thread
// citations. It carries the resolved channel id (the agent passed via
// channel_id) and a snapshot of that channel's cache; anything outside
// the resolved channel is rejected.
type slackValidator struct {
	channelID string
	snap      Snapshot
}

func (v *slackValidator) Validate(cits []citations.Citation) []string {
	parentTSSet := make(map[string]struct{}, len(v.snap.Parents))
	for _, m := range v.snap.Parents {
		parentTSSet[m.TS] = struct{}{}
	}

	var errs []string
	for i, c := range cits {
		idx := i + 1
		if c.Kind != citations.KindSlackThread {
			errs = append(errs, fmt.Sprintf("[%d] this MCP only supports slack_thread citations (got %q)", idx, c.Kind))
			continue
		}
		if c.ChannelID != v.channelID {
			errs = append(errs, fmt.Sprintf("[%d] channel_id %q does not match the channel this call resolved (%q)", idx, c.ChannelID, v.channelID))
		}
		_, threadIsParent := parentTSSet[c.ThreadTS]
		_, threadHasReplies := v.snap.Threads[c.ThreadTS]
		if !threadIsParent && !threadHasReplies {
			errs = append(errs, fmt.Sprintf("[%d] thread_ts %q does not exist in the materialised cache (verify against messages.md before citing)", idx, c.ThreadTS))
			continue
		}
		if c.MessageTS != "" && c.MessageTS != c.ThreadTS {
			replies := v.snap.Threads[c.ThreadTS]
			found := false
			for _, m := range replies {
				if m.TS == c.MessageTS {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Sprintf("[%d] message_ts %q is not a reply under thread_ts %q", idx, c.MessageTS, c.ThreadTS))
			}
		}
	}
	return errs
}

// runSubAgentWithCitations invokes the slack sub-agent through the shared
// citations runner. The caller passes the resolved channel id + store so
// the validator and the sub-agent's working directory are wired to the
// right per-call cache.
func (s *Server) runSubAgentWithCitations(ctx context.Context, prompt, parentToolID, channelID string, store *Store) (subAgentResult, error) {
	workingDir := store.Dir()
	// sessionID threads claude's conversation id from the first sub-agent
	// turn into the citation-correction retry — keeps the retry on the
	// same Claude session so it doesn't re-pay the cost of orientation
	// in the channel cache.
	var sessionID string
	adapter := func(ctx context.Context, p string) (citations.RawResult, error) {
		res, err := s.runSubAgent(ctx, p, parentToolID, workingDir, sessionID)
		if err != nil {
			return citations.RawResult{}, err
		}
		if res.SessionID != "" {
			sessionID = res.SessionID
		}
		return citations.RawResult{Raw: res.Summary, TimedOut: res.TimedOut}, nil
	}
	v := &slackValidator{channelID: channelID, snap: store.Snapshot()}
	out, err := citations.Run(ctx, citations.RunInput{Run: adapter, Validator: v, Prompt: prompt})
	if err != nil {
		return subAgentResult{}, err
	}
	return subAgentResult{
		Summary:             out.Prose,
		Citations:           out.Citations,
		CitationsParseError: out.CitationsParseError,
		TimedOut:            out.TimedOut,
	}, nil
}
