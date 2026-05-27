package slack

import (
	"context"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchIn struct {
	ChannelID string `json:"channel_id" jsonschema:"Slack channel id (C…/D…/G…); required. If you only have a name, call slack_get_channel_id first."`
	Query     string `json:"query" jsonschema:"substring (case-insensitive) to match against message text; required"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max hits to return; default 50, max 200"`
	SinceUnix int64  `json:"since_unix,omitempty" jsonschema:"optional lower bound on message timestamps (Unix seconds); zero means no lower bound. Set this to scope to messages from a known incident-start time onwards."`
}

type searchOut struct {
	Hits         []SearchHit `json:"hits"`
	Truncated    bool        `json:"truncated,omitempty"`
	PartialIndex bool        `json:"partial_index,omitempty"`
	RateLimited  bool        `json:"rate_limited,omitempty"`
}

// SearchHit is one matching message. ThreadTS is set when the hit lives
// inside a thread (i.e. is not the parent of the conversation tree).
type SearchHit struct {
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	User        string `json:"user,omitempty"`
	TextSnippet string `json:"text_snippet"`
}

func (s *Server) handleSearchMessages(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
	channelID := strings.TrimSpace(in.ChannelID)
	if channelID == "" {
		return errorResult("channel_id is required"), searchOut{}, nil
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return errorResult("query is required"), searchOut{}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	store, err := s.resolveStore(channelID)
	if err != nil {
		return errorResult(err.Error()), searchOut{}, nil
	}
	syncRes, err := store.Sync(ctx, syncFull, in.SinceUnix)
	if err != nil {
		return errorResult(err.Error()), searchOut{}, nil
	}

	needle := strings.ToLower(query)
	snap := store.Snapshot()

	type stamped struct {
		hit SearchHit
		key string // sort key — the parent ts (or its own ts if it's a parent) so thread hits cluster with their parent
	}
	var matches []stamped

	for _, m := range snap.Parents {
		if strings.Contains(strings.ToLower(m.Text), needle) {
			matches = append(matches, stamped{
				hit: SearchHit{TS: m.TS, User: m.User, TextSnippet: snippet(m.Text, query)},
				key: m.TS,
			})
		}
	}
	for parentTS, replies := range snap.Threads {
		for _, m := range replies {
			// Skip the parent — it's already in snap.Parents above.
			if m.TS == parentTS {
				continue
			}
			if strings.Contains(strings.ToLower(m.Text), needle) {
				matches = append(matches, stamped{
					hit: SearchHit{TS: m.TS, ThreadTS: parentTS, User: m.User, TextSnippet: snippet(m.Text, query)},
					key: parentTS,
				})
			}
		}
	}

	// Chronological ordering, parent-cluster preserved.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].key != matches[j].key {
			return tsLess(matches[i].key, matches[j].key)
		}
		return tsLess(matches[i].hit.TS, matches[j].hit.TS)
	})

	truncated := false
	if len(matches) > limit {
		matches = matches[:limit]
		truncated = true
	}

	out := searchOut{
		Truncated:    truncated,
		PartialIndex: syncRes.Truncated,
		RateLimited:  syncRes.RateLimited,
		Hits:         make([]SearchHit, 0, len(matches)),
	}
	for _, m := range matches {
		out.Hits = append(out.Hits, m.hit)
	}
	return nil, out, nil
}

// snippet returns up to ~120 chars of context around the first match,
// case-insensitively. If the match isn't found (shouldn't happen — the
// caller already matched), the head of the text is returned.
func snippet(text, query string) string {
	const radius = 60
	idx := strings.Index(strings.ToLower(text), strings.ToLower(query))
	if idx < 0 {
		if len(text) <= 2*radius {
			return text
		}
		return text[:2*radius] + "…"
	}
	start := idx - radius
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + radius
	if end > len(text) {
		end = len(text)
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out = out + "…"
	}
	return out
}
