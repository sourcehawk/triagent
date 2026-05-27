package prom

import (
	"context"
	"fmt"
	"sort"
)

// DescribeResult is the JSON shape returned to the agent.
type DescribeResult struct {
	Name             string      `json:"name"`
	Type             string      `json:"type"`
	Help             string      `json:"help"`
	Unit             string      `json:"unit,omitempty"`
	Labels           []labelInfo `json:"labels"`
	Related          []string    `json:"related,omitempty"`
	CardinalityTotal int         `json:"cardinality_total"`
}

// describeMetric assembles a DescribeResult, lazily probing the series
// endpoint if the catalog has not yet seen this metric.
func describeMetric(ctx context.Context, c *promClient, cat *catalog, name string) (DescribeResult, error) {
	if !catalogHas(cat, name) {
		return DescribeResult{}, fmt.Errorf("metric %q not in catalog", name)
	}
	cat.mu.Lock()
	prof, ok := cat.labelsCache[name]
	cat.mu.Unlock()
	if !ok {
		var err error
		prof, err = probeAndCache(ctx, c, cat, name)
		if err != nil {
			return DescribeResult{}, err
		}
	}
	md := cat.metadata[name]
	return DescribeResult{
		Name:             name,
		Type:             md.Type,
		Help:             md.Help,
		Unit:             md.Unit,
		Labels:           append([]labelInfo(nil), prof.labels...), // copy so callers can't mutate the cached entry
		Related:          prof.relatedMetrics,
		CardinalityTotal: prof.totalCardinality,
	}, nil
}

func catalogHas(cat *catalog, name string) bool {
	i := sort.SearchStrings(cat.names, name)
	return i < len(cat.names) && cat.names[i] == name
}
