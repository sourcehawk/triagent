package prom

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolSpecs_CoversRegisteredTools(t *testing.T) {
	t.Parallel()
	specs := ToolSpecs()
	got := map[string]bool{}
	for _, s := range specs {
		got[s.Name] = true
		assert.Equal(t, "triagent-prom", s.Server)
	}
	want := []string{"prom_list_metrics", "prom_describe_metric", "prom_query", "prom_recent_value", "prom_query_range"}
	for _, n := range want {
		assert.True(t, got[n], "spec missing %s", n)
	}
}

func TestToolSpecs_DescriptionsAreNeutral(t *testing.T) {
	t.Parallel()
	banned := []string{"incident", "investigator", "production", "zeebe"}
	for _, s := range ToolSpecs() {
		low := strings.ToLower(s.Description)
		for _, b := range banned {
			assert.NotContains(t, low, b, "tool %s description leaks vocabulary: %q", s.Name, b)
		}
	}
}
