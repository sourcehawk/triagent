package prom

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const queryHardSeriesCap = 50

// QueryResult is the JSON shape returned to the agent.
type QueryResult struct {
	ResultType  string   `json:"result_type"`
	Samples     []Sample `json:"samples"`
	ScalarValue *float64 `json:"value,omitempty"`
	StringValue string   `json:"string_value,omitempty"`
	Timestamp   string   `json:"timestamp,omitempty"`
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
		if !isFiniteSample(v) {
			return QueryResult{}, fmt.Errorf("scalar result was non-finite (NaN or ±Inf) — the expression produced an undefined value; refine the query")
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
				"query returned %d series; cap is %d — wrap in topk(N, ...), aggregate (sum/avg by (...)) or add a scope matcher",
				len(res.Result), queryHardSeriesCap,
			)
		}
		samples := make([]Sample, 0, len(res.Result))
		for _, r := range res.Result {
			v, ts, err := parseSamplePair(r.Value)
			if err != nil {
				return QueryResult{}, err
			}
			if !isFiniteSample(v) {
				// Drop the row — encoding/json would reject the whole
				// response otherwise. Agents see a shorter samples list
				// and can re-run with a sharper expression if needed.
				continue
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
// parse error. NaN/+Inf/-Inf are valid Prometheus sample values but
// encoding/json cannot marshal them, so callers must filter via
// isFiniteSample before they reach a JSON-bound struct.
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

// isFiniteSample reports whether v is a regular floating-point number
// suitable for inclusion in a JSON-bound struct. NaN and ±Inf are
// excluded because encoding/json returns an UnsupportedValueError for
// them, which would fail the whole MCP tool response.
func isFiniteSample(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// ValueResult is the JSON shape returned by prom_recent_value.
type ValueResult struct {
	Value     float64 `json:"value"`
	Timestamp string  `json:"timestamp"`
}

func runRecentValue(ctx context.Context, snap *snapshot, metric string, labels map[string]string) (ValueResult, error) {
	if metric == "" {
		return ValueResult{}, fmt.Errorf("metric is required — pass the exact metric name from prom_list_metrics")
	}
	if len(labels) == 0 {
		return ValueResult{}, fmt.Errorf("labels required (non-empty) — prom_recent_value needs at least one label matcher to scope the lookup")
	}
	for k := range labels {
		if !isValidLabelName(k) {
			return ValueResult{}, fmt.Errorf("invalid label name %q — must match Prometheus label syntax [a-zA-Z_][a-zA-Z0-9_]*", k)
		}
	}
	expr := metric + "{" + buildMatcherString(labels) + "}"
	if err := checkScope(ctx, snap.client, snap.catalog, expr); err != nil {
		return ValueResult{}, err
	}
	res, err := snap.client.query(ctx, expr, "")
	if err != nil {
		return ValueResult{}, err
	}
	if res.ResultType != "vector" {
		return ValueResult{}, fmt.Errorf("unexpected result type %q for metric+labels query", res.ResultType)
	}
	switch len(res.Result) {
	case 0:
		return ValueResult{}, noMatchError(snap.catalog, metric, labels)
	case 1:
		v, ts, err := parseSamplePair(res.Result[0].Value)
		if err != nil {
			return ValueResult{}, err
		}
		if !isFiniteSample(v) {
			return ValueResult{}, fmt.Errorf("matched series has a non-finite value (NaN or ±Inf) at %s — refine the labels or use a different metric", ts)
		}
		return ValueResult{Value: v, Timestamp: ts}, nil
	default:
		return ValueResult{}, multiMatchError(metric, res.Result, labels)
	}
}

// noMatchError consults the cached labelsCache sample to identify the
// most likely cause of an empty match. When no sample is cached, falls
// back to the bare "no data" form because the alternative — claims
// derived from data we don't have — is worse than silence.
func noMatchError(cat *catalog, metric string, userLabels map[string]string) error {
	cat.mu.Lock()
	prof, ok := cat.labelsCache[metric]
	cat.mu.Unlock()
	if !ok || len(prof.labels) == 0 {
		return fmt.Errorf("no data — no series matches %q with the given labels", metric)
	}
	sample := map[string]map[string]struct{}{}
	known := map[string][]string{}
	for _, l := range prof.labels {
		set := make(map[string]struct{}, len(l.SampleValues))
		for _, v := range l.SampleValues {
			set[v] = struct{}{}
		}
		sample[l.Key] = set
		known[l.Key] = l.SampleValues
	}
	type absent struct {
		key   string
		value string
		known []string
	}
	var absents []absent
	for k, v := range userLabels {
		set, seen := sample[k]
		if !seen {
			// Label key not observed in the sample at all — note it but
			// don't claim absence of the value (the key may exist on
			// series the sample didn't reach).
			continue
		}
		if _, vSeen := set[v]; !vSeen {
			absents = append(absents, absent{key: k, value: v, known: known[k]})
		}
	}
	sort.Slice(absents, func(i, j int) bool { return absents[i].key < absents[j].key })
	if len(absents) == 0 {
		// Every user label value appeared individually in the sample,
		// but the combination still matched nothing. That's the only
		// remaining explanation we can offer without more probes.
		return fmt.Errorf("no data — each label value exists individually in the sample, but no series carries this combination of %s; verify with prom_query: count(%s{...})", joinLabelKeys(userLabels), metric)
	}
	var parts []string
	for _, a := range absents {
		preview := a.known
		if len(preview) > 5 {
			preview = preview[:5]
		}
		if prof.truncated {
			parts = append(parts, fmt.Sprintf("%s=%q not in the 200-series sample (sample values: %v) — may still exist; confirm with prom_query: count(%s{%s=%q})",
				a.key, a.value, preview, metric, a.key, a.value))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%q does not exist for this metric (known values: %v)",
				a.key, a.value, preview))
		}
	}
	return fmt.Errorf("no data — %s", strings.Join(parts, "; "))
}

// multiMatchError surfaces which labels distinguish the matched series
// so the agent has a direct next step instead of re-fetching describe.
func multiMatchError(metric string, results []SeriesResult, userLabels map[string]string) error {
	varying := varyingLabels(results, userLabels)
	if len(varying) == 0 {
		return fmt.Errorf("multiple series matched (%d) — narrow the label set", len(results))
	}
	return fmt.Errorf("multiple series matched (%d) — narrow the label set; values vary across matches on: %s",
		len(results), strings.Join(varying, ", "))
}

// varyingLabels returns the sorted list of label keys whose values
// differ across at least two of the result series. Keys the user
// already pinned are excluded — re-pinning them won't help.
func varyingLabels(results []SeriesResult, userLabels map[string]string) []string {
	if len(results) < 2 {
		return nil
	}
	seen := map[string]map[string]struct{}{}
	for _, r := range results {
		for k, v := range r.Metric {
			if k == "__name__" {
				continue
			}
			if _, pinned := userLabels[k]; pinned {
				continue
			}
			set, ok := seen[k]
			if !ok {
				set = map[string]struct{}{}
				seen[k] = set
			}
			set[v] = struct{}{}
		}
	}
	var out []string
	for k, set := range seen {
		if len(set) > 1 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// joinLabelKeys renders user labels in a deterministic key order for
// error prose.
func joinLabelKeys(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// buildMatcherString renders {k1="v1",k2="v2"} contents with deterministic
// key ordering so equivalent inputs produce identical query strings.
func buildMatcherString(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+`="`+escapeMatcherValue(labels[k])+`"`)
	}
	return strings.Join(parts, ",")
}

// labelNameRE matches Prometheus's label-name grammar
// (`[a-zA-Z_][a-zA-Z0-9_]*`). Used to gate caller-supplied keys before
// they are spliced into PromQL — values are quote-escaped by
// escapeMatcherValue, but keys live between commas and `=` with no
// delimiter, so an unvalidated key can break out of the matcher block.
var labelNameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func isValidLabelName(name string) bool {
	return labelNameRE.MatchString(name)
}

func escapeMatcherValue(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '"' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(out)
}

const (
	rangeHardSeriesCap = 25
	rangeMaxDuration   = 24 * time.Hour
	// rangeHardPointsCap is the hard ceiling on max_points regardless of
	// caller input. Mirrors rangeHardSeriesCap; both bound the worst-case
	// JSON payload of a range query response.
	rangeHardPointsCap = 200
)

// RangeResult is the JSON shape returned by prom_query_range.
type RangeResult struct {
	Step   string        `json:"step"`
	Series []RangeSeries `json:"series"`
}

// RangeSeries is one per-series row in a RangeResult.
type RangeSeries struct {
	Labels    map[string]string `json:"labels"`
	Stats     *SeriesStats      `json:"stats,omitempty"`
	Sparkline string            `json:"sparkline,omitempty"`
	Points    []RangePoint      `json:"points,omitempty"`
}

// RangePoint is a single [ts, value] pair in raw mode.
type RangePoint struct {
	Timestamp string  `json:"ts"`
	Value     float64 `json:"v"`
}

// SeriesStats holds summary statistics for a range series.
type SeriesStats struct {
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	First float64 `json:"first"`
	Last  float64 `json:"last"`
}

func runRangeQuery(ctx context.Context, snap *snapshot, expr, rangeStr, endStr string, maxSeries, maxPoints int, raw bool) (RangeResult, error) {
	if err := checkScope(ctx, snap.client, snap.catalog, expr); err != nil {
		return RangeResult{}, err
	}
	dur, err := time.ParseDuration(rangeStr)
	if err != nil {
		return RangeResult{}, fmt.Errorf("invalid range %q: %w", rangeStr, err)
	}
	if dur > rangeMaxDuration {
		return RangeResult{}, fmt.Errorf("range %s exceeds cap of %s", dur, rangeMaxDuration)
	}
	if dur <= 0 {
		return RangeResult{}, fmt.Errorf("range must be positive")
	}
	if maxPoints <= 0 {
		maxPoints = 100
	}
	if maxPoints > rangeHardPointsCap {
		maxPoints = rangeHardPointsCap
	}
	if maxSeries <= 0 {
		maxSeries = 10
	}
	if maxSeries > rangeHardSeriesCap {
		maxSeries = rangeHardSeriesCap
	}

	end := time.Now().UTC()
	if endStr != "" {
		t, perr := time.Parse(time.RFC3339Nano, endStr)
		if perr != nil {
			return RangeResult{}, fmt.Errorf("invalid end %q: %w", endStr, perr)
		}
		end = t.UTC()
	}
	start := end.Add(-dur)

	// Prom's /api/v1/query_range returns samples inclusive of both start
	// and end, so the actual count is floor(dur/step) + 1. Compute step
	// with ceiling division on (maxPoints - 1) so the count stays within
	// budget without later truncation dropping the most-recent sample.
	durSeconds := int(dur.Seconds())
	var stepSec int
	if maxPoints > 1 {
		stepSec = (durSeconds + maxPoints - 2) / (maxPoints - 1)
	} else {
		// maxPoints == 1: set step larger than the range so Prom's
		// inclusive-endpoint rule (floor(dur/step) + 1) yields exactly
		// one sample. durSeconds alone produces 2 samples because
		// floor(dur/dur) + 1 = 2; durSeconds + 1 gives floor(dur/(dur+1)) + 1 = 1.
		stepSec = durSeconds + 1
	}
	if stepSec < 1 {
		stepSec = 1
	}
	step := fmt.Sprintf("%ds", stepSec)

	res, err := snap.client.queryRange(ctx, expr,
		strconv.FormatInt(start.Unix(), 10),
		strconv.FormatInt(end.Unix(), 10),
		step,
	)
	if err != nil {
		return RangeResult{}, err
	}
	if len(res.Result) > maxSeries {
		return RangeResult{}, fmt.Errorf(
			"query returned %d series; cap is %d — wrap in topk(N, ...), aggregate, or add a scope matcher",
			len(res.Result), maxSeries,
		)
	}

	out := RangeResult{Step: step, Series: []RangeSeries{}}
	for _, sr := range res.Result {
		series := RangeSeries{Labels: sr.Metric}
		if raw {
			points := make([]RangePoint, 0, len(sr.Values))
			for _, pair := range sr.Values {
				v, ts, perr := parseSamplePair(pair)
				if perr != nil {
					return RangeResult{}, perr
				}
				if !isFiniteSample(v) {
					continue
				}
				points = append(points, RangePoint{Timestamp: ts, Value: v})
			}
			if len(points) > maxPoints {
				points = points[:maxPoints]
			}
			series.Points = points
		} else {
			values := make([]float64, 0, len(sr.Values))
			for _, pair := range sr.Values {
				v, _, perr := parseSamplePair(pair)
				if perr != nil {
					return RangeResult{}, perr
				}
				if !isFiniteSample(v) {
					continue
				}
				values = append(values, v)
			}
			stats := computeStats(values)
			series.Stats = &stats
			series.Sparkline = sparkline(values)
		}
		out.Series = append(out.Series, series)
	}
	return out, nil
}

// computeStats returns summary statistics over the provided values.
// Returns a zero-value SeriesStats for an empty slice.
func computeStats(vals []float64) SeriesStats {
	if len(vals) == 0 {
		return SeriesStats{}
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	stats := SeriesStats{
		Min:   sorted[0],
		Max:   sorted[len(sorted)-1],
		First: vals[0],
		Last:  vals[len(vals)-1],
		P50:   percentile(sorted, 0.50),
		P95:   percentile(sorted, 0.95),
		P99:   percentile(sorted, 0.99),
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	stats.Mean = sum / float64(len(vals))
	return stats
}

// percentile returns the value at the given percentile p (0–1) from a
// pre-sorted slice using floor interpolation.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Floor(p * float64(len(sorted)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
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
