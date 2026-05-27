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
		b.WriteString("\ncatalog empty — switch context may not have a Prom endpoint, or the port-forward is still establishing.\n")
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

Query:
  - prom_recent_value(metric, labels)        current value for exact labels
  - prom_query(promql)                       instant; scalar or ≤50-series vector
  - prom_query_range(promql, range)          time-shape; summary stats per series

Conventions:
  - Always pass at least one scope matcher (namespace, service, job, …).
  - Prefer threshold checks and topk() to broad selections.
`
}
