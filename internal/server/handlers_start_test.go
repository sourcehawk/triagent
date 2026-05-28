package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
)

// newPreflightAPIWithInputsProfile returns an apiHandlers whose prof field is
// populated from an inline profile with the canonical four optional inputs,
// so tests don't depend on any embedded profile file.
func newPreflightAPIWithInputsProfile(t *testing.T) *apiHandlers {
	t.Helper()
	prof := &profile.Profile{
		InvestigationInputs: []profile.InvestigationInput{
			{ID: "cluster_id", Label: "Cluster", Type: "cluster_id", Optional: true},
			{ID: "incident_url", Label: "Incident URL", Type: "url", Optional: true},
			{ID: "slack_channel", Label: "Slack channel", Type: "slack_channel", Optional: true},
			{ID: "notes", Label: "Notes", Type: "textarea", Optional: true,
				PromptKeys: []profile.PromptKey{
					{Key: "operator-notes", Value: "{{.value}}", If: `{{ne .value ""}}`},
				}},
		},
	}
	sessionsRoot := t.TempDir()
	manager := NewManager(context.Background(), sessionsRoot)
	t.Cleanup(manager.Shutdown)
	// Drain per-session persistence goroutines before t.TempDir cleanup
	// runs, or RemoveAll races writes into session-*/ and fails with
	// "directory not empty".
	t.Cleanup(manager.Shutdown)
	return &apiHandlers{
		opts:        Options{SessionsRoot: sessionsRoot},
		manager:     manager,
		connections: connections.NewWithDir(t.TempDir()),
		prof:        prof,
		preflightFn: func(_ preflight.Options) (*preflight.Result, error) {
			return &preflight.Result{
				MCPConfigPath: "/dev/null",
				DocsPrefix:    "",
			}, nil
		},
	}
}

// TestPreflight_AcceptsInputsMap verifies that the handler accepts a
// generic inputs map keyed by input ID.
func TestPreflight_AcceptsInputsMap(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)

	body := strings.NewReader(`{
		"inputs": {
			"cluster_id":    {"value": "abc"},
			"incident_url":  {"value": "https://example.com/inc"},
			"slack_channel": {"id": "C1", "name": "inc-foo", "url": ""},
			"notes":         {"value": "something looks wrong"}
		},
		"auto": false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	rr := httptest.NewRecorder()
	a.handlePreflight(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rr.Code, rr.Body.String())
	}
	invs := a.manager.List()
	if len(invs) != 1 {
		t.Fatalf("want 1 investigation, got %d", len(invs))
	}
	got := invs[0]
	if got.SlackChannelID != "C1" {
		t.Errorf("SlackChannelID=%q, want C1", got.SlackChannelID)
	}
	if got.SlackChannelName != "inc-foo" {
		t.Errorf("SlackChannelName=%q, want inc-foo", got.SlackChannelName)
	}
	if got.IncidentURL != "https://example.com/inc" {
		t.Errorf("IncidentURL=%q, want https://example.com/inc", got.IncidentURL)
	}
	if got.Notes != "something looks wrong" {
		t.Errorf("Notes=%q, want 'something looks wrong'", got.Notes)
	}
}

// TestPreflight_RejectsUnknownInputID verifies that an unknown input id
// causes a 400 with the offending id named in the error.
func TestPreflight_RejectsUnknownInputID(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)

	body := strings.NewReader(`{"inputs": {"bogus_id": {"value": "x"}}, "auto": false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	rr := httptest.NewRecorder()
	a.handlePreflight(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "bogus_id") {
		t.Errorf("error must name the offending input: %s", rr.Body.String())
	}
}

// TestPreflight_InputsMap_EmptyIsOK verifies that an empty inputs map is
// accepted when all profile inputs are optional.
func TestPreflight_InputsMap_EmptyIsOK(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)

	body := strings.NewReader(`{"inputs": {}, "auto": false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", body)
	rr := httptest.NewRecorder()
	a.handlePreflight(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rr.Code, rr.Body.String())
	}
}
