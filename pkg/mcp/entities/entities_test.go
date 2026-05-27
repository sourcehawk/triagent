package entities

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateNames_AcceptsCanonical(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateNames("services", "wiki_list_entities", []string{"zeebe-broker", "elasticsearch", "operate"}))
}

func TestValidateNames_RejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		hint  string
	}{
		{"spaces", "Zeebe Broker", "zeebe-broker"},
		{"underscore", "zeebe_broker", "zeebe-broker"},
		{"uppercase", "Operate", "operate"},
		{"mixed", "OOM_kill", "oom-kill"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNames("services", "wiki_list_entities", []string{tc.input})
			require.Error(t, err, "expected error for %q", tc.input)
			msg := err.Error()
			assert.Contains(t, msg, tc.hint, "error message missing canonicalisation hint")
			assert.Contains(t, msg, "wiki_list_entities", "error message should mention listSourceTool when provided")
		})
	}
}

func TestValidateNames_NoHintWhenUnsalvageable(t *testing.T) {
	t.Parallel()
	err := ValidateNames("services", "wiki_list_entities", []string{"-broker"})
	require.Error(t, err, "expected error for leading hyphen")
}

func TestValidateNames_OmitsListSourceWhenEmpty(t *testing.T) {
	t.Parallel()
	err := ValidateNames("services", "", []string{"Bad Name"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Use  to enumerate", "empty listSourceTool must not produce a half-rendered hint")
}

func TestNearMatches_PrefersSubstringHits(t *testing.T) {
	t.Parallel()
	got := NearMatches("broker", []string{"zeebe-broker", "operate", "tasklist", "broker-controller"}, 3)
	want := []string{"zeebe-broker", "broker-controller"}
	assert.Equal(t, want, got)
}

func TestNearMatches_LevenshteinFallback(t *testing.T) {
	t.Parallel()
	got := NearMatches("oom-kil", []string{"oom-kill", "elasticsearch", "zeebe-broker"}, 3)
	require.NotEmpty(t, got, "expected oom-kill first")
	assert.Equal(t, "oom-kill", got[0], "expected oom-kill first")
}

func TestNearMatches_DropsUnrelated(t *testing.T) {
	t.Parallel()
	got := NearMatches("shard-stuck", []string{"zeebe-broker", "operate"}, 3)
	assert.Empty(t, got, "expected empty (unrelated)")
}

func TestResolveKeywords_ExactAndNear(t *testing.T) {
	t.Parallel()
	known := map[string][]string{
		"service": {"zeebe-broker", "elasticsearch"},
		"error":   {"oom-kill"},
		"symptom": {"stuck-reconciliation"},
	}
	got := ResolveKeywords(
		[]string{"zeebe-broker", "broker"},
		[]string{"oom-kill"},
		[]string{"completely-unrelated-thing"},
		known,
	)
	require.Len(t, got, 4, "expected 4 resolution entries")
	assert.True(t, got[0].Exact, "entry 0 wrong: %+v", got[0])
	assert.Equal(t, "services", got[0].Field, "entry 0 wrong")
	assert.Equal(t, "zeebe-broker", got[0].Input, "entry 0 wrong")
	assert.False(t, got[1].Exact, "entry 1 should have near=[zeebe-broker]: %+v", got[1])
	assert.Equal(t, "broker", got[1].Input, "entry 1 should have near=[zeebe-broker]")
	require.NotEmpty(t, got[1].Near, "entry 1 should have near=[zeebe-broker]")
	assert.Equal(t, "zeebe-broker", got[1].Near[0], "entry 1 should have near=[zeebe-broker]")
	assert.True(t, got[2].Exact, "entry 2 wrong: %+v", got[2])
	assert.Equal(t, "errors", got[2].Field, "entry 2 wrong")
	assert.False(t, got[3].Exact, "entry 3 should have empty near: %+v", got[3])
	assert.Equal(t, "symptoms", got[3].Field, "entry 3 should have empty near")
	assert.Empty(t, got[3].Near, "entry 3 should have empty near")
}
