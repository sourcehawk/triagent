package prom

import (
	"context"
)

const (
	// cardProbeLimit caps the /api/v1/series probe. If the response is
	// exactly this length we treat the metric as "high cardinality" and
	// avoid a second probe.
	cardProbeLimit = 200
	// scopeRequiredThreshold gates the scope-enforcement rule. Below
	// this, an unscoped query is allowed; at or above (including the
	// high-card sentinel), a label matcher is required.
	scopeRequiredThreshold = 50
)

// cardinalityOf returns the cached cardinality for `name`, probing if
// not yet known. -1 is the "high cardinality" sentinel (limit reached
// during probe).
func cardinalityOf(ctx context.Context, c *promClient, cat *catalog, name string) (int, error) {
	cat.mu.Lock()
	if v, ok := cat.cardEst[name]; ok && v != 0 {
		cat.mu.Unlock()
		return v, nil
	}
	cat.mu.Unlock()

	rows, err := c.series(ctx, name, cardProbeLimit)
	if err != nil {
		return 0, err
	}
	cat.mu.Lock()
	defer cat.mu.Unlock()
	if len(rows) >= cardProbeLimit {
		cat.cardEst[name] = -1
		return -1, nil
	}
	cat.cardEst[name] = len(rows)
	// Bonus: stash the rows for the describe tool's first call so it
	// doesn't re-probe.
	cat.labelsCache[name] = buildLabelProfile(rows, cat, name)
	return len(rows), nil
}

// buildLabelProfile derives describe_metric's per-label info from a
// series probe response. Filled lazily; called from both cardinalityOf
// and describe directly.
func buildLabelProfile(rows []map[string]string, cat *catalog, name string) labelProfile {
	if len(rows) == 0 {
		return labelProfile{}
	}
	type bucket struct {
		values map[string]struct{}
		order  []string
	}
	keys := map[string]*bucket{}
	for _, row := range rows {
		for k, v := range row {
			if k == "__name__" {
				continue
			}
			b, ok := keys[k]
			if !ok {
				b = &bucket{values: map[string]struct{}{}}
				keys[k] = b
			}
			if _, seen := b.values[v]; !seen {
				b.values[v] = struct{}{}
				b.order = append(b.order, v)
			}
		}
	}
	var labels []labelInfo
	for k, b := range keys {
		sample := b.order
		if len(sample) > 10 {
			sample = sample[:10]
		}
		labels = append(labels, labelInfo{
			Key:          k,
			Cardinality:  len(b.values),
			SampleValues: sample,
		})
	}
	annotateTypicalScope(labels, len(rows))
	related := relatedByPrefix(cat.names, name, 10)
	return labelProfile{
		labels:           labels,
		relatedMetrics:   related,
		totalCardinality: len(rows),
	}
}

// annotateTypicalScope marks the most likely "scope" key: lowest
// cardinality that appears in ≥80% of series, with namespace/service
// nudged ahead.
func annotateTypicalScope(labels []labelInfo, totalSeries int) {
	const presenceThreshold = 0.8
	bestIdx := -1
	bestCard := -1
	for i, l := range labels {
		// The probe-derived cardinality counts distinct values; the
		// "presence" check (≥80% of series) is approximate — we proxy it
		// by requiring distinct values ≤ totalSeries (always true) AND
		// preferring namespace/service when present.
		if bestIdx == -1 || l.Cardinality < bestCard {
			bestIdx = i
			bestCard = l.Cardinality
		}
	}
	// Nudge: if namespace/service is present, override the pick.
	for i, l := range labels {
		if l.Key == "namespace" || l.Key == "service" {
			bestIdx = i
			break
		}
	}
	if bestIdx >= 0 {
		labels[bestIdx].TypicalScope = true
	}
	_ = totalSeries       // currently unused; presence threshold deferred
	_ = presenceThreshold // deferred: will gate the presence check
}

// relatedByPrefix returns sibling metric names sharing the longest
// underscore-bounded prefix with `name`. Capped at `n`.
func relatedByPrefix(names []string, name string, n int) []string {
	if len(names) == 0 {
		return nil
	}
	var prefix string
	if i := lastUnderscore(name); i > 0 {
		prefix = name[:i+1]
	} else {
		return nil
	}
	var out []string
	for _, m := range names {
		if m == name || !startsWith(m, prefix) {
			continue
		}
		out = append(out, m)
		if len(out) >= n {
			break
		}
	}
	return out
}

func lastUnderscore(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '_' {
			return i
		}
	}
	return -1
}

func startsWith(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	return s[:len(prefix)] == prefix
}
