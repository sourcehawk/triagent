package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/editor"
	"github.com/sourcehawk/triagent/prompts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRebindAPI(t *testing.T) *apiHandlers {
	t.Helper()
	root := t.TempDir()
	return &apiHandlers{
		opts:        Options{SessionsRoot: root},
		manager:     NewManager(context.Background(), root),
		editorMgr:   editor.NewManager(context.Background(), root),
		connections: connections.NewWithDir(t.TempDir()),
	}
}

func registerEditorSession(t *testing.T, a *apiHandlers, subject editor.Subject) *editor.Session {
	t.Helper()
	sess, err := a.editorMgr.Register(&editor.Session{Subject: subject})
	require.NoError(t, err)
	return sess
}

func TestRebindEditorSession_Success(t *testing.T) {
	t.Parallel()
	a := newRebindAPI(t)
	sess := registerEditorSession(t, a, prompts.PlaybookSubject{ID: "__new", Version: "HEAD"})

	body := bytes.NewBufferString(`{"kind":"playbook","playbook":{"id":"saved_id","version":"HEAD"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/editor-sessions/"+sess.ID+"/rebind", body)
	req.SetPathValue("id", sess.ID)
	a.handleRebindEditorSession(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	var dto editor.DTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, sess.ID, dto.ID)
	require.NotNil(t, dto.Playbook)
	assert.Equal(t, "saved_id", dto.Playbook.ID)
}

func TestRebindEditorSession_NotFound(t *testing.T) {
	t.Parallel()
	a := newRebindAPI(t)

	body := bytes.NewBufferString(`{"kind":"playbook","playbook":{"id":"x","version":"HEAD"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/editor-sessions/missing/rebind", body)
	req.SetPathValue("id", "missing")
	a.handleRebindEditorSession(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRebindEditorSession_Conflict(t *testing.T) {
	t.Parallel()
	a := newRebindAPI(t)
	sess := registerEditorSession(t, a, prompts.PlaybookSubject{ID: "__new", Version: "HEAD"})
	registerEditorSession(t, a, prompts.PlaybookSubject{ID: "taken", Version: "HEAD"})

	body := bytes.NewBufferString(`{"kind":"playbook","playbook":{"id":"taken","version":"HEAD"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/editor-sessions/"+sess.ID+"/rebind", body)
	req.SetPathValue("id", sess.ID)
	a.handleRebindEditorSession(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestRebindEditorSession_BadKind(t *testing.T) {
	t.Parallel()
	a := newRebindAPI(t)
	sess := registerEditorSession(t, a, prompts.PlaybookSubject{ID: "__new", Version: "HEAD"})

	body := bytes.NewBufferString(`{"kind":"weather","playbook":{"id":"x","version":"HEAD"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/editor-sessions/"+sess.ID+"/rebind", body)
	req.SetPathValue("id", sess.ID)
	a.handleRebindEditorSession(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
