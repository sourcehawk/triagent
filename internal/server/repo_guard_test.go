package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readError decodes the {"error": "..."} body that writeError emits, so
// the assertions below can check the message rather than depend on
// recorder.Body raw bytes.
func readError(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "body: %s", string(body))
	return out.Error
}

func TestRequireUpstreamRepo_EmptyReturns400WithGuidance(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	ok := a.requireUpstreamRepo(rr, "playbooks", "")
	require.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	msg := readError(t, rr.Body.Bytes())
	assert.Contains(t, msg, "defaults.playbooks_repo", "message must point at the field to set")
}

func TestRequireUpstreamRepo_NonEmptyReturnsTrue(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	ok := a.requireUpstreamRepo(rr, "wiki", "my-org/wiki")
	require.True(t, ok)
	assert.Equal(t, http.StatusOK, rr.Code, "no response written when repo is set")
}

// handlePushSessionPR must short-circuit with 400 when sessions repo is
// empty, before doing any manager / investigation lookup. Operator running
// on the embedded default profile (no defaults.sessions_repo) would
// otherwise hit a confusing git/gh failure deep in the push goroutine.
func TestHandlePushSessionPR_EmptyRepoGuard(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{manager: NewManager(context.Background(), t.TempDir())}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/abc/push-pr", nil)
	req.SetPathValue("id", "abc")
	a.handlePushSessionPR(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, readError(t, rr.Body.Bytes()), "defaults.sessions_repo")
}

func TestHandlePushPlaybookPR_EmptyRepoGuard(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbooks/foo/push-pr", strings.NewReader(`{"yaml":"x","type":"general"}`))
	req.SetPathValue("id", "foo")
	a.handlePushPlaybookPR(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, readError(t, rr.Body.Bytes()), "defaults.playbooks_repo")
}

func TestHandlePlaybooksUpstreamSync_EmptyRepoGuard(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbooks-upstream/sync", nil)
	a.handlePlaybooksUpstreamSync(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, readError(t, rr.Body.Bytes()), "defaults.playbooks_repo")
}

func TestHandleWikiUpstreamSync_EmptyRepoGuard(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/wiki-upstream/sync", nil)
	a.handleWikiUpstreamSync(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, readError(t, rr.Body.Bytes()), "defaults.wiki_repo")
}

func TestHandleSessionsUpstreamSync_EmptyRepoGuard(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions-upstream/sync", nil)
	a.handleSessionsUpstreamSync(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, readError(t, rr.Body.Bytes()), "defaults.sessions_repo")
}

func TestHandlePushWikiPR_EmptyRepoGuard(t *testing.T) {
	t.Parallel()
	a := &apiHandlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/wiki-proposals/abc/push-pr", strings.NewReader(`{}`))
	req.SetPathValue("id", "abc")
	a.handlePushWikiPR(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, readError(t, rr.Body.Bytes()), "defaults.wiki_repo")
}
