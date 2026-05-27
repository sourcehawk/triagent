package citations

import (
	"fmt"
	"regexp"
	"strconv"
)

// markerRE matches the [N] markers a sub-agent emits in prose. A
// post-filter rejects matches preceded by a word character so code
// references like `processors[0]` / `args[1]` aren't read as citation
// markers (Go's regexp has no lookbehind).
var markerRE = regexp.MustCompile(`\[(\d+)\]`)

// MarkerCheck verifies the [N] markers in prose against the citations
// array: every marker must resolve to an entry, and every entry must be
// referenced by at least one marker. Returns one error per problem;
// empty slice means valid.
func MarkerCheck(prose string, citations []Citation) []string {
	var errs []string
	seen := make(map[int]struct{})
	for _, idx := range markerRE.FindAllStringSubmatchIndex(prose, -1) {
		// idx = [matchStart, matchEnd, digitStart, digitEnd]. Skip
		// code-reference syntax: a `[N]` glued to an identifier
		// (preceded by [A-Za-z0-9_]) is not a citation marker. ASCII
		// word chars are single-byte in UTF-8, so a byte-level check
		// is safe even when the preceding rune is multi-byte.
		if idx[0] > 0 {
			prev := prose[idx[0]-1]
			if isWordByte(prev) {
				continue
			}
		}
		n, err := strconv.Atoi(prose[idx[2]:idx[3]])
		if err != nil {
			continue
		}
		if n < 1 || n > len(citations) {
			errs = append(errs, fmt.Sprintf("prose contains marker [%d] but citations array has %d entries", n, len(citations)))
			continue
		}
		seen[n] = struct{}{}
	}
	for i := range citations {
		idx := i + 1
		if _, ok := seen[idx]; !ok {
			errs = append(errs, fmt.Sprintf("citation [%d] is not referenced in the prose (every entry must have at least one [N] marker)", idx))
		}
	}
	return errs
}

func isWordByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}
