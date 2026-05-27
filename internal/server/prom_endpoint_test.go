package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/sourcehawk/triagent/internal/promforward"
)

// fakeFwd is a minimal PortForwarder for handler tests.
type fakeFwd struct{ url string }

func (f *fakeFwd) Start(_ context.Context) (string, error) { return f.url, nil }
func (f *fakeFwd) Stop()                                    {}

func newTestPromMgr(url string) *promforward.Manager {
	return promforward.NewManager(promforward.Options{
		Factory: func(*rest.Config, kubernetes.Interface, promforward.Target) (promforward.PortForwarder, error) {
			return &fakeFwd{url: url}, nil
		},
		KubeBuilder: func(_, _ string) (*rest.Config, kubernetes.Interface, error) {
			return &rest.Config{}, nil, nil
		},
		Target: promforward.Target{Service: "prometheus", Namespace: "monitoring", Port: 9090},
	})
}

func TestPromEndpoint_ReturnsForwardURLForActiveContext(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	inv.SessionDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(inv.SessionDir, "active-context"), []byte("cluster-a"), 0o600))

	a := &apiHandlers{
		manager:        mgr,
		telemetryToken: "tok",
		promForwarder:  newTestPromMgr("http://127.0.0.1:60001"),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/internal/prom/"+inv.ID+"/endpoint", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.SetPathValue("id", inv.ID)
	w := httptest.NewRecorder()
	a.handlePromEndpoint(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "http://127.0.0.1:60001", body["url"])
}

func TestPromEndpoint_NoActiveContextFile(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	inv.SessionDir = t.TempDir() // dir exists, but no active-context file
	a := &apiHandlers{
		manager:        mgr,
		telemetryToken: "tok",
		promForwarder:  newTestPromMgr("http://127.0.0.1:0"),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/internal/prom/"+inv.ID+"/endpoint", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.SetPathValue("id", inv.ID)
	w := httptest.NewRecorder()
	a.handlePromEndpoint(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "switch_context")
}

func TestPromEndpoint_RejectsMissingBearer(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	a := &apiHandlers{manager: mgr, telemetryToken: "tok"}
	req := httptest.NewRequest(http.MethodPost, "/api/internal/prom/"+inv.ID+"/endpoint", nil)
	req.SetPathValue("id", inv.ID)
	w := httptest.NewRecorder()
	a.handlePromEndpoint(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPromEndpoint_RejectsWrongBearer(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	a := &apiHandlers{manager: mgr, telemetryToken: "tok"}
	req := httptest.NewRequest(http.MethodPost, "/api/internal/prom/"+inv.ID+"/endpoint", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	req.SetPathValue("id", inv.ID)
	w := httptest.NewRecorder()
	a.handlePromEndpoint(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPromEndpoint_UnknownInvestigation(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManagerWithInvestigationForLabel(t)
	a := &apiHandlers{manager: mgr, telemetryToken: "tok"}
	req := httptest.NewRequest(http.MethodPost, "/api/internal/prom/nope/endpoint", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	a.handlePromEndpoint(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestPromEndpoint_NoForwarderConfigured(t *testing.T) {
	t.Parallel()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	inv.SessionDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(inv.SessionDir, "active-context"), []byte("cluster-a"), 0o600))
	// No promForwarder injected.
	a := &apiHandlers{manager: mgr, telemetryToken: "tok"}
	req := httptest.NewRequest(http.MethodPost, "/api/internal/prom/"+inv.ID+"/endpoint", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.SetPathValue("id", inv.ID)
	w := httptest.NewRecorder()
	a.handlePromEndpoint(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "prom not configured")
}
