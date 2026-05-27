package prom

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// checkScope walks the PromQL string, finds each referenced metric name
// from the catalog, and rejects when a high-cardinality metric appears
// without at least one non-`__name__` label matcher.
//
// We do not parse PromQL. Substring-scan + a small follow-token window
// is enough to cover the realistic cases (single matchers, regex
// matchers, multi-matcher braces) without a full parser. False
// positives (rejecting a scoped query) are surfaced clearly — the
// agent reissues with explicit scope.
func checkScope(ctx context.Context, c *promClient, cat *catalog, promql string) error {
	q := strings.TrimSpace(promql)
	if q == "" {
		return fmt.Errorf("promql is required")
	}
	// Guard against {__name__=...} selectors that bypass the catalog-
	// substring scan entirely. A bare __name__ matcher (with no other
	// label matchers in the same block) can reference arbitrary metrics
	// via regex and would never appear as a literal substring.
	if hasUnscopedNameMatcher(q) {
		return fmt.Errorf("scope required: a {__name__=...} matcher must be paired with at least one non-__name__ label matcher; pure __name__ selectors can target arbitrary metrics and bypass the cardinality gate")
	}
	// Walk catalog metric names longest-first so a longer name (e.g.
	// "http_requests_total") isn't masked by a shorter prefix
	// ("http_requests") in the substring scan.
	names := append([]string(nil), cat.names...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	for _, name := range names {
		idx := 0
		for {
			rel := strings.Index(q[idx:], name)
			if rel < 0 {
				break
			}
			abs := idx + rel
			if !isStandaloneIdentifier(q, abs, name) {
				idx = abs + 1
				continue
			}
			scoped, est, err := isScoped(ctx, c, cat, q, abs+len(name), name)
			if err != nil {
				return err
			}
			if !scoped {
				return fmt.Errorf(
					"scope required for high-cardinality metric %q (cardinality estimate %s): add at least one non-__name__ label matcher, e.g. namespace=\"...\", service=\"...\", job=\"...\"; see prom_describe_metric for typical scope keys",
					name, formatCard(est),
				)
			}
			idx = abs + len(name)
		}
	}
	return nil
}

// isStandaloneIdentifier returns true if `name` at position `at` in `q`
// is bounded by characters that are not valid in a Prom metric
// identifier (so we don't match "go_goroutines" inside "ago_goroutines").
func isStandaloneIdentifier(q string, at int, name string) bool {
	if at > 0 {
		c := q[at-1]
		if isIdentChar(c) {
			return false
		}
	}
	if at+len(name) < len(q) {
		c := q[at+len(name)]
		if isIdentChar(c) {
			return false
		}
	}
	return true
}

func isIdentChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '_':
		return true
	default:
		return false
	}
}

// isScoped looks at the chars immediately after a metric-name match and
// returns (scoped, cardinalityEstimate, err). The est is surfaced so
// callers building error messages don't need to re-read catalog state
// (which would race against concurrent probes). When the metric is
// below the scope-required threshold (and known), scope is not required.
func isScoped(ctx context.Context, c *promClient, cat *catalog, q string, after int, name string) (bool, int, error) {
	cat.mu.Lock()
	est, ok := cat.cardEst[name]
	cat.mu.Unlock()
	if !ok {
		if c == nil {
			est = 0 // treat as low-card in tests that don't wire a client
		} else {
			v, err := cardinalityOf(ctx, c, cat, name)
			if err != nil {
				return false, 0, err
			}
			est = v
		}
	}
	if est >= 0 && est < scopeRequiredThreshold {
		return true, est, nil
	}
	// est == -1 (high-card sentinel) OR est >= threshold → require scope.
	return hasNonNameMatcher(q, after), est, nil
}

// hasNonNameMatcher returns true if a `{...}` block immediately after
// the given position contains at least one key=... where key != __name__.
// Whitespace between the name and the brace is ignored.
func hasNonNameMatcher(q string, after int) bool {
	i := after
	for i < len(q) && (q[i] == ' ' || q[i] == '\t' || q[i] == '\n' || q[i] == '\r') {
		i++
	}
	if i >= len(q) || q[i] != '{' {
		return false
	}
	end := strings.IndexByte(q[i:], '}')
	if end < 0 {
		return false
	}
	body := q[i+1 : i+end]
	// Split on commas at the top level (Prom label values are quoted
	// strings — commas inside quotes don't break us in practice; we
	// keep it simple and rely on whether any segment passes the test).
	for _, seg := range strings.Split(body, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		key := strings.FieldsFunc(seg, func(r rune) bool {
			return r == '=' || r == '!' || r == '~'
		})
		if len(key) == 0 {
			continue
		}
		k := strings.TrimSpace(key[0])
		if k != "" && k != "__name__" {
			return true
		}
	}
	return false
}

func formatCard(est int) string {
	if est == -1 {
		return "high (≥ probe limit)"
	}
	if est == 0 {
		return "unknown"
	}
	return fmt.Sprintf("≥ %d series", est)
}

// hasUnscopedNameMatcher returns true if any {...} block in q contains
// a __name__ matcher (=, =~, !=, !~) without at least one non-__name__
// matcher in the same block. Used to reject regex-based name selectors
// like {__name__=~"container_.*"} that would otherwise bypass the
// catalog-substring scope check.
func hasUnscopedNameMatcher(q string) bool {
	i := 0
	for {
		open := indexByteFrom(q, '{', i)
		if open < 0 {
			return false
		}
		close := indexByteFrom(q, '}', open)
		if close < 0 {
			return false
		}
		body := q[open+1 : close]
		hasName, hasOther := false, false
		for _, seg := range strings.Split(body, ",") {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			key := strings.FieldsFunc(seg, func(r rune) bool {
				return r == '=' || r == '!' || r == '~'
			})
			if len(key) == 0 {
				continue
			}
			k := strings.TrimSpace(key[0])
			if k == "" {
				continue
			}
			if k == "__name__" {
				hasName = true
			} else {
				hasOther = true
			}
		}
		if hasName && !hasOther {
			return true
		}
		i = close + 1
	}
}

// indexByteFrom is strings.IndexByte starting at offset from. Returns
// an absolute index, or -1.
func indexByteFrom(s string, c byte, from int) int {
	if from >= len(s) {
		return -1
	}
	rel := strings.IndexByte(s[from:], c)
	if rel < 0 {
		return -1
	}
	return from + rel
}
