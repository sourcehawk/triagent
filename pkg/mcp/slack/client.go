package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is a thin Slack Web API wrapper. It carries a single token and
// returns parsed types; rate-limit handling is surfaced explicitly via
// the rateLimited bool on responses (rather than hidden retries) so the
// agent can decide whether to back off.
type Client struct {
	base   string
	token  string
	http   *http.Client
	userMu sync.Mutex
	users  map[string]*UserSummary // cache: U… → resolved
}

// NewClient builds a Client with a 15s timeout. Caller is responsible
// for keeping it alive for the server's lifetime.
func NewClient(base, token string) *Client {
	return &Client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
		users: map[string]*UserSummary{},
	}
}

// Message is the parent-message subset we surface. Slack's full shape is
// huge; we deliberately project to the fields the wiki-author agent
// actually needs.
type Message struct {
	TS         string `json:"ts"`
	User       string `json:"user,omitempty"`
	Text       string `json:"text,omitempty"`
	Subtype    string `json:"subtype,omitempty"`
	ThreadTS   string `json:"thread_ts,omitempty"`
	ReplyCount int    `json:"reply_count,omitempty"`
}

// UserSummary is the user-resolution shape — display name, real name,
// timezone. We deliberately don't surface emails or phone numbers.
type UserSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	RealName string `json:"real_name,omitempty"`
	TZ       string `json:"tz,omitempty"`
	IsBot    bool   `json:"is_bot,omitempty"`
}

// ChannelInfo summarises one channel. Topics/purposes are
// flattened to strings (Slack returns them as `{value, creator,
// last_set}` triples; we drop the metadata).
//
// We deliberately do NOT surface `is_private`. The operator's xoxp-
// token typically doesn't have `groups:read` scope, which means
// private channels just don't appear in conversations.list at all —
// surfacing the field would be misleading (always false from this
// token's perspective).
type ChannelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsArchived  bool   `json:"is_archived"`
	Topic       string `json:"topic,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	NumMembers  int    `json:"num_members,omitempty"`
	CreatedUnix int64  `json:"created_unix,omitempty"`
}

// Bookmark is one entry from conversations.bookmarks.list. We surface only
// title + link; the full Slack response also has icon_url, type, app_id,
// date_created etc. that the agent doesn't need to reason about.
type Bookmark struct {
	Title string `json:"title"`
	Link  string `json:"link"`
}

// ErrMissingScope is returned when Slack rejects a call because the operator
// token doesn't grant the required OAuth scope. Handlers treat this as a
// soft-fail rather than a hard error so the rest of the response stays useful.
var ErrMissingScope = errors.New("slack: missing_scope")

// ConversationsListEntry is the lookup-only subset of a conversations.list /
// users.conversations entry — the bits slack_get_channel_id surfaces.
type ConversationsListEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsArchived bool   `json:"is_archived"`
	IsMember   bool   `json:"is_member,omitempty"`
}

// UsersConversations pages through the channels the operator's token has
// joined (public + private + DM + group DM), returning lookup-only
// projections. Used by slack_get_channel_id to map a channel name to its
// id without forcing the agent to remember `C…` ids.
//
// Membership-scoped on purpose — listing every public channel in a large
// workspace via conversations.list is slow and frequently scope-restricted
// for xoxp- tokens. For channels the operator isn't joined to, the agent
// falls back to asking the operator for the id directly.
func (c *Client) UsersConversations(ctx context.Context) ([]ConversationsListEntry, error) {
	var out []ConversationsListEntry
	cursor := ""
	for i := 0; i < 10; i++ {
		q := url.Values{}
		q.Set("types", "public_channel,private_channel,mpim,im")
		q.Set("limit", "200")
		q.Set("exclude_archived", "false")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp, err := c.get(ctx, "/users.conversations", q)
		if err != nil {
			return nil, err
		}
		var body struct {
			OK               bool                     `json:"ok"`
			Error            string                   `json:"error,omitempty"`
			Channels         []ConversationsListEntry `json:"channels"`
			ResponseMetadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if !body.OK {
			return nil, fmt.Errorf("users.conversations: %s", body.Error)
		}
		out = append(out, body.Channels...)
		if body.ResponseMetadata.NextCursor == "" {
			break
		}
		cursor = body.ResponseMetadata.NextCursor
	}
	return out, nil
}

// ConversationsBookmarksList calls bookmarks.list. Requires bookmarks:read.
// Returns ErrMissingScope when the token lacks the scope (Slack response
// "ok":false,"error":"missing_scope").
func (c *Client) ConversationsBookmarksList(ctx context.Context, channel string) ([]Bookmark, error) {
	q := url.Values{}
	q.Set("channel", channel)
	resp, err := c.get(ctx, "/bookmarks.list", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		OK        bool       `json:"ok"`
		Error     string     `json:"error,omitempty"`
		Bookmarks []Bookmark `json:"bookmarks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if !body.OK {
		if body.Error == "missing_scope" {
			return nil, ErrMissingScope
		}
		return nil, fmt.Errorf("slack bookmarks.list: %s", body.Error)
	}
	return body.Bookmarks, nil
}

