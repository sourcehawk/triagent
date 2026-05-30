package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers"
)

// Connection-management endpoints. The panel reads /api/connections to
// render the Slack and incident.io status pills, and PUT /api/connections/{kind}
// to store a token after validating it against the upstream service.
//
// The endpoints never echo a stored token back — Status only carries booleans.

// connectionsResponse extends connections.Status with profile-sourced boot
// config. The frontend fetches this once on mount and caches the result so
// profile values (like slack_channel_prefix) don't require a separate
// endpoint.
type connectionsResponse struct {
	connections.Status
	SlackChannelPrefix string            `json:"slack_channel_prefix"`
	Cloud              []cloudConnection `json:"cloud"`
}

// cloudConnection is the read-only view of one profile cloud source: its alias,
// the pinned identity, and the request-time probe result. The alias keys the
// triagent-cloud-<alias> MCP and distinguishes two sources that share a
// provider and identity but differ in scope. It carries no edit affordance —
// cloud is configured in the profile, never entered in the panel.
type cloudConnection struct {
	Alias           string `json:"alias"`
	Provider        string `json:"provider"`
	AssumedIdentity string `json:"assumed_identity"`
	Valid           bool   `json:"valid"`
	Hint            string `json:"hint,omitempty"`
}

// connectionsResp builds the full response body for all /api/connections
// endpoints, merging connection status with profile boot config and the
// request-time cloud identity probe.
func (a *apiHandlers) connectionsResp(ctx context.Context) connectionsResponse {
	var prefix string
	if a.prof != nil {
		prefix = a.prof.Slack.ChannelPrefix
	}
	return connectionsResponse{
		Status:             a.connections.Status(),
		SlackChannelPrefix: prefix,
		Cloud:              a.cloudConnections(ctx),
	}
}

// cloudConnections probes each profile cloud source at request time and projects
// the result into the read-only panel view. Returns nil when no profile or no
// cloud sources are configured. The probe degrades, never blocks: an invalid
// source still appears, with its hint, so the operator can fix a stale
// credential before starting a session.
func (a *apiHandlers) cloudConnections(ctx context.Context) []cloudConnection {
	if a.prof == nil || len(a.prof.Cloud) == 0 {
		return nil
	}
	probe := a.cloudProbe
	if probe == nil {
		probe = defaultCloudProbe
	}
	out := make([]cloudConnection, 0, len(a.prof.Cloud))
	for _, src := range a.prof.Cloud {
		st := probe(ctx, src)
		// A probe that fails before resolving an identity leaves Provider and
		// AssumedIdentity blank. Fall back to the configured source values so a
		// degraded source still shows WHICH identity was pinned alongside its
		// failure hint; the alias is always the source's.
		provider := st.Provider
		if provider == "" {
			provider = src.Provider
		}
		identity := st.AssumedIdentity
		if identity == "" {
			identity = src.AssumedIdentity
		}
		out = append(out, cloudConnection{
			Alias:           src.Alias,
			Provider:        provider,
			AssumedIdentity: identity,
			Valid:           st.Valid,
			Hint:            st.Hint,
		})
	}
	return out
}

// defaultCloudProbe is the real request-time prober: it maps a profile cloud
// source to the providers package's neutral Source and runs ProbeSource, which
// constructs the provider and shells its whoami CLI, degrading a construction
// error to an invalid status.
func defaultCloudProbe(ctx context.Context, src profile.CloudSource) cloud.IdentityStatus {
	return providers.ProbeSource(ctx, providers.Source{
		Provider:        src.Provider,
		AssumedIdentity: src.AssumedIdentity,
		Profile:         src.Profile,
	})
}

// handleGetConnections returns which integrations have a usable token, plus
// profile boot config (slack_channel_prefix) and the request-time cloud
// identity probe.
//
// GET /api/connections
func (a *apiHandlers) handleGetConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, a.connectionsResp(r.Context()))
}

// handlePutSlackToken validates a Slack token via auth.test, then persists
// it. Validation runs before the Manager.SetSlackToken call so we never
// store a token that won't authenticate.
//
// PUT /api/connections/slack  body: {"token": "xoxp-..."}
func (a *apiHandlers) handlePutSlackToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse body: %v", err))
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	workspaceURL, err := verifySlackToken(a.slackAPIBaseOrDefault(), body.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("slack auth.test failed: %v", err))
		return
	}
	if err := a.connections.SetSlackToken(body.Token, workspaceURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.connectionsResp(r.Context()))
}

// handlePutIncidentioToken validates an incident.io API key by calling
// /v1/identity, then persists it.
//
// PUT /api/connections/incidentio  body: {"token": "..."}
func (a *apiHandlers) handlePutIncidentioToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse body: %v", err))
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	if err := verifyIncidentioToken(a.incidentioAPIBaseOrDefault(), body.Token); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("incidentio identity check failed: %v", err))
		return
	}
	if err := a.connections.SetIncidentioToken(body.Token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.connectionsResp(r.Context()))
}

// handleDeleteConnection clears the token for the given kind.
//
// DELETE /api/connections/{kind}
func (a *apiHandlers) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kind := connections.Kind(r.PathValue("kind"))
	if err := a.connections.Clear(kind); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.connectionsResp(r.Context()))
}

