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
		rows, err := c.series(ctx, name, cardProbeLimit)
		if err != nil {
			return DescribeResult{}, err
		}
		cat.mu.Lock()
		if len(rows) >= cardProbeLimit {
			cat.cardEst[name] = -1
		} else {
			cat.cardEst[name] = len(rows)
		}
		prof = buildLabelProfile(rows, cat, name)
		cat.labelsCache[name] = prof
		cat.mu.Unlock()
	}
	sort.Slice(prof.labels, func(i, j int) bool {
		return prof.labels[i].Key < prof.labels[j].Key
	})
	md := cat.metadata[name]
	return DescribeResult{
		Name:             name,
		Type:             md.Type,
		Help:             md.Help,
		Unit:             md.Unit,
		Labels:           prof.labels,
		Related:          prof.relatedMetrics,
		CardinalityTotal: prof.totalCardinality,
	}, nil
}

func catalogHas(cat *catalog, name string) bool {
	for _, n := range cat.names {
		if n == name {
			return true
		}
	}
	return false
}
