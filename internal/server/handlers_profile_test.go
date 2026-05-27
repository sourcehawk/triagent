package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
)

func testProfileWithInputs() *profile.Profile {
	return &profile.Profile{
		InvestigationInputs: []profile.InvestigationInput{
			{ID: "cluster_id", Label: "Cluster", Type: "cluster_id", Optional: true},
			{ID: "incident_url", Label: "Incident URL", Type: "url", Optional: true},
			{ID: "slack_channel", Label: "Slack channel", Type: "slack_channel", Optional: true},
			{ID: "notes", Label: "Notes", Type: "textarea", Optional: true},
		},
	}
}

func TestHandleProfileInputs(t *testing.T) {
	prof := testProfileWithInputs()
	a := &apiHandlers{prof: prof}

	req := httptest.NewRequest(http.MethodGet, "/api/profile/inputs", nil)
	rr := httptest.NewRecorder()
	a.handleProfileInputs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Inputs []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Type        string `json:"type"`
			Optional    bool   `json:"optional"`
			Placeholder string `json:"placeholder,omitempty"`
			Hint        string `json:"hint,omitempty"`
		} `json:"inputs"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Inputs) == 0 {
		t.Fatal("no inputs returned")
	}
	if resp.Inputs[0].ID != "cluster_id" {
		t.Errorf("first input id=%q, want cluster_id", resp.Inputs[0].ID)
	}
	// Ensure all four canonical inputs are exposed.
	wantIDs := map[string]bool{"cluster_id": false, "incident_url": false, "slack_channel": false, "notes": false}
	for _, in := range resp.Inputs {
		if _, ok := wantIDs[in.ID]; ok {
			wantIDs[in.ID] = true
		}
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Errorf("input %q missing from response", id)
		}
	}
}

func TestHandleProfileInputsRejectsNonGet(t *testing.T) {
	prof := testProfileWithInputs()
	a := &apiHandlers{prof: prof}
	req := httptest.NewRequest(http.MethodPost, "/api/profile/inputs", nil)
	rr := httptest.NewRecorder()
	a.handleProfileInputs(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", rr.Code)
	}
}

func TestHandleProfileInputsNilProfile(t *testing.T) {
	a := &apiHandlers{prof: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/profile/inputs", nil)
	rr := httptest.NewRecorder()
	a.handleProfileInputs(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Inputs []any `json:"inputs"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Inputs) != 0 {
		t.Errorf("expected empty inputs for nil profile, got %d", len(resp.Inputs))
	}
}