// channelDTO is the redacted shape returned by /api/slack/channels: just
// what the picker needs to render.
//
// CreatedUnix lets the picker default the wiki modal's `since` field to
// the channel creation time when the operator picks a channel.
//
// is_private is intentionally absent: the listing path is public-channels-only
// by design (see spec 2026-05-08-slack-channel-picker-unification-design.md).
type channelDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CreatedUnix int64  `json:"created_unix,omitempty"`
}

// slackChannelsCache caches users.conversations output across requests.
// Slack's rate limits on this endpoint are tight (Tier 2: ~20 rpm), so a
// short TTL absorbs the modal opening repeatedly without round-tripping.
type slackChannelsCache struct {
	mu        sync.Mutex
	at        time.Time
	channels  []channelDTO
	tokenHash string // detect token rotation; doesn't store the token itself
}

const slackChannelsTTL = 30 * time.Second

var slackChannels = &slackChannelsCache{}

// handleListSlackChannels proxies users.conversations with the operator's
// stored token. Paginated client-side (up to 1000 channels) so the picker
// gets a single flat list. Cached for 30s.
//
// GET /api/slack/channels
func (a *apiHandlers) handleListSlackChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tok, err := a.connections.GetSlackToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tok == "" {
		writeError(w, http.StatusBadRequest, "slack not connected — link a token first")
		return
	}

	channels, err := slackChannels.fetch(a.slackAPIBaseOrDefault(), tok)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (c *slackChannelsCache) fetch(apiBase, token string) ([]channelDTO, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hash := tokenFingerprint(token)
	if c.tokenHash == hash && time.Since(c.at) < slackChannelsTTL && c.channels != nil {
		return c.channels, nil
	}
	out, err := slackUsersConversations(apiBase, token)
	if err != nil {
		return nil, err
	}
	c.channels = out
	c.at = time.Now()
	c.tokenHash = hash
	return out, nil
}

// tokenFingerprint is a non-reversible cache key. Same length as a SHA1
// hex but doesn't actually round-trip the token through any hashing — we
// just want to detect a different token; never reveal anything.
func tokenFingerprint(token string) string {
	if len(token) < 12 {
		return token
	}
	return token[:6] + "…" + token[len(token)-4:]
}

// slackUsersConversations paginates users.conversations, returning the
// public channels the token's owner is a member of. Joined-only by design
// (see spec 2026-05-08-slack-channel-picker-unification-design.md): an
// active operator's joined-channel list is naturally bounded (typically
// well under the 1000-channel cap) and avoids the missing_scope failure
// mode that conversations.list hit when groups:read was missing.
func slackUsersConversations(apiBase, token string) ([]channelDTO, error) {
	var out []channelDTO
	cursor := ""
	for i := 0; i < 5; i++ {
		page, next, err := slackUsersConversationsPage(apiBase, token, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

func slackUsersConversationsPage(apiBase, token, cursor string) ([]channelDTO, string, error) {
	u, err := url.Parse(apiBase + "/users.conversations")
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	q.Set("types", "public_channel")
	q.Set("limit", "200")
	q.Set("exclude_archived", "true")
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()

	resp, err := slackHTTPGet(u.String(), token)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error,omitempty"`
		Channels []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Created int64  `json:"created"`
		} `json:"channels"`
		ResponseMetadata struct {
			NextCursor string `json:"next_cursor"`
		} `json:"response_metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", err
	}
	if !body.OK {
		return nil, "", fmt.Errorf("users.conversations: %s", body.Error)
	}
	out := make([]channelDTO, 0, len(body.Channels))
	for _, c := range body.Channels {
		out = append(out, channelDTO{ID: c.ID, Name: c.Name, CreatedUnix: c.Created})
	}
	return out, body.ResponseMetadata.NextCursor, nil
}

// verifySlackToken calls auth.test and returns the workspace URL on success.
// auth.test requires no scope and is the standard "what does this token resolve to"
// call — every healthy Slack token can call it.
func verifySlackToken(apiBase, token string) (workspaceURL string, err error) {
	if token == "" {
		return "", errors.New("token required")
	}
	resp, err := slackHTTPGet(apiBase+"/auth.test", token)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		URL   string `json:"url,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode auth.test: %w", err)
	}
	if !body.OK {
		return "", fmt.Errorf("auth.test: %s", body.Error)
	}
	return strings.TrimRight(body.URL, "/"), nil
}

// verifyIncidentioToken calls /v1/identity. A 200 means the key is valid;
// any other status (including 401) is a rejection.
func verifyIncidentioToken(apiBase, token string) error {
	if token == "" {
		return errors.New("token required")
	}
	req, err := http.NewRequest(http.MethodGet, apiBase+"/v1/identity", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func slackHTTPGet(rawURL, token string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}

func (a *apiHandlers) slackAPIBaseOrDefault() string {
	if a.slackAPIBase != "" {
		return a.slackAPIBase
	}
	return "https://slack.com/api"
}

func (a *apiHandlers) incidentioAPIBaseOrDefault() string {
	if a.incidentioAPIBase != "" {
		return a.incidentioAPIBase
	}
	return "https://api.incident.io"
}

// SlackChannelURL returns the canonical Slack archives URL for a channel,
// or empty string if either input is empty. Used by the start-investigation
// handler to derive a slack_link from a picker selection (channel ID +
// persisted workspace URL).
func SlackChannelURL(workspaceURL, channelID string) string {
	if workspaceURL == "" || channelID == "" {
		return ""
	}
	return strings.TrimRight(workspaceURL, "/") + "/archives/" + channelID
}
