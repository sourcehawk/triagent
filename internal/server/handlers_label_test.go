package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAPIWithInvestigationForLabel(t *testing.T) (*apiHandlers, *Investigation) {
	t.Helper()
	mgr, inv := newTestManagerWithInvestigationForLabel(t)
	return &apiHandlers{manager: mgr}, inv
}

func TestHandleSetLabelInternal_Bearer(t *testing.T) {
	a, inv := newAPIWithInvestigationForLabel(t)
	a.telemetryToken = "tkn"

	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/"+inv.ID+"/label",
		bytes.NewReader([]byte(`{"label":"hello world"}`)))
	req.Header.Set("Authorization", "Bearer tkn")
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()

	a.handleSetLabelFromMCP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%q", rr.Code, rr.Body.String())
	}
	if got := inv.Snapshot().Label; got != "hello world" {
		t.Fatalf("label: %q", got)
	}
}

func TestHandleSetLabelInternal_RejectsBadBearer(t *testing.T) {
	a, inv := newAPIWithInvestigationForLabel(t)
	a.telemetryToken = "tkn"
	req := httptest.NewRequest(http.MethodPost, "/api/internal/investigations/"+inv.ID+"/label",
		bytes.NewReader([]byte(`{"label":"x"}`)))
	req.Header.Set("Authorization", "Bearer wrong")
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()
	a.handleSetLabelFromMCP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestHandleRenameInvestigation_Public(t *testing.T) {
	a, inv := newAPIWithInvestigationForLabel(t)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/label",
		bytes.NewReader([]byte(`{"label":"renamed"}`)))
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()
	a.handleRenameInvestigation(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%q", rr.Code, rr.Body.String())
	}
	if got := inv.Snapshot().Label; got != "renamed" {
		t.Fatalf("label: %q", got)
	}
}

func TestHandleRenameInvestigation_RejectsEmpty(t *testing.T) {
	a, inv := newAPIWithInvestigationForLabel(t)
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/label",
		bytes.NewReader([]byte(`{"label":"   "}`)))
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()
	a.handleRenameInvestigation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "empty") {
		t.Fatalf("body: %s", rr.Body.String())
	}
}

func TestHandleRenameInvestigation_RejectsOverCap(t *testing.T) {
	a, inv := newAPIWithInvestigationForLabel(t)
	body := `{"label":"` + strings.Repeat("a", 81) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/label",
		bytes.NewReader([]byte(body)))
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()
	a.handleRenameInvestigation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
}
