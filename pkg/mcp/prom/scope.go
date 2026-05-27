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
	// Walk catalog metric names longest-first so a longer name doesn't
	// get masked by a shorter prefix in the substring scan (e.g.
	// "http_requests_total" before "http_requests").
	names := append([]string(nil), cat.names...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	// remaining tracks the spans of the query still eligible for a
	// metric-name hit. We carve out matched spans as we go so the same
	// substring isn't double-counted.
	type span struct{ lo, hi int }
	spans := []span{{0, len(q)}}

	for _, name := range names {
		var nextSpans []span
		for _, sp := range spans {
			rest := q[sp.lo:sp.hi]
			idx := 0
			for {
				rel := strings.Index(rest[idx:], name)
				if rel < 0 {
					break
				}
				abs := sp.lo + idx + rel
				if !isStandaloneIdentifier(q, abs, name) {
					idx += rel + 1
					continue
				}
				scoped, err := isScoped(ctx, c, cat, q, abs+len(name), name)
				if err != nil {
					return err
				}
				if !scoped {
					est := cat.cardEst[name]
					return fmt.Errorf(
						"scope required for high-cardinality metric %q (cardinality estimate %s). Add at least one non-__name__ label matcher, e.g. namespace=\"...\", service=\"...\", job=\"...\". See prom_describe_metric for typical scope keys.",
						name, formatCard(est),
					)
				}
				idx += rel + len(name)
			}
			// Anything we didn't carve out stays eligible for shorter
			// names later in the loop (no-op carving in v1; simplifies
			// the loop without losing correctness).
			nextSpans = append(nextSpans, sp)
		}
		spans = nextSpans
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
// returns true if a `{...}` block follows that contains at least one
// matcher with a key other than `__name__`. When the metric is below
// the scope-required threshold (and known), scope is not required.
func isScoped(ctx context.Context, c *promClient, cat *catalog, q string, after int, name string) (bool, error) {
	cat.mu.Lock()
	est, ok := cat.cardEst[name]
	cat.mu.Unlock()
	if !ok {
		if c == nil {
			est = 0 // treat as low-card in tests that don't wire a client
		} else {
			v, err := cardinalityOf(ctx, c, cat, name)
			if err != nil {
				return false, err
			}
			est = v
		}
	}
	if est >= 0 && est < scopeRequiredThreshold {
		return true, nil
	}
	// est == -1 (high-card sentinel) OR est >= threshold → require scope.
	return hasNonNameMatcher(q, after), nil
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
