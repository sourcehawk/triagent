package git

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Sentinels present + preamble + trailing commentary is the canonical
// case the prompt is engineered for. The extractor must return only
// the fenced body, trimmed.
func TestExtractSummaryBody_StripsPreambleAndTrailingCommentary(t *testing.T) {
	t.Parallel()
	raw := strings.Join([]string{
		"I have enough context now to produce the architecture summary.",
		"The repo is a Crossplane Configuration package.",
		"",
		"<<<BEGIN_SUMMARY",
		"# example-org/saas-platform-gcp — architecture summary",
		"",
		"## Top-level structure",
		"Crossplane XRDs and Compositions.",
		"SUMMARY>>>",
		"",
		"Hope that helps — let me know if you want me to drill into anything.",
	}, "\n")

	got := extractSummaryBody(raw)

	assert.Equal(t,
		"# example-org/saas-platform-gcp — architecture summary\n\n## Top-level structure\nCrossplane XRDs and Compositions.",
		got,
		"only the fenced body should survive; preamble and trailing commentary must be dropped")
}

// Agents sometimes indent the sentinel line (e.g. inside a list or
// after auto-formatting). The extractor matches on the trimmed line
// content so this still works.
func TestExtractSummaryBody_ToleratesWhitespaceAroundSentinels(t *testing.T) {
	t.Parallel()
	raw := strings.Join([]string{
		"preamble line",
		"   <<<BEGIN_SUMMARY   ",
		"# x/y — architecture summary",
		"\tSUMMARY>>>\t",
		"trailing line",
	}, "\n")

	got := extractSummaryBody(raw)

	assert.Equal(t, "# x/y — architecture summary", got)
}

// BEGIN present but END missing — the agent stopped early or got
// truncated. We should still salvage the body from BEGIN to EOF rather
// than discarding the run.
func TestExtractSummaryBody_BeginOnlyTakesToEOF(t *testing.T) {
	t.Parallel()
	raw := strings.Join([]string{
		"preamble",
		"<<<BEGIN_SUMMARY",
		"# x/y — architecture summary",
		"",
		"## Top-level structure",
		"body content with no closing sentinel",
	}, "\n")

	got := extractSummaryBody(raw)

	assert.Equal(t,
		"# x/y — architecture summary\n\n## Top-level structure\nbody content with no closing sentinel",
		got)
}

// Neither sentinel emitted (legacy / non-compliant generation) but the
// response contains an H1: strip preamble before the H1 as a best-effort
// fallback. Better than caching the raw preamble verbatim.
func TestExtractSummaryBody_NoSentinelsFallsBackToFirstH1(t *testing.T) {
	t.Parallel()
	raw := strings.Join([]string{
		"I have enough context to produce the summary.",
		"Here it is:",
		"",
		"# x/y — architecture summary",
		"",
		"## Top-level structure",
		"…",
	}, "\n")

	got := extractSummaryBody(raw)

	assert.Equal(t,
		"# x/y — architecture summary\n\n## Top-level structure\n…",
		got,
		"missing sentinels should fall back to stripping preamble before the first H1")
}

// Worst case: no sentinels, no H1. Return the raw input trimmed rather
// than dropping the operator's generation on the floor — they can read
// what came back even if it's malformed.
func TestExtractSummaryBody_RawFallbackWhenNothingMatches(t *testing.T) {
	t.Parallel()
	raw := "  the agent produced free prose with no markers at all  \n"

	got := extractSummaryBody(raw)

	assert.Equal(t, "the agent produced free prose with no markers at all", got)
}

// Empty body between sentinels collapses to an empty string. Surfaces
// the empty-generation case to the caller cleanly rather than caching
// a stray newline.
func TestExtractSummaryBody_EmptyBetweenSentinelsReturnsEmpty(t *testing.T) {
	t.Parallel()
	raw := "<<<BEGIN_SUMMARY\n\nSUMMARY>>>\n"

	got := extractSummaryBody(raw)

	assert.Equal(t, "", got)
}
