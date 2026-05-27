package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOverviewStub(t *testing.T, bookmarksBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.info":
			_, _ = w.Write([]byte(`{"ok":true,"channel":{"id":"C1","name":"incident","is_archived":false,"topic":{"value":"t"},"purpose":{"value":"p"},"num_members":3,"created":1700000000}}`))
		case "/conversations.history":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[],"response_metadata":{"next_cursor":""}}`))
		case "/bookmarks.list":
			_, _ = w.Write([]byte(bookmarksBody))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestChannelOverview_IncludesBookmarks(t *testing.T) {
	stub := newOverviewStub(t, `{"ok":true,"bookmarks":[{"title":"Doc","link":"https://x"}]}`)
	defer stub.Close()
	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	_, out, err := srv.handleChannelOverview(context.Background(), nil, channelOverviewIn{ChannelID: "C1"})
	require.NoError(t, err)
	require.Len(t, out.Bookmarks, 1)
	assert.Equal(t, "Doc", out.Bookmarks[0].Title)
	assert.False(t, out.BookmarksUnavailable)
}

func TestChannelOverview_MissingScope_SoftFails(t *testing.T) {
	stub := newOverviewStub(t, `{"ok":false,"error":"missing_scope"}`)
	defer stub.Close()
	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	_, out, err := srv.handleChannelOverview(context.Background(), nil, channelOverviewIn{ChannelID: "C1"})
	require.NoError(t, err)
	assert.Empty(t, out.Bookmarks)
	assert.True(t, out.BookmarksUnavailable, "missing scope must surface as a flag, not a tool error")
	assert.Equal(t, "C1", out.Channel.ID, "channel metadata must still be returned alongside the soft-fail")
}

func TestChannelOverview_RequiresChannelID(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	res, _, err := srv.handleChannelOverview(context.Background(), nil, channelOverviewIn{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "missing channel_id must surface as an error result")
}
