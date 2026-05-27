package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResolveStub(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.conversations" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		_, _ = w.Write([]byte(`{
			"ok": true,
			"channels": [
				{"id":"C100","name":"incidents","is_member":true,"is_archived":false},
				{"id":"C200","name":"oncall","is_member":true,"is_archived":false},
				{"id":"C300","name":"old-incidents","is_member":false,"is_archived":true}
			],
			"response_metadata": {"next_cursor": ""}
		}`))
	}))
	return stub, &calls
}

func TestGetChannelID_ResolvesByName(t *testing.T) {
	stub, _ := newResolveStub(t)
	defer stub.Close()
	srv, err := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})
	require.NoError(t, err)

	_, out, err := srv.handleGetChannelID(context.Background(), nil, getChannelIDIn{Name: "incidents"})
	require.NoError(t, err)
	require.Len(t, out.Matches, 1)
	assert.Equal(t, "C100", out.Matches[0].ID)
	assert.True(t, out.Matches[0].IsMember)
}

func TestGetChannelID_StripsHashAndIsCaseInsensitive(t *testing.T) {
	stub, _ := newResolveStub(t)
	defer stub.Close()
	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	_, out, err := srv.handleGetChannelID(context.Background(), nil, getChannelIDIn{Name: "#Oncall"})
	require.NoError(t, err)
	require.Len(t, out.Matches, 1)
	assert.Equal(t, "C200", out.Matches[0].ID)
}

func TestGetChannelID_NotFound(t *testing.T) {
	stub, _ := newResolveStub(t)
	defer stub.Close()
	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	_, out, err := srv.handleGetChannelID(context.Background(), nil, getChannelIDIn{Name: "nope"})
	require.NoError(t, err)
	assert.True(t, out.NotFound)
	assert.Empty(t, out.Matches)
}

func TestGetChannelID_CachesAcrossCalls(t *testing.T) {
	// Two different lookups should issue users.conversations once total —
	// the second name is populated from the first call's pagination cache.
	stub, calls := newResolveStub(t)
	defer stub.Close()
	srv, _ := New(Options{Token: "xoxp-x", APIBase: stub.URL, CacheDir: t.TempDir()})

	_, _, err := srv.handleGetChannelID(context.Background(), nil, getChannelIDIn{Name: "incidents"})
	require.NoError(t, err)
	_, _, err = srv.handleGetChannelID(context.Background(), nil, getChannelIDIn{Name: "oncall"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), calls.Load(), "second lookup should hit the in-process cache")
}

func TestGetChannelID_RequiresName(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	res, _, err := srv.handleGetChannelID(context.Background(), nil, getChannelIDIn{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}
