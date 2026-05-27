package citations

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ParseBlock extracts the trailing <<<CITATIONS … CITATIONS>>> block from
// a sub-agent response. Returns:
//   - prose: the response with the block (and surrounding whitespace) stripped.
//     On error paths prose is the original input so the caller can still
//     surface something to the parent agent.
//   - citations: the parsed []Citation when the block is present and valid.
//     An empty body (just whitespace between the markers) parses as zero
//     citations — that's the legitimate "nothing to cite" outcome, not an
//     error.
//   - err: non-nil if the block is missing, unterminated, or contains
//     malformed (non-empty, non-array) JSON.
//
// Contract: even on error, prose is non-empty when the input was, so the
// soft-fail path can surface usable text.
func ParseBlock(raw string) (string, []Citation, error) {
	openIdx := strings.LastIndex(raw, OpenMarker)
	if openIdx < 0 {
		return raw, nil, errors.New("citations block missing: expected <<<CITATIONS … CITATIONS>>>")
	}
	closeIdx := strings.Index(raw[openIdx:], CloseMarker)
	if closeIdx < 0 {
		return raw, nil, errors.New("citations block unterminated: expected CITATIONS>>> after <<<CITATIONS")
	}
	jsonBody := strings.TrimSpace(raw[openIdx+len(OpenMarker) : openIdx+closeIdx])
	prose := strings.TrimRight(raw[:openIdx], " \t\n")

	if jsonBody == "" {
		return prose, []Citation{}, nil
	}

	var cits []Citation
	if err := json.Unmarshal([]byte(jsonBody), &cits); err != nil {
		return prose, nil, fmt.Errorf("citations block: invalid JSON: %w", err)
	}

	return prose, cits, nil
}
