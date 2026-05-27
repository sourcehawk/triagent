package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// LauncherSlackAdapter implements the watches sources.SlackClient
// interface by talking to slack.com/api/conversations.history directly.
//
// Why a small re-implementation: the mcp/ module is a separate Go module
// from investigate/, and the slack helper lives under
// mcp/internal/slack — Go's internal-package rule keeps it private to
// the mcp module. The watches package only needs the History call, so a
// 60-line adapter is cheaper than restructuring two modules.
type LauncherSlackAdapter struct {
	apiBase    string
	token      string
	httpClient *http.Client

	// Workspace URL discovered via auth.test on first use, then cached
	// for the lifetime of the adapter. Used to construct message
	// permalinks locally; calling chat.getPermalink per message would
	// hit Slack's tier-4 rate limit (20/min) on backfills.
	wsMu  sync.Mutex
	wsURL string // e.g. "https://example.slack.com/" (trailing slash)

	// User display-name cache. Slack's conversations.history returns
	// only user IDs; resolving each via users.info on demand and caching
	// is far cheaper than batching (we'd need to know all IDs up front).
	// Tier 4 (100/min) is plenty for typical poll volumes.
	usersMu   sync.Mutex
	users     map[string]string // userID → display name
	usersMiss map[string]bool   // userID → previously failed; don't retry every poll
}

// PerPollMessageCap bounds how much one poll can pull from a chatty
// channel; keeps a misconfigured cadence from fan-out-loading thousands
// of messages on first connection.
const PerPollMessageCap = 1000

// PerPagePulledMessages is the slack API page size we ask for. Slack's
// own ceiling is 999; 200 is a comfortable middle ground.
const PerPagePulledMessages = 200

// NewLauncherSlackAdapter constructs an adapter with sensible defaults.
// Pass apiBase="" to use https://slack.com/api (the only sane default).
func NewLauncherSlackAdapter(apiBase, token string) *LauncherSlackAdapter {
	if apiBase == "" {
		apiBase = "https://slack.com/api"
	}
	return &LauncherSlackAdapter{
		apiBase:    apiBase,
		token:      token,
		httpClient: http.DefaultClient,
		users:      map[string]string{},
		usersMiss:  map[string]bool{},
	}
}

// slackHistoryResponse maps the subset of conversations.history we care
// about. Slack returns much more (response_metadata, has_more,
// pin_count, etc.); we only project what the watches feature needs.
type slackHistoryResponse struct {
	OK               bool           `json:"ok"`
	Error            string         `json:"error,omitempty"`
	Messages         []slackMessage `json:"messages"`
	HasMore          bool           `json:"has_more"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type slackMessage struct {
	TS      string `json:"ts"`
	User    string `json:"user"`
	Text    string `json:"text"`
	Subtype string `json:"subtype,omitempty"`
	// Bot integrations (alertmanager, monitoring, etc.) post with an
	// empty top-level Text and the real payload in Attachments[] (legacy
	// "secondary content") or Blocks[] (Block Kit, Slack's modern
	// surface). slackMessageText synthesizes a usable body from these
	// when Text is empty.
	Attachments []slackAttachment `json:"attachments,omitempty"`
	Blocks      []slackBlock      `json:"blocks,omitempty"`
}

type slackAttachment struct {
	Title    string `json:"title,omitempty"`
	Pretext  string `json:"pretext,omitempty"`
	Text     string `json:"text,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// slackBlock captures the subset of Block Kit fields that carry text.
// Slack's block model is recursive (rich_text > elements > elements),
// so we keep both `text` (section/header) and `elements` (rich_text).
type slackBlock struct {
	Type     string           `json:"type"`
	Text     *slackBlockText  `json:"text,omitempty"`
	Elements []slackBlockElem `json:"elements,omitempty"`
}

type slackBlockText struct {
	Text string `json:"text"`
}

type slackBlockElem struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	Elements []slackBlockElem `json:"elements,omitempty"`
}

// slackMessageText returns a usable plain-text body for `m`. Prefers the
// top-level `text` when set; otherwise concatenates whatever readable
// content is reachable from `attachments[]` and `blocks[]`. Returns ""
// when nothing usable was found — that's the poller's signal to drop
// the message as "no content".
func slackMessageText(m slackMessage) string {
	if t := strings.TrimSpace(m.Text); t != "" {
		return t
	}
	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	for _, a := range m.Attachments {
		add(a.Title)
		add(a.Pretext)
		add(a.Text)
		// fallback is only useful when no richer field is present; it
		// typically duplicates Title + a short label.
		if a.Title == "" && a.Text == "" && a.Pretext == "" {
			add(a.Fallback)
		}
	}
	for _, b := range m.Blocks {
		collectBlockText(b, &parts)
	}
	return strings.Join(parts, "\n")
}

func collectBlockText(b slackBlock, out *[]string) {
	if b.Text != nil {
		if t := strings.TrimSpace(b.Text.Text); t != "" {
			*out = append(*out, t)
		}
	}
	for _, e := range b.Elements {
		collectBlockElemText(e, out)
	}
}

func collectBlockElemText(e slackBlockElem, out *[]string) {
	if t := strings.TrimSpace(e.Text); t != "" {
		*out = append(*out, t)
	}
	for _, c := range e.Elements {
		collectBlockElemText(c, out)
	}
}

