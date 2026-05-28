package prom

import (
	"fmt"
	"sort"
	"strings"
)

const infoMinPrefixCount = 20

// renderInfo builds the prom://info resource body from a catalog
// snapshot. Output is human-readable text; the agent reads it once
// per attach to learn the metric-namespace shape and the tool guidance.
func renderInfo(cat *catalog) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d metrics indexed at %s.\n", len(cat.names), cat.endpoint)
	if len(cat.names) == 0 {
		b.WriteString("\ncatalog empty — the endpoint may have no metrics indexed, or it is not yet reachable.\n")
		b.WriteString(toolGuidanceBlock())
		return b.String()
	}
	// Top prefixes ≥ infoMinPrefixCount. For tiny catalogs (≤ a few
	// hundred metrics) the threshold misses everything, so fall back to
	// the top-12 prefixes regardless of count.
	type pref struct {
		name  string
		count int
	}
	var prefs []pref
	for p, c := range cat.prefixIdx {
		prefs = append(prefs, pref{p, c})
	}
	sort.Slice(prefs, func(i, j int) bool {
		if prefs[i].count != prefs[j].count {
			return prefs[i].count > prefs[j].count
		}
		return prefs[i].name < prefs[j].name
	})
	threshold := infoMinPrefixCount
	if len(cat.names) < 500 {
		threshold = 1
	}
	b.WriteString("\nTop prefixes")
	if threshold > 1 {
		fmt.Fprintf(&b, " (≥ %d metrics)", threshold)
	}
	b.WriteString(":\n")
	shown := 0
	for _, p := range prefs {
		if p.count < threshold {
			break
		}
		fmt.Fprintf(&b, "  %s*%s%d\n", p.name, strings.Repeat(" ", colPad(p.name)), p.count)
		shown++
		if shown == 12 {
			break
		}
	}
	b.WriteString(toolGuidanceBlock())
	return b.String()
}

// colPad returns the space count to insert between the `*` character that
// follows the prefix in renderInfo's row format and the count column at
// position `target`. The -1 accounts for the `*` that renderInfo emits
// immediately after the prefix in its `"  %s*%s%d\n"` format.
func colPad(prefix string) int {
	const target = 18
	pad := target - len(prefix) - 1
	if pad < 2 {
		return 2
	}
	return pad
}

func toolGuidanceBlock() string {
	return `
Discovery:
  - prom_list_metrics(query)         search by name or HELP text
  - prom_describe_metric(name)       labels, sample values, related metrics
                                     (sample is historical — when truncated:true,
                                      verify with count by (label) (metric))

Query:
  - prom_recent_value(metric, labels)        current value for exact labels
  - prom_query(promql)                       instant; scalar or ≤50-series vector
  - prom_query_range(promql, range)          time-shape; summary stats per series

Conventions:
  - Scope a high-cardinality metric with EITHER a label matcher
    ({namespace="…"}) OR a bounding wrapper (topk(N, …) / bottomk(N, …) /
    limitk(N, …)). Both satisfy the scope guard.
  - Default series cap is 50 (prom_query) / 10 (prom_query_range). Pass
    max_series (up to 500) for a deliberate fleet aggregation; the cap
    exists to keep the conversation cheap, not to forbid wide queries.
  - A query that matches no series returns {"result_type":"vector",
    "samples":[]}. That is a successful empty result, not an error.
`
}
