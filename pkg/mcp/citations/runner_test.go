package citations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubValidator implements Validator for runner tests. errs is what
// Validate returns; cleared after the first call so retry paths can flip
// to a clean validation outcome.
type stubValidator struct {
	errs    []string
	cleared bool
}

func (v *stubValidator) Validate(_ []Citation) []string {
	if v.cleared {
		return nil
	}
	return v.errs
}

func TestRun_HappyPath(t *testing.T) {
	calls := 0
	run := func(_ context.Context, _ string) (RawResult, error) {
		calls++
		return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"500.000100"}]
CITATIONS>>>`}, nil
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: &stubValidator{}, Prompt: "ask"})
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "happy path should only call once")
	assert.Equal(t, "claim [1].", strings.TrimSpace(out.Prose))
	require.Len(t, out.Citations, 1)
	assert.Empty(t, out.CitationsParseError)
}

func TestRun_RetryThenSuccess(t *testing.T) {
	calls := 0
	v := &stubValidator{errs: []string{"[1] thread does not exist"}}
	run := func(_ context.Context, prompt string) (RawResult, error) {
		calls++
		if calls == 1 {
			return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"999.000000"}]
CITATIONS>>>`}, nil
		}
		// Retry must include the previous response and the diagnosis.
		assert.Contains(t, prompt, "999.000000", "retry prompt should include broken response")
		assert.Contains(t, prompt, "thread does not exist", "retry prompt should include diagnosis")
		v.cleared = true // simulate sub-agent fixing its output
		return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"500.000100"}]
CITATIONS>>>`}, nil
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: v, Prompt: "ask"})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "should have retried exactly once")
	require.Len(t, out.Citations, 1)
	assert.Equal(t, "500.000100", out.Citations[0].ThreadTS)
}

func TestRun_RetryAlsoFails_SoftFail(t *testing.T) {
	calls := 0
	v := &stubValidator{errs: []string{"[1] thread does not exist"}}
	run := func(_ context.Context, _ string) (RawResult, error) {
		calls++
		return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"999.000000"}]
CITATIONS>>>`}, nil
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: v, Prompt: "ask"})
	require.NoError(t, err, "soft-fail should not propagate as Go error")
	assert.Equal(t, 2, calls, "retry budget capped at 1 — total calls is 2")
	assert.NotNil(t, out.Citations, "soft-fail Citations must be a non-nil empty slice so it marshals as [] not null — a nil slice trips the consumer's non-nullable array output schema")
	assert.Empty(t, out.Citations)
	assert.NotEmpty(t, out.CitationsParseError)
	assert.Contains(t, out.Prose, "claim [1]", "prose still surfaced on soft-fail")
}

func TestRun_NoBlock_TriggersRetry(t *testing.T) {
	calls := 0
	run := func(_ context.Context, _ string) (RawResult, error) {
		calls++
		return RawResult{Raw: "no citations block at all"}, nil
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: &stubValidator{}, Prompt: "ask"})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "missing block triggers retry")
	assert.NotEmpty(t, out.CitationsParseError)
	assert.Equal(t, "no citations block at all", strings.TrimSpace(out.Prose))
}

func TestRun_ShapeError_TriggersRetry(t *testing.T) {
	// Sub-agent emits a github_file citation missing path. Shape check
	// catches it; retry succeeds.
	calls := 0
	run := func(_ context.Context, _ string) (RawResult, error) {
		calls++
		if calls == 1 {
			return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"github_file","repo":"x/y","ref":"abc"}]
CITATIONS>>>`}, nil
		}
		return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"github_file","repo":"x/y","path":"f.go","ref":"abc"}]
CITATIONS>>>`}, nil
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: &stubValidator{}, Prompt: "ask"})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	require.Len(t, out.Citations, 1)
	assert.Equal(t, "f.go", out.Citations[0].Path)
}

func TestRun_TransportErrorOnFirstCall_Propagates(t *testing.T) {
	run := func(_ context.Context, _ string) (RawResult, error) {
		return RawResult{}, errors.New("subprocess died")
	}
	_, err := Run(context.Background(), RunInput{Run: run, Validator: &stubValidator{}, Prompt: "ask"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subprocess died")
}

func TestRun_TransportErrorOnRetry_SoftFail(t *testing.T) {
	calls := 0
	v := &stubValidator{errs: []string{"[1] bogus"}}
	run := func(_ context.Context, _ string) (RawResult, error) {
		calls++
		if calls == 1 {
			return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"x"}]
CITATIONS>>>`}, nil
		}
		return RawResult{}, errors.New("retry subprocess died")
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: v, Prompt: "ask"})
	require.NoError(t, err, "retry transport failure should soft-fail, not error")
	assert.Equal(t, 2, calls)
	assert.NotNil(t, out.Citations, "retry-transport soft-fail Citations must be non-nil so it marshals as [] not null")
	assert.Contains(t, out.CitationsParseError, "retry transport")
	assert.Contains(t, out.Prose, "claim [1]")
}

func TestRun_TimedOutForwarded(t *testing.T) {
	run := func(_ context.Context, _ string) (RawResult, error) {
		return RawResult{
			Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"1"}]
CITATIONS>>>`,
			TimedOut: true,
		}, nil
	}
	out, err := Run(context.Background(), RunInput{Run: run, Validator: &stubValidator{}, Prompt: "ask"})
	require.NoError(t, err)
	assert.True(t, out.TimedOut, "TimedOut should be forwarded from RawResult")
}
