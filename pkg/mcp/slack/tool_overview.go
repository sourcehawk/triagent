package slack

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// channelOverviewIn carries the channel id the agent wants an overview of.
// Required: every channel-aware tool resolves its own per-call store.
type channelOverviewIn struct {
	ChannelID string `json:"channel_id" jsonschema:"Slack channel id (C…/D…/G…); required. If you only have a name, call slack_get_channel_id first."`
}

type channelOverviewOut struct {
	Channel              ChannelInfo `json:"channel"`
	Bookmarks            []Bookmark  `json:"bookmarks"`
	BookmarksUnavailable bool        `json:"bookmarks_unavailable,omitempty"`
	NewestTS             string      `json:"newest_ts,omitempty"`
	OldestTSSeen         string      `json:"oldest_ts_seen,omitempty"`
	ParentsSeen          int         `json:"parents_seen"`
	ThreadedParents      int         `json:"threaded_parents"`
	DistinctUsers        int         `json:"distinct_users"`
	PeekBoundHit         bool        `json:"peek_bound_hit,omitempty"`
	RateLimited          bool        `json:"rate_limited,omitempty"`
}

func (s *Server) handleChannelOverview(ctx context.Context, _ *mcp.CallToolRequest, in channelOverviewIn) (*mcp.CallToolResult, channelOverviewOut, error) {
	channelID := strings.TrimSpace(in.ChannelID)
	if channelID == "" {
		return errorResult("channel_id is required"), channelOverviewOut{}, nil
	}
	store, err := s.resolveStore(channelID)
	if err != nil {
		return errorResult(err.Error()), channelOverviewOut{}, nil
	}

	info, err := s.client.ConversationsInfo(ctx, channelID)
	if err != nil {
		return errorResult(err.Error()), channelOverviewOut{}, nil
	}

	bookmarks, bmErr := s.client.ConversationsBookmarksList(ctx, channelID)
	bookmarksUnavailable := false
	if bmErr != nil {
		if errors.Is(bmErr, ErrMissingScope) {
			bookmarks = nil
			bookmarksUnavailable = true
		} else {
			return errorResult(bmErr.Error()), channelOverviewOut{}, nil
		}
	}
	if bookmarks == nil {
		bookmarks = []Bookmark{}
	}

	syncRes, err := store.Sync(ctx, syncPeek, 0)
	if err != nil {
		return errorResult(err.Error()), channelOverviewOut{}, nil
	}

	snap := store.Snapshot()
	threaded := 0
	users := map[string]struct{}{}
	for _, m := range snap.Parents {
		if m.ReplyCount > 0 {
			threaded++
		}
		if m.User != "" {
			users[m.User] = struct{}{}
		}
	}
	for _, replies := range snap.Threads {
		for _, m := range replies {
			if m.User != "" {
				users[m.User] = struct{}{}
			}
		}
	}

	return nil, channelOverviewOut{
		Channel:              *info,
		Bookmarks:            bookmarks,
		BookmarksUnavailable: bookmarksUnavailable,
		NewestTS:             syncRes.NewestTS,
		OldestTSSeen:         syncRes.OldestTS,
		ParentsSeen:          syncRes.ParentCount,
		ThreadedParents:      threaded,
		DistinctUsers:        len(users),
		PeekBoundHit:         syncRes.Truncated,
		RateLimited:          syncRes.RateLimited,
	}, nil
}
