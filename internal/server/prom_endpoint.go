package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// promForwarderAPI is the subset of promforward.Manager the handler
// needs. Lets tests inject a fake without spinning up a real manager.
type promForwarderAPI interface {
	Get(ctx context.Context, investigationID, contextName string) (string, error)
	Stop(investigationID string)
}

// POST /api/internal/prom/{id}/endpoint
// Body: empty.
// Auth: Bearer telemetry token.
//
// Returns {"url": "http://127.0.0.1:NNNNN"} — the current loopback URL
// of the port-forward bound to the investigation's active kubeconfig
// context. Provisions the port-forward lazily on first call.
//
// 503 when no context is bound yet (active-context file missing) or
// when prom is not configured for this launcher.
// 502 when port-forward provisioning fails (target unreachable).
func (a *apiHandlers) handlePromEndpoint(w http.ResponseWriter, r *http.Request) {
	if a.telemetryToken == "" {
		writeError(w, http.StatusServiceUnavailable, "telemetry not configured")
		return
	}
	if !checkBearer(r, a.telemetryToken) {
		writeError(w, http.StatusUnauthorized, "invalid telemetry token")
		return
	}
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation "+id+" not found")
		return
	}
	if a.promForwarder == nil {
		writeError(w, http.StatusServiceUnavailable, "prom not configured for this launcher")
		return
	}
	contextName, err := readActiveContext(inv.SessionDir)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable,
			"no active kubernetes context — call triagent-k8s.switch_context first")
		return
	}
	url, err := a.promForwarder.Get(r.Context(), id, contextName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "port-forward failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// readActiveContext reads <sessionDir>/active-context written by the
// k8s MCP's switchContext handler. Returns the trimmed context name or
// an error when the file is missing / empty.
func readActiveContext(sessionDir string) (string, error) {
	body, err := os.ReadFile(filepath.Join(sessionDir, "active-context"))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(body))
	if name == "" {
		return "", os.ErrNotExist
	}
	return name, nil
}
