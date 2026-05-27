package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationsBookmarksList_OK(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bookmarks.list", r.URL.Path)
		assert.Equal(t, "C1", r.URL.Query().Get("channel"))
		_, _ = w.Write([]byte(`{"ok":true,"bookmarks":[
			{"title":"Incident doc","link":"https://docs.example/incident-42"},
			{"title":"Status page","link":"https://status.example"}
		]}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL, "xoxp-x")
	got, err := c.ConversationsBookmarksList(context.Background(), "C1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Incident doc", got[0].Title)
	assert.Equal(t, "https://docs.example/incident-42", got[0].Link)
}

func TestConversationsBookmarksList_MissingScope(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL, "xoxp-x")
	_, err := c.ConversationsBookmarksList(context.Background(), "C1")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingScope, "missing_scope must be a sentinel so handlers can distinguish it from generic errors")
}
