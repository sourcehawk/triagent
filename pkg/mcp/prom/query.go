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
	StringValue string   `json:"string_value,omitempty"`
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
		// Prom shape: result = [timestamp, "stringValue"]; we surface
		// the value via the StringValue field for the agent's benefit.
		ts, sv, err := parseStringPair(res.Scalar)
		if err != nil {
			return QueryResult{}, err
		}
		return QueryResult{ResultType: "string", StringValue: sv, Timestamp: ts}, nil
	case "vector":
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
	case "matrix":
		// An instant query that returns a matrix is anomalous — Prom
		// normally returns matrices only from /api/v1/query_range. Reject
		// rather than silently mis-shaping the response.
		return QueryResult{}, fmt.Errorf("instant query returned a matrix; use prom_query_range for shape-over-time data")
	default:
		return QueryResult{}, fmt.Errorf("unexpected resultType %q from instant query", res.ResultType)
	}
}

// parseSamplePair handles Prom's [timestampSeconds, "valueString"] pair
// shape. Shared by runInstantQuery, runRecentValue (Task 12) and
// runRangeQuery (Task 14). Returns the parsed value, the RFC3339-Nano
// formatted UTC timestamp (sub-second precision preserved), and any
// parse error.
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
	sec := int64(tsFloat)
	nsec := int64((tsFloat - float64(sec)) * 1e9)
	ts := time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
	return v, ts, nil
}

// parseStringPair handles Prom's [timestampSeconds, "value"] shape for
// resultType "string" — unlike scalar, the second element is the value
// itself rather than a stringified number.
func parseStringPair(pair []any) (string, string, error) {
	if len(pair) != 2 {
		return "", "", fmt.Errorf("invalid string pair: len=%d", len(pair))
	}
	tsFloat, ok := pair[0].(float64)
	if !ok {
		return "", "", fmt.Errorf("invalid string-pair timestamp: %T", pair[0])
	}
	sv, ok := pair[1].(string)
	if !ok {
		return "", "", fmt.Errorf("invalid string-pair value: %T", pair[1])
	}
	sec := int64(tsFloat)
	nsec := int64((tsFloat - float64(sec)) * 1e9)
	ts := time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
	return ts, sv, nil
}
