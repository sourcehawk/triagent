package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sourcehawk/triagent/prompts"
	"github.com/stretchr/testify/require"
)

func TestInterruptEditorSession_NotFound(t *testing.T) {
	t.Parallel()
	a := newRebindAPI(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/editor-sessions/nope/interrupt", nil)
	req.SetPathValue("id", "nope")
	a.handleInterruptEditorSession(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestInterruptEditorSession_NotStreamingIsConflict(t *testing.T) {
	t.Parallel()
	a := newRebindAPI(t)
	sess := registerEditorSession(t, a, prompts.PlaybookSubject{ID: "p", Version: "HEAD"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/editor-sessions/"+sess.ID+"/interrupt", nil)
	req.SetPathValue("id", sess.ID)
	a.handleInterruptEditorSession(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body)
}
