package sources

import (
	"context"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/watches"
)

// slackNoiseSubtypes is the set of conversations.history subtypes that
// represent channel housekeeping rather than real content: joins, leaves,
// topic/purpose/name changes, pin/unpin, archive toggles, and edits/
// deletes (we already captured the original message on a prior poll).
// Everything else — including bot_message, file_share, me_message — is
// real content the watch should surface.
var slackNoiseSubtypes = map[string]bool{
	"channel_join":      true,
	"channel_leave":     true,
	"channel_topic":     true,
	"channel_purpose":   true,
	"channel_name":      true,
	"channel_archive":   true,
	"channel_unarchive": true,
	"group_join":        true,
	"group_leave":       true,
	"group_topic":       true,
	"group_purpose":     true,
	"group_name":        true,
	"group_archive":     true,
	"group_unarchive":   true,
	"pinned_item":       true,
	"unpinned_item":     true,
	"bot_add":           true,
	"bot_remove":        true,
	"message_changed":   true,
	"message_deleted":   true,
}

// SlackMessage is the subset of fields we need from conversations.history.
// Keep this struct small so the existing slack client can be adapted or
// re-implemented without breaking the source.
type SlackMessage struct {
	TS        string `json:"ts"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Subtype   string `json:"subtype,omitempty"`
	Permalink string `json:"permalink,omitempty"`
}

// SlackClient is the seam the watches package depends on. Implementations
// live elsewhere (the launcher wires up an adapter over mcp/internal/slack).
type SlackClient interface {
	History(ctx context.Context, channelID, oldestTs string) ([]SlackMessage, error)
}

type Slack struct {
	client SlackClient
}

func NewSlack(client SlackClient) *Slack { return &Slack{client: client} }

func (s *Slack) Kind() watches.SourceKind { return watches.SourceSlackChannel }

func (s *Slack) Poll(ctx context.Context, w watches.Watch, wm watches.WatermarkState) ([]watches.Item, watches.WatermarkState, error) {
	msgs, err := s.client.History(ctx, w.Source.ChannelID, wm.SlackOldestTS)
	if err != nil {
		return nil, wm, err
	}
	out := []watches.Item{}
	maxTS := wm.SlackOldestTS
	for _, m := range msgs {
		// Always drop channel housekeeping. The previous filter dropped
		// every message with a non-empty subtype, which silently ate
		// bot_message / file_share / me_message — i.e. most operational
		// content in a typical monitoring channel.
		if slackNoiseSubtypes[m.Subtype] {
			continue
		}
		// thread_broadcast is the only subtype gated by IncludeThreadReplies.
		// conversations.history already excludes regular thread replies by
		// default; broadcasts are the one case where a reply also lands in
		// the channel timeline.
		if !w.Source.IncludeThreadReplies && m.Subtype == "thread_broadcast" {
			continue
		}
		// Skip messages with no usable text — channel-join system
		// events, file-only uploads, edits that lost their original
		// text, etc. They show up as "(no title)" cards on the
		// detail page and are pure noise for the ingestion agent.
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		if watches.HasSeenTS(wm, m.TS) {
			continue
		}
		out = append(out, watches.Item{
			ID:         newULID(),
			WatchID:    w.ID,
			SourceKind: string(watches.SourceSlackChannel),
			CapturedAt: time.Now().UTC(),
			SourceRef: watches.SourceRef{
				ChannelID: w.Source.ChannelID, TS: m.TS,
			},
			Snapshot: watches.Snapshot{
				Text: truncate(m.Text, 4*1024), Author: m.User, Permalink: m.Permalink,
			},
		})
		wm = watches.RememberSeenTS(wm, m.TS)
		if m.TS > maxTS {
			maxTS = m.TS
		}
	}
	wm.SlackOldestTS = maxTS
	return out, wm, nil
}
