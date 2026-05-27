package slack

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunSubAgentWithCitations_RetryResumesSession verifies that the
// slack citation-correction retry resumes the prior turn's Claude
// session rather than spawning fresh. The first sub-agent call returns
// a SessionID; the retry sub-agent invocation must carry that id as
// resumeSessionID — otherwise every citation retry pays the cost of
// re-orienting in the channel cache.
func TestRunSubAgentWithCitations_RetryResumesSession(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	store := resolveStoreSeed(t, srv, "C1", []Message{{TS: "500.000100"}})

	var observedResumeIDs []string
	srv.runSubAgent = func(_ context.Context, _, _, _, resumeID string) (subAgentResult, error) {
		observedResumeIDs = append(observedResumeIDs, resumeID)
		// Both calls return broken citations → citations runner triggers
		// the retry on the second invocation. Both report session "sess-S".
		return subAgentResult{
			Summary:   "claim [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"999.000000\"}]\nCITATIONS>>>",
			SessionID: "sess-S",
		}, nil
	}

	_, err := srv.runSubAgentWithCitations(context.Background(), "ask question", "", "C1", store)
	require.NoError(t, err, "soft-fail (both attempts bad) should not propagate as Go error")
	require.GreaterOrEqual(t, len(observedResumeIDs), 2,
		"citation retry must invoke the sub-agent at least twice")
	require.Empty(t, observedResumeIDs[0], "first call must not carry a resume id")
	require.Equal(t, "sess-S", observedResumeIDs[1],
		"citation retry must resume the session captured from the first call")
}
