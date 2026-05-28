package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUsersConversations_MissingScopeReturnsSentinel pins the contract that
// users.conversations surfaces missing_scope via the ErrMissingScope
// sentinel so callers can branch on it — matching the bookmarks.list and
// channel-overview handling. Without the sentinel the agent only sees the
// raw "users.conversations: missing_scope" string and can't tell a setup
// gap from a transient API failure.
func TestUsersConversations_MissingScopeReturnsSentinel(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/users.conversations", r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL, "xoxp-x")
	_, err := c.UsersConversations(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingScope)
}
