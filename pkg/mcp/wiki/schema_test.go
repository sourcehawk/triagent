package wiki

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		slug  string
		valid bool
	}{
		{"inc-12345-broker-ooms", true},
		{"inv-broker-rebalance", true},
		{"alert-cpu-spike", true},
		{"a", true},
		{"zeebe-broker-oom", true},
		{"INC-1-x", false},         // uppercase
		{"-leading-hyphen", false}, // bad first char
		{"trailing-", false},       // trailing hyphen
		{"has space", false},
		{"", false},
	}
	for _, c := range cases {
		err := ValidateSlug(c.slug)
		got := err == nil
		assert.Equal(t, c.valid, got, "ValidateSlug(%q) = %v, want valid=%v", c.slug, err, c.valid)
	}
}

func TestValidateFrontmatter_RequiredFields(t *testing.T) {
	t.Parallel()
	good := Frontmatter{
		ID:       "inc-12345-broker-ooms",
		Date:     "2026-04-12",
		Title:    "Zeebe broker OOMs during reconciliation",
		Status:   "resolved",
		Services: []string{"zeebe-broker"},
		Errors:   []string{"oom-kill"},
		Symptoms: []string{"stuck-reconciliation"},
		Links:    Links{Investigation: "http://localhost:8080/investigations/?id=sess-abc"},
	}
	require.Empty(t, ValidateFrontmatter(good), "expected good frontmatter to validate")

	missing := good
	missing.Title = ""
	errs := ValidateFrontmatter(missing)
	require.NotEmpty(t, errs, "expected missing title to fail validation")
	assert.True(t, containsSubstr(errs, "title is required"), "expected 'title is required' error, got %v", errs)

	badStatus := good
	badStatus.Status = "in-flight"
	require.NotEmpty(t, ValidateFrontmatter(badStatus), "expected invalid status to fail validation")
}

func containsSubstr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
