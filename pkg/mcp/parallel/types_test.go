package parallel

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallIn_RoundTripsThroughJSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"summary": "Spawning two sub-agents — find the alert rule, identify the workload.",
		"calls": [
			{"server":"triagent-git-alerts","tool":"analyze_change","input":{"ref":"main","question":"q1"},"purpose":"find alert rule"},
			{"server":"triagent-git-example-operator","tool":"analyze_change","input":{"ref":"main","question":"q2"}}
		],
		"max_concurrency": 6
	}`)
	var in CallIn
	require.NoError(t, json.Unmarshal(raw, &in))
	require.Equal(t, "Spawning two sub-agents — find the alert rule, identify the workload.", in.Summary)
	require.Len(t, in.Calls, 2)
	require.Equal(t, "triagent-git-alerts", in.Calls[0].Server)
	require.Equal(t, "analyze_change", in.Calls[0].Tool)
	require.Equal(t, "find alert rule", in.Calls[0].Purpose)
	require.Equal(t, 6, in.MaxConcurrency)
}

func TestCallOut_OmitsAbsentFields(t *testing.T) {
	t.Parallel()
	out := CallOut{
		Summary:    "s",
		DurationMs: 100,
		Results: []SubResult{
			{OK: true, Result: map[string]any{"x": 1}},
			{OK: false, Error: "boom"},
			{OK: false, Rejected: true, Error: "not allowlisted"},
		},
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	js := string(b)
	require.Contains(t, js, `"ok":true`)
	require.Contains(t, js, `"ok":false`)
	require.Contains(t, js, `"rejected":true`)
	require.NotContains(t, js, `"result":null`)
	require.NotContains(t, js, `"error":""`)
	require.NotContains(t, js, `"timed_out":false`)
	require.NotContains(t, js, `"rejected":false`)
}
