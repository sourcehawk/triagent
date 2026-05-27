package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchMessages_CaseInsensitiveSubstringHits(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"300.000100","user":"U1","text":"503 errors on prod","reply_count":2,"thread_ts":"300.000100"},
				{"ts":"200.000100","user":"U2","text":"all clear"},
				{"ts":"100.000100","user":"U3","text":"got 503"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"300.000100","user":"U1","text":"503 errors on prod"},
				{"ts":"310.000100","user":"U2","text":"More 503s incoming"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `"}}`))
		}
	}))
	defer stub.Close()

	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})
	_, out, err := srv.handleSearchMessages(context.Background(), nil, searchIn{ChannelID: "C1", Query: "503"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(out.Hits), 2, "want >= 2 hits for '503', got %+v", out.Hits)
	for _, h := range out.Hits {
		assert.Contains(t, strings.ToLower(h.TextSnippet), "503", "snippet %q missing query", h.TextSnippet)
	}
}

func TestSearchMessages_RequiresChannelID(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	res, _, err := srv.handleSearchMessages(context.Background(), nil, searchIn{Query: "503"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

func TestSearchMessages_RequiresQuery(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	res, _, err := srv.handleSearchMessages(context.Background(), nil, searchIn{ChannelID: "C1"})
	require.NoError(t, err)
	require.NotNil(t, res, "want error result for missing query")
	assert.True(t, res.IsError, "want error result for missing query")
}
