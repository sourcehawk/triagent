package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newConnectionsAPI(t *testing.T) *apiHandlers {
	t.Helper()
	dir := t.TempDir()
	return &apiHandlers{
		connections: connections.NewWithDir(dir),
	}
}

func TestGetConnections_DefaultEmpty(t *testing.T) {
	t.Parallel()
	a := newConnectionsAPI(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	a.handleGetConnections(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	var s connections.Status
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	assert.False(t, s.Slack, "want empty")
	assert.False(t, s.Incidentio, "want empty")
}

func TestConnectionsResponseIncludesSlackChannelPrefix(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{Slack: profile.Slack{ChannelPrefix: "inc-"}}

	dir := t.TempDir()
	a := &apiHandlers{
		connections: connections.NewWithDir(dir),
		prof:        prof,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	rr := httptest.NewRecorder()
	a.handleGetConnections(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body)
	var resp struct {
		SlackChannelPrefix string `json:"slack_channel_prefix"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "inc-", resp.SlackChannelPrefix, "slack_channel_prefix must come from profile")
}

func TestPutSlackToken_AcceptsValidToken(t *testing.T) {
	t.Parallel()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/auth.test") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ok": true, "user": "u", "team": "t"}`))
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.slackAPIBase = stub.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/connections/slack", strings.NewReader(`{"token":"xoxp-good"}`))
	a.handlePutSlackToken(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	tok, _ := a.connections.GetSlackToken()
	assert.Equal(t, "xoxp-good", tok, "token not persisted")
}

func TestPutSlackToken_RejectsBadAuth(t *testing.T) {
	t.Parallel()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok": false, "error": "invalid_auth"}`))
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.slackAPIBase = stub.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/connections/slack", strings.NewReader(`{"token":"xoxp-bad"}`))
	a.handlePutSlackToken(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body)
	tok, _ := a.connections.GetSlackToken()
	assert.Empty(t, tok, "token should not be persisted on auth failure")
}

func TestPutSlackToken_RejectsBadPrefix(t *testing.T) {
	t.Parallel()
	// auth.test stub that always returns ok — shouldn't be hit.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.slackAPIBase = stub.URL

	rec := httptest.NewRecorder()
	// auth.test passes (the stub always says ok), but the manager
	// rejects the prefix — we still get 400.
	req := httptest.NewRequest(http.MethodPut, "/api/connections/slack", strings.NewReader(`{"token":"not-slack"}`))
	a.handlePutSlackToken(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPutIncidentioToken_AcceptsValidKey(t *testing.T) {
	t.Parallel()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/identity") {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer io-good" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"identity":{"name":"acme"}}`))
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.incidentioAPIBase = stub.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/connections/incidentio", strings.NewReader(`{"token":"io-good"}`))
	a.handlePutIncidentioToken(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
}

func TestPutIncidentioToken_Rejects401(t *testing.T) {
	t.Parallel()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.incidentioAPIBase = stub.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/connections/incidentio", strings.NewReader(`{"token":"io-bad"}`))
	a.handlePutIncidentioToken(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeleteConnection_ClearsToken(t *testing.T) {
	t.Parallel()
	a := newConnectionsAPI(t)
	_ = a.connections.SetSlackToken("xoxp-x", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/connections/slack", nil)
	req.SetPathValue("kind", "slack")
	a.handleDeleteConnection(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	got, _ := a.connections.GetSlackToken()
	assert.Empty(t, got, "token should be cleared")
}

func TestListSlackChannels_HitsUsersConversationsPublicOnly(t *testing.T) {
	t.Parallel()

	// Reset the package-level cache.
	slackChannels.mu.Lock()
	slackChannels.channels = nil
	slackChannels.tokenHash = ""
	slackChannels.mu.Unlock()

	calls := 0
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/users.conversations", r.URL.Path,
			"must call users.conversations, not conversations.list (regression: missing_scope bug)")
		require.Equal(t, "public_channel", r.URL.Query().Get("types"),
			"types must be public_channel only — never private_channel (regression: missing_scope bug)")
		cursor := r.URL.Query().Get("cursor")
		if cursor == "" {
			_, _ = w.Write([]byte(`{
			  "ok": true,
			  "channels": [{"id":"C1","name":"incidents","created":1700000000}],
			  "response_metadata": {"next_cursor": "page2"}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
		  "ok": true,
		  "channels": [{"id":"C2","name":"oncall","created":1710000000}],
		  "response_metadata": {"next_cursor": ""}
		}`))
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.slackAPIBase = stub.URL
	_ = a.connections.SetSlackToken("xoxp-list", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/slack/channels", nil)
	a.handleListSlackChannels(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	body, _ := io.ReadAll(rec.Body)
	var got []channelDTO
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	assert.Equal(t, "C1", got[0].ID)
	assert.Equal(t, "incidents", got[0].Name)
	assert.Equal(t, int64(1700000000), got[0].CreatedUnix)
	assert.Equal(t, int64(1710000000), got[1].CreatedUnix)
	assert.Equal(t, 2, calls, "want 2 paged calls")
}

func TestListSlackChannels_NoTokenReturns400(t *testing.T) {
	t.Parallel()
	a := newConnectionsAPI(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/slack/channels", nil)
	a.handleListSlackChannels(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSlackChannelURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		workspace string
		channel   string
		want      string
	}{
		{"happy path", "https://example.slack.com", "C0123ABC", "https://example.slack.com/archives/C0123ABC"},
		{"trailing slash trimmed", "https://example.slack.com/", "C0123ABC", "https://example.slack.com/archives/C0123ABC"},
		{"empty workspace", "", "C0123ABC", ""},
		{"empty channel", "https://example.slack.com", "", ""},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SlackChannelURL(tc.workspace, tc.channel))
		})
	}
}

func TestPutSlackToken_PersistsWorkspaceURLFromAuthTest(t *testing.T) {
	t.Parallel()

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/auth.test", r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true,"url":"https://example.slack.com/","team":"example-team","user_id":"U1"}`))
	}))
	defer stub.Close()

	a := newConnectionsAPI(t)
	a.slackAPIBase = stub.URL

	body := strings.NewReader(`{"token":"xoxp-good"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/connections/slack", body)
	a.handlePutSlackToken(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	wsURL, err := a.connections.GetSlackWorkspaceURL()
	require.NoError(t, err)
	assert.Equal(t, "https://example.slack.com", wsURL, "trailing slash must be stripped")
}

func TestGetConnections_IncludesCloudArrayProbedAtRequestTime(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		Cloud: []profile.CloudSource{
			{Alias: "prod-gcp", Provider: "gcp", AssumedIdentity: "ro@p.iam.gserviceaccount.com", Projects: []profile.CloudProject{{ID: "prod-platform", Tags: []string{"prod"}}, {ID: "prod-data"}}},
			{Alias: "prod-aws", Provider: "aws", SourceProfile: "sso-admin", Accounts: []profile.CloudAccount{
				{AccountID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/ro"},
				{AccountID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/ro"},
			}},
		},
	}
	a := &apiHandlers{
		connections: connections.NewWithDir(t.TempDir()),
		prof:        prof,
		cloudProbe: func(_ context.Context, src profile.CloudSource) cloud.IdentityStatus {
			if src.Provider == "gcp" {
				return cloud.IdentityStatus{Provider: "gcp", AssumedIdentity: src.AssumedIdentity, Valid: true}
			}
			return cloud.IdentityStatus{Provider: "aws", Valid: false, Hint: "run: aws sso login"}
		},
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	a.handleGetConnections(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body)

	var resp struct {
		Cloud []struct {
			Alias           string   `json:"alias"`
			Provider        string   `json:"provider"`
			AssumedIdentity string   `json:"assumed_identity"`
			Projects        []string `json:"projects"`
			Accounts        []string `json:"accounts"`
			SourceProfile   string   `json:"source_profile"`
			Valid           bool     `json:"valid"`
			Hint            string   `json:"hint"`
		} `json:"cloud"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Cloud, 2)

	// gcp: the impersonated identity over its allowlisted projects.
	assert.Equal(t, "prod-gcp", resp.Cloud[0].Alias)
	assert.Equal(t, "gcp", resp.Cloud[0].Provider)
	assert.Equal(t, "ro@p.iam.gserviceaccount.com", resp.Cloud[0].AssumedIdentity)
	assert.Equal(t, []string{"prod-platform", "prod-data"}, resp.Cloud[0].Projects)
	assert.Empty(t, resp.Cloud[0].Accounts)
	assert.True(t, resp.Cloud[0].Valid)

	// aws: the SSO base profile over its account set, never a single identity.
	assert.Equal(t, "prod-aws", resp.Cloud[1].Alias)
	assert.Equal(t, "aws", resp.Cloud[1].Provider)
	assert.Empty(t, resp.Cloud[1].AssumedIdentity, "aws has no single assumed identity")
	assert.Empty(t, resp.Cloud[1].Projects)
	assert.Equal(t, []string{"111111111111", "222222222222"}, resp.Cloud[1].Accounts)
	assert.Equal(t, "sso-admin", resp.Cloud[1].SourceProfile)
	assert.False(t, resp.Cloud[1].Valid)
	assert.Equal(t, "run: aws sso login", resp.Cloud[1].Hint)
}

