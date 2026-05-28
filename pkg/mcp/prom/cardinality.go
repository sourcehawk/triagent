package prom

import (
	"context"
	"sort"
	"strings"
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

// probeAndCache issues the series probe for `name` and stores both
// cardEst and labelsCache under cat.mu in a single critical section.
// Returns the freshly-built labelProfile so callers that need it (like
// describeMetric) don't re-fetch from the cache.
//
// The cache write is a "last writer wins" overwrite — concurrent callers
// for the same metric race to probe; the later writer's labelProfile
// supersedes. Result is consistent because both probes derive from the
// same series-endpoint state.
func probeAndCache(ctx context.Context, c *promClient, cat *catalog, name string) (labelProfile, error) {
	rows, err := c.series(ctx, name, cardProbeLimit)
	if err != nil {
		return labelProfile{}, err
	}
	truncated := len(rows) >= cardProbeLimit
	prof := buildLabelProfile(rows, cat, name)
	prof.truncated = truncated
	cat.mu.Lock()
	defer cat.mu.Unlock()
	if truncated {
		cat.cardEst[name] = -1
	} else {
		cat.cardEst[name] = len(rows)
	}
	cat.labelsCache[name] = prof
	return prof, nil
}

// cardinalityOf returns the cached cardinality for `name`, probing if
// not yet known. -1 is the "high cardinality" sentinel (limit reached
// during probe).
func cardinalityOf(ctx context.Context, c *promClient, cat *catalog, name string) (int, error) {
	cat.mu.Lock()
	if v, ok := cat.cardEst[name]; ok {
		cat.mu.Unlock()
		return v, nil
	}
	cat.mu.Unlock()

	if _, err := probeAndCache(ctx, c, cat, name); err != nil {
		return 0, err
	}
	cat.mu.Lock()
	defer cat.mu.Unlock()
	return cat.cardEst[name], nil
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
	sort.Slice(labels, func(i, j int) bool {
		return labels[i].Key < labels[j].Key
	})
	annotateTypicalScope(labels)
	related := relatedByPrefix(cat.names, name, 10)
	return labelProfile{
		labels:           labels,
		relatedMetrics:   related,
		totalCardinality: len(rows),
	}
}

// annotateTypicalScope marks the most likely "scope" key on `labels` in
// place: the lowest-cardinality label wins, with `namespace`/`service`
// taking precedence when present. A presence-threshold gate (e.g. label
// must appear in ≥80% of series) is a possible future refinement; it
// requires per-row presence counts we don't track today.
func annotateTypicalScope(labels []labelInfo) {
	bestIdx := -1
	bestCard := -1
	for i, l := range labels {
		if bestIdx == -1 || l.Cardinality < bestCard {
			bestIdx = i
			bestCard = l.Cardinality
		}
	}
	for i, l := range labels {
		if l.Key == "namespace" || l.Key == "service" {
			bestIdx = i
			break
		}
	}
	if bestIdx >= 0 {
		labels[bestIdx].TypicalScope = true
	}
}

// relatedByPrefix returns sibling metric names sharing the longest
// underscore-bounded prefix with `name` (excluding `name` itself).
// Returns nil when `name` has no usable prefix — names with no
// underscore, or a leading underscore at index 0, have no group.
// Capped at `n` results.
func relatedByPrefix(names []string, name string, n int) []string {
	if len(names) == 0 {
		return nil
	}
	i := strings.LastIndexByte(name, '_')
	if i <= 0 {
		return nil
	}
	prefix := name[:i+1]
	var out []string
	for _, m := range names {
		if m == name || !strings.HasPrefix(m, prefix) {
			continue
		}
		out = append(out, m)
		if len(out) >= n {
			break
		}
	}
	return out
}
