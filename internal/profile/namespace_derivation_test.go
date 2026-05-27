package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderNamespaceHints_EmptyConfigReturnsNil(t *testing.T) {
	t.Parallel()
	got := RenderNamespaceHints(NamespaceDerivation{}, map[string]string{"cluster": "x"})
	assert.Empty(t, got)
}

func TestRenderNamespaceHints_TemplateSubstitutes(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{Template: "${project_id}-${component}"}
	got := RenderNamespaceHints(cfg, map[string]string{
		"project_id": "saas-platform-aws",
		"component":  "zeebe",
	})
	assert.Equal(t, []string{"saas-platform-aws-zeebe"}, got)
}

func TestRenderNamespaceHints_TemplateMissingFieldYieldsNothing(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{Template: "${project_id}-${component}"}
	got := RenderNamespaceHints(cfg, map[string]string{"project_id": "x"})
	// component missing -> template leaves ${component} in the output, which we treat as a failed render
	assert.Empty(t, got)
}

func TestRenderNamespaceHints_RulesFirstMatchWins(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${alert_kind} == 'CrossplaneHighNumberOfManagedResourceNotReady'", Template: "crossplane-system"},
			{When: "${project_id} != ''", Template: "${project_id}-zeebe"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{
		"alert_kind": "CrossplaneHighNumberOfManagedResourceNotReady",
		"project_id": "p",
	})
	assert.Equal(t, []string{"crossplane-system"}, got)
}

func TestRenderNamespaceHints_RulesFallthroughToSecond(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${alert_kind} == 'NoMatch'", Template: "nope"},
			{When: "${project_id} != ''", Template: "${project_id}-zeebe"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{
		"alert_kind": "Other",
		"project_id": "saas",
	})
	assert.Equal(t, []string{"saas-zeebe"}, got)
}

func TestRenderNamespaceHints_RulesNoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${alert_kind} == 'NoMatch'", Template: "nope"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{"alert_kind": "Other"})
	assert.Empty(t, got)
}

func TestRenderNamespaceHints_MalformedPredicateDoesNotPanic(t *testing.T) {
	t.Parallel()
	// A predicate with a single quote (unterminated literal) must be
	// rejected, not crash the process. Profile authors can fat-finger
	// the YAML and we shouldn't tip over.
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${field} == '", Template: "should-not-fire"},
			{When: "${field} != ''", Template: "fallback"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{"field": "x"})
	// First rule's predicate is malformed → skip. Second rule matches (field is non-empty) → render.
	assert.Equal(t, []string{"fallback"}, got)
}

func TestRenderNamespaceHints_RuleTemplateFailureStopsEvaluation(t *testing.T) {
	t.Parallel()
	// When a matching rule's template references a missing field, the
	// function returns nil — it does NOT fall through to subsequent rules.
	cfg := NamespaceDerivation{
		Rules: []NamespaceRule{
			{When: "${kind} == 'X'", Template: "${absent}-suffix"},
			{When: "${kind} == 'X'", Template: "would-be-fallback"},
		},
	}
	got := RenderNamespaceHints(cfg, map[string]string{"kind": "X"})
	assert.Empty(t, got, "matched rule with failed template render must return nil, not fall through")
}
