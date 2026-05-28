package slack

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getChannelIDIn struct {
	Name string `json:"name" jsonschema:"channel name with or without leading '#' (e.g. 'incidents-2026-04-01' or '#incidents-2026-04-01'); case-insensitive exact match; required"`
}

type getChannelIDOut struct {
	Matches  []channelNameMatch `json:"matches"`
	NotFound bool               `json:"not_found,omitempty"`
}

func (s *Server) handleGetChannelID(ctx context.Context, _ *mcp.CallToolRequest, in getChannelIDIn) (*mcp.CallToolResult, getChannelIDOut, error) {
	name := normaliseChannelName(in.Name)
	if name == "" {
		return errorResult("name is required"), getChannelIDOut{}, nil
	}

	// Cache lookup by exact normalised name. Hit returns one match
	// directly; miss falls through to a paged users.conversations walk.
	s.nameMu.Lock()
	if cached, ok := s.nameCache[name]; ok {
		s.nameMu.Unlock()
		return nil, getChannelIDOut{Matches: []channelNameMatch{cached}}, nil
	}
	s.nameMu.Unlock()

	channels, err := s.client.UsersConversations(ctx)
	if err != nil {
		if errors.Is(err, ErrMissingScope) {
			return errorResult("slack token cannot enumerate joined channels: users.conversations requires the channels:read, groups:read, mpim:read and im:read scopes. If the operator already knows the channel id (e.g. from a slack URL like https://camunda.slack.com/archives/C055PP73FQQ), pass that id directly to the other slack tools instead of going through slack_get_channel_id."), getChannelIDOut{}, nil
		}
		return errorResult(err.Error()), getChannelIDOut{}, nil
	}

	matches := make([]channelNameMatch, 0, 1)
	s.nameMu.Lock()
	for _, c := range channels {
		key := normaliseChannelName(c.Name)
		entry := channelNameMatch(c)
		// Populate the cache as we walk so subsequent lookups by other
		// channel names hit memory instead of round-tripping Slack again.
		// Only the first occurrence per normalised name is cached — Slack
		// allows historical name collisions; we surface every match via
		// the response and prefer the first one for cache resolution.
		if _, exists := s.nameCache[key]; !exists {
			s.nameCache[key] = entry
		}
		if key == name {
			matches = append(matches, entry)
		}
	}
	s.nameMu.Unlock()

	if len(matches) == 0 {
		return nil, getChannelIDOut{NotFound: true}, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		// Joined channels first; then non-archived; then by id for
		// stability across calls.
		if matches[i].IsMember != matches[j].IsMember {
			return matches[i].IsMember
		}
		if matches[i].IsArchived != matches[j].IsArchived {
			return !matches[i].IsArchived
		}
		return matches[i].ID < matches[j].ID
	})
	return nil, getChannelIDOut{Matches: matches}, nil
}

// normaliseChannelName strips a leading '#' and lower-cases the result.
// Slack channel names are stored lower-cased so case-insensitive match
// is just lower-case + trim.
func normaliseChannelName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	return strings.ToLower(s)
}
