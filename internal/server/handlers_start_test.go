package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// seedPlaybooks populates the handler's meta cache with the given
// playbooks so preflight's playbook validation has a catalog to check.
func seedPlaybooks(a *apiHandlers, pbs map[string]MetaPlaybook) {
	a.metaCache = &metaCache{}
	a.metaCache.set(&Meta{Playbooks: pbs})
}

func postPreflight(t *testing.T, a *apiHandlers, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/preflight", strings.NewReader(body))
	rr := httptest.NewRecorder()
	a.handlePreflight(rr, req)
	return rr
}

func TestPreflight_SelectedPlaybookIsRecordedOnInvestigation(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)
	seedPlaybooks(a, map[string]MetaPlaybook{
		"release_verification": {Source: "plugin", Type: "general", YAML: "id: release_verification\nsymptom: x\nentrypoint: n\nnodes:\n  n: {description: d}\n"},
	})

	rr := postPreflight(t, a, `{"inputs": {}, "playbook": "release_verification"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var dto InvestigationDTO
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &dto))
	assert.Equal(t, "release_verification", dto.Playbook)
	inv := a.manager.Get(dto.ID)
	require.NotNil(t, inv)
	assert.Equal(t, "release_verification", inv.Playbook)
}

func TestPreflight_UnknownPlaybookRejected(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)
	seedPlaybooks(a, map[string]MetaPlaybook{})

	rr := postPreflight(t, a, `{"inputs": {}, "playbook": "nope"}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "nope", "error must name the offending playbook")
}

func TestPreflight_DisabledPlaybookRejected(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)
	seedPlaybooks(a, map[string]MetaPlaybook{
		"off": {Source: "plugin", Type: "general", YAML: "id: off\nactive: false\nsymptom: x\nentrypoint: n\nnodes:\n  n: {description: d}\n"},
	})

	rr := postPreflight(t, a, `{"inputs": {}, "playbook": "off"}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "off")
}

func TestPreflight_LockedPlaybookRejected(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)
	seedPlaybooks(a, map[string]MetaPlaybook{
		"investigation": {Source: "system", Locked: true, Type: "general", YAML: "id: investigation\nsymptom: x\nentrypoint: n\nnodes:\n  n: {description: d}\n"},
	})

	rr := postPreflight(t, a, `{"inputs": {}, "playbook": "investigation"}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "investigation")
}

func TestPreflight_NoPlaybookLeavesInvestigationUnset(t *testing.T) {
	t.Parallel()
	a := newPreflightAPIWithInputsProfile(t)
	// No meta cache seeded: the default path must not consult the catalog.

	rr := postPreflight(t, a, `{"inputs": {}}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var dto InvestigationDTO
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &dto))
	assert.Empty(t, dto.Playbook)
	assert.NotContains(t, rr.Body.String(), `"playbook"`, "unset playbook must be omitted from the wire DTO")
}
