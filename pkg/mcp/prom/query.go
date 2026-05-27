package prom

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const queryHardSeriesCap = 50

// QueryResult is the JSON shape returned to the agent.
type QueryResult struct {
	ResultType  string   `json:"result_type"`
	Samples     []Sample `json:"samples,omitempty"`
	ScalarValue *float64 `json:"value,omitempty"`
	Timestamp   string   `json:"timestamp,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
}

// Sample is a single labeled data point from an instant query result.
type Sample struct {
	Labels    map[string]string `json:"labels"`
	Value     float64           `json:"value"`
	Timestamp string            `json:"timestamp"`
}

func runInstantQuery(ctx context.Context, snap *snapshot, expr, atTime string) (QueryResult, error) {
	if err := checkScope(ctx, snap.client, snap.catalog, expr); err != nil {
		return QueryResult{}, err
	}
	res, err := snap.client.query(ctx, expr, atTime)
	if err != nil {
		return QueryResult{}, err
	}
	switch res.ResultType {
	case "scalar":
		v, ts, err := parseSamplePair(res.Scalar)
		if err != nil {
			return QueryResult{}, err
		}
		return QueryResult{ResultType: "scalar", ScalarValue: &v, Timestamp: ts}, nil
	case "string":
		return QueryResult{ResultType: "string"}, nil
	default:
		if len(res.Result) > queryHardSeriesCap {
			return QueryResult{}, fmt.Errorf(
				"query returned %d series; cap is %d. Wrap in topk(N, ...), aggregate (sum/avg by (...)) or add a scope matcher.",
				len(res.Result), queryHardSeriesCap,
			)
		}
		samples := make([]Sample, 0, len(res.Result))
		for _, r := range res.Result {
			v, ts, err := parseSamplePair(r.Value)
			if err != nil {
				return QueryResult{}, err
			}
			samples = append(samples, Sample{Labels: r.Metric, Value: v, Timestamp: ts})
		}
		return QueryResult{ResultType: res.ResultType, Samples: samples}, nil
	}
}

// parseSamplePair handles Prom's [timestamp, "value"] pair shape.
// Exported for reuse by Tasks 12 and 14 (prom_recent_value, prom_query_range).
func parseSamplePair(pair []any) (float64, string, error) {
	if len(pair) != 2 {
		return 0, "", fmt.Errorf("invalid sample pair: len=%d", len(pair))
	}
	tsFloat, ok := pair[0].(float64)
	if !ok {
		return 0, "", fmt.Errorf("invalid sample timestamp: %T", pair[0])
	}
	valStr, ok := pair[1].(string)
	if !ok {
		return 0, "", fmt.Errorf("invalid sample value: %T", pair[1])
	}
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, "", err
	}
	ts := time.Unix(int64(tsFloat), 0).UTC().Format(time.RFC3339)
	return v, ts, nil
}
