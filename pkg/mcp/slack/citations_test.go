package slack

import (
	"context"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlackValidator_AllValid(t *testing.T) {
	snap := Snapshot{
		Parents: []Message{{TS: "500.000100"}, {TS: "480.000100"}},
		Threads: map[string][]Message{
			"480.000100": {{TS: "480.000100"}, {TS: "485.000700"}},
		},
	}
	v := &slackValidator{channelID: "C1", snap: snap}
	cits := []citations.Citation{
		{Kind: citations.KindSlackThread, ChannelID: "C1", ThreadTS: "500.000100"},
		{Kind: citations.KindSlackThread, ChannelID: "C1", ThreadTS: "480.000100", MessageTS: "485.000700"},
	}
	assert.Empty(t, v.Validate(cits))
}

func TestSlackValidator_WrongChannel(t *testing.T) {
	v := &slackValidator{channelID: "C1", snap: Snapshot{Parents: []Message{{TS: "500.000100"}}}}
	cits := []citations.Citation{{Kind: citations.KindSlackThread, ChannelID: "C2", ThreadTS: "500.000100"}}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "channel_id")
}

func TestSlackValidator_UnknownThreadTS(t *testing.T) {
	v := &slackValidator{channelID: "C1", snap: Snapshot{Parents: []Message{{TS: "500.000100"}}}}
	cits := []citations.Citation{{Kind: citations.KindSlackThread, ChannelID: "C1", ThreadTS: "999.000000"}}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "thread_ts")
}

func TestSlackValidator_UnknownMessageTS(t *testing.T) {
	snap := Snapshot{
		Parents: []Message{{TS: "480.000100"}},
		Threads: map[string][]Message{"480.000100": {{TS: "480.000100"}, {TS: "485.000700"}}},
	}
	v := &slackValidator{channelID: "C1", snap: snap}
	cits := []citations.Citation{{Kind: citations.KindSlackThread, ChannelID: "C1", ThreadTS: "480.000100", MessageTS: "999.000000"}}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "message_ts")
}

func TestSlackValidator_RejectsNonSlackKind(t *testing.T) {
	v := &slackValidator{channelID: "C1", snap: Snapshot{}}
	cits := []citations.Citation{{Kind: citations.KindGithubFile, Repo: "x/y", Path: "f", Ref: "r"}}
	errs := v.Validate(cits)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "slack_thread")
}

// resolveStoreSeed primes a Server's per-channel store with a parents
// list so the validator has something to match against. Returns the
// resolved store for any further inspection.
func resolveStoreSeed(t *testing.T, srv *Server, channelID string, parents []Message) *Store {
	t.Helper()
	st, err := srv.resolveStore(channelID)
	require.NoError(t, err)
	st.parents = parents
	return st
}

func TestRunSubAgentWithCitations_HappyPath(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		return subAgentResult{
			Summary: "claim [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"500.000100\"}]\nCITATIONS>>>",
		}, nil
	}
	store := resolveStoreSeed(t, srv, "C1", []Message{{TS: "500.000100"}})

	res, err := srv.runSubAgentWithCitations(context.Background(), "ask question", "", "C1", store)
	require.NoError(t, err)
	assert.Equal(t, "claim [1].", strings.TrimSpace(res.Summary))
	require.Len(t, res.Citations, 1)
	assert.Empty(t, res.CitationsParseError)
}

func TestRunSubAgentWithCitations_RetryThenSuccess(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	store := resolveStoreSeed(t, srv, "C1", []Message{{TS: "500.000100"}})

	calls := 0
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		calls++
		if calls == 1 {
			// First call returns a citation for an unknown thread.
			return subAgentResult{Summary: "claim [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"999.000000\"}]\nCITATIONS>>>"}, nil
		}
		// Retry must include the previous response and the validation errors.
		assert.Contains(t, prompt, "999.000000", "retry prompt missing the broken thread_ts so the sub-agent can fix it")
		assert.Contains(t, prompt, "thread_ts", "retry prompt missing the validator's diagnosis")
		return subAgentResult{Summary: "claim [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"500.000100\"}]\nCITATIONS>>>"}, nil
	}

	res, err := srv.runSubAgentWithCitations(context.Background(), "ask question", "", "C1", store)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "should have retried exactly once")
	require.Len(t, res.Citations, 1)
	assert.Equal(t, "500.000100", res.Citations[0].ThreadTS)
	assert.Empty(t, res.CitationsParseError)
}

func TestRunSubAgentWithCitations_RetryAlsoFails_SoftFail(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	store := resolveStoreSeed(t, srv, "C1", []Message{{TS: "500.000100"}})

	calls := 0
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		calls++
		// Both calls return broken citations.
		return subAgentResult{Summary: "claim [1].\n\n<<<CITATIONS\n[{\"kind\":\"slack_thread\",\"channel_id\":\"C1\",\"thread_ts\":\"999.000000\"}]\nCITATIONS>>>"}, nil
	}

	res, err := srv.runSubAgentWithCitations(context.Background(), "ask question", "", "C1", store)
	require.NoError(t, err, "soft-fail must not propagate the validation error as a Go error")
	assert.Equal(t, 2, calls, "retry budget capped at 1 — total calls is 2")
	assert.Empty(t, res.Citations, "citations dropped when validation failed twice")
	assert.NotEmpty(t, res.CitationsParseError, "soft-fail surfaces the validator messages")
	assert.Contains(t, res.Summary, "claim [1]", "prose still surfaced even on soft-fail")
}

func TestRunSubAgentWithCitations_NoBlock_SoftFail(t *testing.T) {
	srv, _ := New(Options{Token: "xoxp-x", APIBase: "http://unused", CacheDir: t.TempDir()})
	store := resolveStoreSeed(t, srv, "C1", nil)
	calls := 0
	srv.runSubAgent = func(ctx context.Context, prompt, parentToolID, workingDir, _ string) (subAgentResult, error) {
		calls++
		return subAgentResult{Summary: "no citations block at all"}, nil
	}
	res, err := srv.runSubAgentWithCitations(context.Background(), "ask question", "", "C1", store)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "missing block triggers retry")
	assert.NotEmpty(t, res.CitationsParseError)
	assert.Equal(t, "no citations block at all", strings.TrimSpace(res.Summary))
}