// ConversationsInfo calls conversations.info on the given channel.
func (c *Client) ConversationsInfo(ctx context.Context, channel string) (*ChannelInfo, error) {
	q := url.Values{}
	q.Set("channel", channel)
	q.Set("include_num_members", "true")
	resp, err := c.get(ctx, "/conversations.info", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		Channel struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			IsArchived bool   `json:"is_archived"`
			Topic      struct {
				Value string `json:"value"`
			} `json:"topic"`
			Purpose struct {
				Value string `json:"value"`
			} `json:"purpose"`
			NumMembers int   `json:"num_members"`
			Created    int64 `json:"created"`
		} `json:"channel"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if !body.OK {
		return nil, fmt.Errorf("conversations.info: %s", body.Error)
	}
	return &ChannelInfo{
		ID:          body.Channel.ID,
		Name:        body.Channel.Name,
		IsArchived:  body.Channel.IsArchived,
		Topic:       body.Channel.Topic.Value,
		Purpose:     body.Channel.Purpose.Value,
		NumMembers:  body.Channel.NumMembers,
		CreatedUnix: body.Channel.Created,
	}, nil
}

// ConversationsHistory pages through messages in a channel. SinceUnix
// (oldest), cursor, limit are passed through to Slack as documented.
// Returns the page, the next cursor, and a rateLimited flag set to true
// on a 429 — partial pages are returned on rate-limit so the agent can
// emit them as findings before backing off.
func (c *Client) ConversationsHistory(ctx context.Context, channel, cursor string, limit int, oldestUnix int64) ([]Message, string, bool, error) {
	q := url.Values{}
	q.Set("channel", channel)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if oldestUnix > 0 {
		q.Set("oldest", strconv.FormatInt(oldestUnix, 10))
	}
	resp, err := c.get(ctx, "/conversations.history", q)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, "", true, nil
	}
	var body struct {
		OK               bool      `json:"ok"`
		Error            string    `json:"error,omitempty"`
		Messages         []Message `json:"messages"`
		ResponseMetadata struct {
			NextCursor string `json:"next_cursor"`
		} `json:"response_metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", false, err
	}
	if !body.OK {
		// Slack also returns "ratelimited" as a body error code on
		// some endpoints — surface that the same way.
		if strings.Contains(body.Error, "ratelimited") {
			return nil, "", true, nil
		}
		return nil, "", false, fmt.Errorf("conversations.history: %s", body.Error)
	}
	return body.Messages, body.ResponseMetadata.NextCursor, false, nil
}

// ConversationsReplies fetches all replies to a thread, paginated.
// Returns parent+replies in chronological order.
func (c *Client) ConversationsReplies(ctx context.Context, channel, threadTS, cursor string, limit int) ([]Message, string, bool, error) {
	if threadTS == "" {
		return nil, "", false, errors.New("thread_ts required")
	}
	q := url.Values{}
	q.Set("channel", channel)
	q.Set("ts", threadTS)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	resp, err := c.get(ctx, "/conversations.replies", q)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, "", true, nil
	}
	var body struct {
		OK               bool      `json:"ok"`
		Error            string    `json:"error,omitempty"`
		Messages         []Message `json:"messages"`
		ResponseMetadata struct {
			NextCursor string `json:"next_cursor"`
		} `json:"response_metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", false, err
	}
	if !body.OK {
		if strings.Contains(body.Error, "ratelimited") {
			return nil, "", true, nil
		}
		return nil, "", false, fmt.Errorf("conversations.replies: %s", body.Error)
	}
	return body.Messages, body.ResponseMetadata.NextCursor, false, nil
}

// ResolveUsers fetches profiles for the given user IDs, populating the
// in-process cache. Repeated calls with overlapping IDs hit the cache —
// only previously-unseen IDs round-trip to Slack.
func (c *Client) ResolveUsers(ctx context.Context, ids []string) ([]UserSummary, error) {
	out := make([]UserSummary, 0, len(ids))
	c.userMu.Lock()
	missing := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if u, ok := c.users[id]; ok && u != nil {
			out = append(out, *u)
			continue
		}
		missing = append(missing, id)
	}
	c.userMu.Unlock()

	for _, id := range missing {
		u, err := c.userInfo(ctx, id)
		if err != nil {
			// Cache the missing entry so we don't keep hammering Slack
			// for an id that's been deactivated. Surface a stub.
			stub := UserSummary{ID: id, Name: "(unknown)"}
			c.userMu.Lock()
			c.users[id] = &stub
			c.userMu.Unlock()
			out = append(out, stub)
			continue
		}
		c.userMu.Lock()
		c.users[id] = u
		c.userMu.Unlock()
		out = append(out, *u)
	}
	return out, nil
}

func (c *Client) userInfo(ctx context.Context, id string) (*UserSummary, error) {
	q := url.Values{}
	q.Set("user", id)
	resp, err := c.get(ctx, "/users.info", q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		User  struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			TZ       string `json:"tz"`
			IsBot    bool   `json:"is_bot"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if !body.OK {
		return nil, fmt.Errorf("users.info: %s", body.Error)
	}
	return &UserSummary{
		ID:       body.User.ID,
		Name:     body.User.Name,
		RealName: body.User.RealName,
		TZ:       body.User.TZ,
		IsBot:    body.User.IsBot,
	}, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u = u + "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	return c.http.Do(req)
}
