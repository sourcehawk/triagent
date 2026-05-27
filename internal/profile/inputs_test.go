package profile_test

import (
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/internal/profile"
)

func TestRenderPromptKeys_TextInput(t *testing.T) {
	in := profile.InvestigationInput{
		ID:   "x",
		Type: "text",
		PromptKeys: []profile.PromptKey{
			{Key: "k", Value: "{{.value}}", If: `{{ne .value ""}}`},
		},
	}
	out, err := profile.RenderPromptKeys(in, map[string]any{"value": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Key != "k" || out[0].Value != "hello" {
		t.Errorf("got %+v", out)
	}
}

func TestRenderPromptKeys_OmitsOnIfFalse(t *testing.T) {
	in := profile.InvestigationInput{
		ID:   "x",
		Type: "text",
		PromptKeys: []profile.PromptKey{
			{Key: "k", Value: "{{.value}}", If: `{{ne .value ""}}`},
		},
	}
	out, _ := profile.RenderPromptKeys(in, map[string]any{"value": ""})
	if len(out) != 0 {
		t.Errorf("expected empty, got %+v", out)
	}
}

func TestRenderPromptKeys_DerivedValue(t *testing.T) {
	// Mirrors a typical cluster_id input's namespace derivation.
	in := profile.InvestigationInput{
		ID:   "cluster_id",
		Type: "cluster_id",
		PromptKeys: []profile.PromptKey{
			{Key: "cluster-id", Value: "{{.value}}", If: `{{ne .value ""}}`},
			{Key: "cluster-resource-namespace", Value: "{{.value}}-workload", If: `{{ne .value ""}}`},
		},
	}
	out, _ := profile.RenderPromptKeys(in, map[string]any{"value": "abc"})
	if len(out) != 2 || out[1].Value != "abc-workload" {
		t.Errorf("got %+v", out)
	}
}

func TestRenderPromptKeys_SlackChannel(t *testing.T) {
	in := profile.InvestigationInput{
		ID:   "slack",
		Type: "slack_channel",
		PromptKeys: []profile.PromptKey{
			{Key: "id", Value: "{{.id}}", If: `{{ne .id ""}}`},
			{Key: "url", Value: "{{.url}}", If: `{{and (eq .id "") (ne .url "")}}`},
		},
	}
	// Picker-supplied id case.
	out, _ := profile.RenderPromptKeys(in, map[string]any{"id": "C123", "name": "n", "url": ""})
	if len(out) != 1 || out[0].Key != "id" {
		t.Errorf("picker case: %+v", out)
	}
	// URL fallback case.
	out, _ = profile.RenderPromptKeys(in, map[string]any{"id": "", "name": "", "url": "https://example.com"})
	if len(out) != 1 || out[0].Key != "url" {
		t.Errorf("url case: %+v", out)
	}
}

// ── ValidateInputValues ──────────────────────────────────────────────────────

func TestValidateInputValues_UnknownID(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "cluster_id", Type: "cluster_id", Optional: true},
	}
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{
		"bogus_id": {"value": "x"},
	})
	if err == nil {
		t.Fatal("expected error for unknown input id")
	}
	if !strings.Contains(err.Error(), "bogus_id") {
		t.Errorf("error must name the offending id, got: %s", err.Error())
	}
}

func TestValidateInputValues_MissingRequired(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "ticket", Type: "text", Optional: false},
	}
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required input")
	}
	if !strings.Contains(err.Error(), "ticket") {
		t.Errorf("error must name the missing input, got: %s", err.Error())
	}
}

func TestValidateInputValues_EmptyRequiredValue(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "ticket", Type: "text", Optional: false},
	}
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{
		"ticket": {"value": ""},
	})
	if err == nil {
		t.Fatal("expected error for empty required value")
	}
}

func TestValidateInputValues_PresentRequired(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "ticket", Type: "text", Optional: false},
	}
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{
		"ticket": {"value": "INC-42"},
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateInputValues_OptionalMissing(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "cluster_id", Type: "cluster_id", Optional: true},
	}
	// Omitting an optional input entirely is fine.
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{})
	if err != nil {
		t.Errorf("unexpected error for missing optional: %v", err)
	}
}

func TestValidateInputValues_SlackChannelRequired_WithID(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "slack_channel", Type: "slack_channel", Optional: false},
	}
	// id present → valid
	if err := profile.ValidateInputValues(inputs, map[string]map[string]any{
		"slack_channel": {"id": "C123", "name": "foo", "url": ""},
	}); err != nil {
		t.Errorf("unexpected error with id set: %v", err)
	}
}

func TestValidateInputValues_SlackChannelRequired_WithURL(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "slack_channel", Type: "slack_channel", Optional: false},
	}
	// url present, id empty → valid
	if err := profile.ValidateInputValues(inputs, map[string]map[string]any{
		"slack_channel": {"id": "", "url": "https://slack.com/archives/C1"},
	}); err != nil {
		t.Errorf("unexpected error with url set: %v", err)
	}
}

func TestValidateInputValues_SlackChannelRequired_BothEmpty(t *testing.T) {
	inputs := []profile.InvestigationInput{
		{ID: "slack_channel", Type: "slack_channel", Optional: false},
	}
	// both empty → error
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{
		"slack_channel": {"id": "", "url": ""},
	})
	if err == nil {
		t.Fatal("expected error for slack_channel with both empty")
	}
}

func TestValidateInputValues_AllInputsOptional(t *testing.T) {
	// All canonical optional inputs — empty map is valid.
	inputs := []profile.InvestigationInput{
		{ID: "cluster_id", Type: "cluster_id", Optional: true},
		{ID: "incident_url", Type: "url", Optional: true},
		{ID: "slack_channel", Type: "slack_channel", Optional: true},
		{ID: "notes", Type: "textarea", Optional: true},
	}
	err := profile.ValidateInputValues(inputs, map[string]map[string]any{})
	if err != nil {
		t.Errorf("unexpected error for all-optional inputs with empty map: %v", err)
	}
}

