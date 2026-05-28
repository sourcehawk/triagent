package prom

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{Server: "triagent-prom", Name: "prom_list_metrics", Description: "Search the indexed metric namespace by token-AND match against name and HELP text. Over-cap match sets return as a sub-prefix facet breakdown.", Inputs: toolspec.FromStruct(listMetricsIn{})},
		{Server: "triagent-prom", Name: "prom_describe_metric", Description: "Return label keys, sample values, related sibling metrics, and total cardinality for a known metric. Lazy series probe on first call. When `truncated: true`, per-label cardinality and sample_values come from a 200-series sample — verify uniqueness with prom_query before trusting them.", Inputs: toolspec.FromStruct(describeMetricIn{})},
		{Server: "triagent-prom", Name: "prom_query", Description: "Instant PromQL. Scalar-first; hard 50-series cap; high-cardinality metrics require a non-__name__ label matcher.", Inputs: toolspec.FromStruct(promQueryIn{})},
		{Server: "triagent-prom", Name: "prom_recent_value", Description: "Current value of `metric` for the exact label set. Single value or structured error (no data / multiple matched).", Inputs: toolspec.FromStruct(recentValueIn{})},
		{Server: "triagent-prom", Name: "prom_query_range", Description: "Range query for shape-over-time. Per-series summary stats + sparkline by default; opt-in raw points. Step auto-computed.", Inputs: toolspec.FromStruct(promQueryRangeIn{})},
	}
}
