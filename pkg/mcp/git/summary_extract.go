package git

import "strings"

// summaryBeginMarker / summaryEndMarker bracket the architecture-summary
// body inside the sub-agent's raw response. The prompt instructs the
// agent to emit each on its own line — the extractor matches them as
// trimmed line equality, so stray indentation or whitespace around the
// marker doesn't break extraction. The marker strings are also embedded
// in summary_prompt.md (asserted in tests) so changing them here
// requires updating the prompt in lockstep.
const (
	summaryBeginMarker = "<<<BEGIN_SUMMARY"
	summaryEndMarker   = "SUMMARY>>>"
)

// extractSummaryBody pulls the cache-file body out of a sub-agent's
// raw response.
//
// The prompt asks the agent to wrap its body in
//
//	<<<BEGIN_SUMMARY
//	# <owner>/<name> — architecture summary
//	…
//	END_SUMMARY>>>
//
// sentinels so any preamble ("I have enough context to produce the
// summary…") or trailing commentary ("let me know if you need…") can be
// stripped before the body lands in the cache file. When both sentinels
// are present we return the content between them, trimmed.
//
// Resilience fallbacks, in order:
//   - Begin present, end missing: take from the line after BEGIN to EOF.
//     The agent stopped early but the body is still salvageable.
//   - Both sentinels missing but the response contains an `# ` H1: take
//     from the first H1 to EOF. Strips preamble heuristically.
//   - No sentinels and no H1: return the raw response, trimmed. The body
//     is probably malformed but cache-writing the raw text beats
//     dropping the operator's generation on the floor.
//
// All return paths trim surrounding whitespace.
func extractSummaryBody(raw string) string {
	lines := strings.Split(raw, "\n")
	beginIdx := indexOfTrimmedLine(lines, summaryBeginMarker)
	if beginIdx < 0 {
		return fallbackToFirstH1(lines, raw)
	}
	endIdx := indexOfTrimmedLineFrom(lines, summaryEndMarker, beginIdx+1)
	if endIdx < 0 {
		return strings.TrimSpace(strings.Join(lines[beginIdx+1:], "\n"))
	}
	return strings.TrimSpace(strings.Join(lines[beginIdx+1:endIdx], "\n"))
}

// indexOfTrimmedLine returns the first index i where strings.TrimSpace(lines[i]) == marker,
// or -1 if none match.
func indexOfTrimmedLine(lines []string, marker string) int {
	return indexOfTrimmedLineFrom(lines, marker, 0)
}

func indexOfTrimmedLineFrom(lines []string, marker string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker {
			return i
		}
	}
	return -1
}

// fallbackToFirstH1 strips preamble before the first `# ` line. When no
// H1 exists, returns the raw input trimmed.
func fallbackToFirstH1(lines []string, raw string) string {
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(raw)
}