// TestGetConnections_DegradedSource_KeepsConfiguredDetail asserts that when the
// probe fails before resolving anything, each source still renders its
// configured detail so the operator sees what was pinned alongside valid:false
// and the hint: gcp falls back to its configured assumed_identity, and aws shows
// its account set + SSO base (which always come from the profile, not the probe).
func TestGetConnections_DegradedSource_KeepsConfiguredDetail(t *testing.T) {
	t.Parallel()
	prof := &profile.Profile{
		Cloud: []profile.CloudSource{
			{Alias: "prod-gcp", Provider: "gcp", AssumedIdentity: "ro@p.iam.gserviceaccount.com"},
			{Alias: "prod-aws", Provider: "aws", SourceProfile: "sso-admin", Accounts: []profile.CloudAccount{{AccountID: "123456789012", RoleARN: "arn:aws:iam::123456789012:role/ro"}}},
		},
	}
	a := &apiHandlers{
		connections: connections.NewWithDir(t.TempDir()),
		prof:        prof,
		// Probe failed before resolving anything: the status carries only the
		// failure signal, no provider or identity.
		cloudProbe: func(_ context.Context, _ profile.CloudSource) cloud.IdentityStatus {
			return cloud.IdentityStatus{Valid: false, Hint: "re-authenticate"}
		},
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	a.handleGetConnections(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body)

	var resp struct {
		Cloud []struct {
			Alias           string   `json:"alias"`
			Provider        string   `json:"provider"`
			AssumedIdentity string   `json:"assumed_identity"`
			Accounts        []string `json:"accounts"`
			SourceProfile   string   `json:"source_profile"`
			Valid           bool     `json:"valid"`
			Hint            string   `json:"hint"`
		} `json:"cloud"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Cloud, 2)

	assert.Equal(t, "gcp", resp.Cloud[0].Provider, "degraded gcp source falls back to configured provider")
	assert.Equal(t, "ro@p.iam.gserviceaccount.com", resp.Cloud[0].AssumedIdentity, "degraded gcp source falls back to configured identity")
	assert.False(t, resp.Cloud[0].Valid)

	assert.Equal(t, "aws", resp.Cloud[1].Provider, "degraded aws source falls back to configured provider")
	assert.Empty(t, resp.Cloud[1].AssumedIdentity)
	assert.Equal(t, []string{"123456789012"}, resp.Cloud[1].Accounts, "degraded aws source still shows its accounts")
	assert.Equal(t, "sso-admin", resp.Cloud[1].SourceProfile)
	assert.False(t, resp.Cloud[1].Valid)
	assert.Equal(t, "re-authenticate", resp.Cloud[1].Hint)
}

func TestGetConnections_NoCloudSources_OmitsOrEmptyCloud(t *testing.T) {
	t.Parallel()
	a := newConnectionsAPI(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	a.handleGetConnections(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body)

	body := rr.Body.String()
	assert.Contains(t, body, `"cloud":[]`,
		"cloud must serialize as an empty array, never null, so clients can always treat it as an array")

	var resp struct {
		Cloud []json.RawMessage `json:"cloud"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Empty(t, resp.Cloud, "no cloud sources means an empty cloud array")
}