func (a *LauncherSlackAdapter) History(ctx context.Context, channelID, oldestTs string) ([]SlackMessage, error) {
	oldestUnix := parseSlackTSToUnix(oldestTs)
	wsURL, _ := a.workspaceURL(ctx) // best-effort: empty wsURL just means no permalinks
	out := make([]SlackMessage, 0, PerPagePulledMessages)
	cursor := ""
	for {
		page, nextCursor, hasMore, err := a.fetchOnePage(ctx, channelID, cursor, oldestUnix)
		if err != nil {
			return nil, err
		}
		for _, m := range page {
			out = append(out, SlackMessage{
				TS:        m.TS,
				User:      a.resolveUserName(ctx, m.User),
				Text:      slackMessageText(m),
				Subtype:   m.Subtype,
				Permalink: buildSlackPermalink(wsURL, channelID, m.TS),
			})
			if len(out) >= PerPollMessageCap {
				return out, nil
			}
		}
		if !hasMore || nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return out, nil
}

// workspaceURL returns the operator's Slack workspace URL (with
// trailing slash). Cached on the adapter after the first successful
// auth.test call. Empty return means we couldn't resolve it; callers
// treat that as "permalinks unavailable" and skip the field.
func (a *LauncherSlackAdapter) workspaceURL(ctx context.Context) (string, error) {
	a.wsMu.Lock()
	cached := a.wsURL
	a.wsMu.Unlock()
	if cached != "" {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.apiBase+"/auth.test", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("slack auth.test: status %d", resp.StatusCode)
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		URL   string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if !body.OK {
		return "", fmt.Errorf("slack auth.test: %s", body.Error)
	}
	ws := body.URL
	if ws != "" && !strings.HasSuffix(ws, "/") {
		ws += "/"
	}
	a.wsMu.Lock()
	a.wsURL = ws
	a.wsMu.Unlock()
	return ws, nil
}

// resolveUserName turns a Slack user ID like "U01B60N5VA5" into the
// operator-visible display name (falling back to real_name and finally
// to the raw ID if everything fails). Cached for the adapter lifetime
// — typical channels have <50 unique posters and users.info is cheap.
// Previously-failed lookups are remembered too so a deactivated user
// doesn't trigger a fresh API call on every poll.
func (a *LauncherSlackAdapter) resolveUserName(ctx context.Context, userID string) string {
	if userID == "" {
		return ""
	}
	a.usersMu.Lock()
	if name, ok := a.users[userID]; ok {
		a.usersMu.Unlock()
		return name
	}
	if a.usersMiss[userID] {
		a.usersMu.Unlock()
		return userID
	}
	a.usersMu.Unlock()

	name, err := a.fetchUserName(ctx, userID)
	a.usersMu.Lock()
	defer a.usersMu.Unlock()
	if err != nil || name == "" {
		a.usersMiss[userID] = true
		return userID
	}
	a.users[userID] = name
	return name
}

func (a *LauncherSlackAdapter) fetchUserName(ctx context.Context, userID string) (string, error) {
	q := url.Values{}
	q.Set("user", userID)
	req, err := http.NewRequestWithContext(ctx, "GET", a.apiBase+"/users.info?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("slack users.info: status %d", resp.StatusCode)
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		User  struct {
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			Profile  struct {
				DisplayName string `json:"display_name"`
				RealName    string `json:"real_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if !body.OK {
		return "", fmt.Errorf("slack users.info: %s", body.Error)
	}
	// Prefer display_name (the @-handle the operator uses in Slack),
	// fall back to real_name (the legal/full name), then to the
	// username, and finally bail.
	if n := strings.TrimSpace(body.User.Profile.DisplayName); n != "" {
		return n, nil
	}
	if n := strings.TrimSpace(body.User.Profile.RealName); n != "" {
		return n, nil
	}
	if n := strings.TrimSpace(body.User.RealName); n != "" {
		return n, nil
	}
	if n := strings.TrimSpace(body.User.Name); n != "" {
		return n, nil
	}
	return "", nil
}

// buildSlackPermalink constructs the canonical message URL Slack itself
// uses ("https://<team>.slack.com/archives/<CID>/p<ts_no_dot>"). Skips
// when the workspace URL hasn't been resolved yet.
func buildSlackPermalink(wsURL, channelID, ts string) string {
	if wsURL == "" || channelID == "" || ts == "" {
		return ""
	}
	// "1715420822.000100" → "1715420822000100"
	tsNoDot := strings.ReplaceAll(ts, ".", "")
	return wsURL + "archives/" + channelID + "/p" + tsNoDot
}

func (a *LauncherSlackAdapter) fetchOnePage(ctx context.Context, channelID, cursor string, oldestUnix int64) ([]slackMessage, string, bool, error) {
	q := url.Values{}
	q.Set("channel", channelID)
	q.Set("limit", strconv.Itoa(PerPagePulledMessages))
	if oldestUnix > 0 {
		q.Set("oldest", strconv.FormatInt(oldestUnix, 10))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.apiBase+"/conversations.history?"+q.Encode(), nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("slack history: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, "", false, fmt.Errorf("slack history: status %d", resp.StatusCode)
	}
	var body slackHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", false, fmt.Errorf("decode slack history: %w", err)
	}
	if !body.OK {
		return nil, "", false, fmt.Errorf("slack history: %s", body.Error)
	}
	return body.Messages, body.ResponseMetadata.NextCursor, body.HasMore, nil
}

// parseSlackTSToUnix takes "1715420822.000100" → 1715420822. Returns 0
// when the input is empty or unparseable (slack treats 0 as "no oldest"
// → pull from the start).
func parseSlackTSToUnix(ts string) int64 {
	if ts == "" {
		return 0
	}
	if dot := strings.IndexByte(ts, '.'); dot >= 0 {
		ts = ts[:dot]
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
