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
	case c == ':':
		// PromQL metric identifiers may contain `:` — the recording-
		// rule convention is `level:metric:operation` (e.g.
		// `job:http_requests:rate5m`). Treating `:` as a non-ident
		// boundary would let a recording rule containing the substring
		// of a high-cardinality raw metric be misclassified as an
		// unscoped reference to that raw metric.
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
//
// If the query already carries a non-__name__ label matcher, scope is
// satisfied regardless of cardinality — return early without probing.
// The probe is expensive on high-fanout upstreams (Thanos against a
// thousands-of-series metric can take many seconds) and serves only
// to decide whether scope is *required*. A query that already has
// scope doesn't need that decision.
func isScoped(ctx context.Context, c *promClient, cat *catalog, q string, after int, name string) (bool, int, error) {
	if hasNonNameMatcher(q, after) {
		cat.mu.Lock()
		est := cat.cardEst[name] // zero when not yet probed; surfaced only for error-message context
		cat.mu.Unlock()
		return true, est, nil
	}
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
	return false, est, nil
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
	end := findClosingBrace(q, i+1)
	if end < 0 {
		return false
	}
	body := q[i+1 : end]
	for _, seg := range splitMatchersQuoteAware(body) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		k := strings.TrimSpace(matcherKey(seg))
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
		open := indexByteFromQuoteAware(q, '{', i)
		if open < 0 {
			return false
		}
		end := findClosingBrace(q, open+1)
		if end < 0 {
			return false
		}
		body := q[open+1 : end]
		hasName, hasOther := false, false
		for _, seg := range splitMatchersQuoteAware(body) {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			k := strings.TrimSpace(matcherKey(seg))
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
		i = end + 1
	}
}

// findClosingBrace returns the absolute index of the first `}` at or
// after `from` in s that is not inside a double-quoted Prometheus label
// value. Returns -1 if no such brace exists. Recognises backslash
// escapes inside quotes so a literal `\"` does not terminate the run.
func findClosingBrace(s string, from int) int {
	inQuote := false
	for i := from; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == '"' {
				inQuote = false
			}
			continue
		}
		if c == '"' {
			inQuote = true
			continue
		}
		if c == '}' {
			return i
		}
	}
	return -1
}

// indexByteFromQuoteAware returns the absolute index of the first
// occurrence of c at or after `from` in s that is not inside a
// double-quoted Prometheus label value. Returns -1 if none.
func indexByteFromQuoteAware(s string, c byte, from int) int {
	inQuote := false
	for i := from; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			if ch == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if ch == '"' {
				inQuote = false
			}
			continue
		}
		if ch == '"' {
			inQuote = true
			continue
		}
		if ch == c {
			return i
		}
	}
	return -1
}

// splitMatchersQuoteAware splits a label-matcher block body on commas
// at the top level — commas inside double-quoted label values are
// preserved as literals so a selector like
// `{__name__=~"container_.*,kube_.*"}` is treated as a single matcher
// rather than two. Recognises backslash escapes inside quotes.
func splitMatchersQuoteAware(body string) []string {
	var out []string
	var sb strings.Builder
	inQuote := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inQuote {
			if c == '\\' && i+1 < len(body) {
				sb.WriteByte(c)
				sb.WriteByte(body[i+1])
				i++
				continue
			}
			if c == '"' {
				inQuote = false
			}
			sb.WriteByte(c)
			continue
		}
		if c == '"' {
			inQuote = true
			sb.WriteByte(c)
			continue
		}
		if c == ',' {
			out = append(out, sb.String())
			sb.Reset()
			continue
		}
		sb.WriteByte(c)
	}
	out = append(out, sb.String())
	return out
}

// matcherKey extracts the label name from a single matcher segment like
// `key="value"`, `key=~"v"`, `key!="v"`, or `key!~"v"`. Returns the
// substring up to the first matcher-operator byte (`=`, `!`, `~`) that
// is not inside a double-quoted value. Operator-like bytes inside the
// quoted value never appear before the opening quote, so a simple
// pre-quote scan is sufficient.
func matcherKey(seg string) string {
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if c == '=' || c == '!' || c == '~' || c == '"' {
			return seg[:i]
		}
	}
	return seg
}
