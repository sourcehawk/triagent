package citations

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetryReminder_AppendedToRetryPromptWhenSet — when RunInput
// carries a RetryReminder, the corrective-retry prompt sent on the
// second sub-agent invocation must include it. Lets callers like
// draft_pr reinforce side-effect contracts ("you must have committed",
// "keep the <<<PR_TITLE>>>/<<<PR_BODY>>> markers") that the generic
// citation-correction prompt doesn't carry.
func TestRetryReminder_AppendedToRetryPromptWhenSet(t *testing.T) {
	v := &stubValidator{errs: []string{"[1] bogus"}}
	calls := 0
	var retryPrompt string
	run := func(_ context.Context, prompt string) (RawResult, error) {
		calls++
		if calls == 2 {
			retryPrompt = prompt
			v.cleared = true
		}
		return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"1"}]
CITATIONS>>>`}, nil
	}
	reminder := "Reminder: you must have committed before re-emitting."
	out, err := Run(context.Background(), RunInput{
		Run:           run,
		Validator:     v,
		Prompt:        "ask",
		RetryReminder: reminder,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "retry should fire when first validation fails")
	assert.Contains(t, retryPrompt, reminder, "retry prompt must carry the reminder verbatim")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(retryPrompt), reminder),
		"reminder should be at the tail of the retry prompt so it lands close to the agent's next emit")
	_ = out
}

// TestRetryReminder_NotAppliedWhenEmpty — RunInput without a reminder
// must produce the same retry prompt as before (back-compat for
// existing callers like wiki/playbook).
func TestRetryReminder_NotAppliedWhenEmpty(t *testing.T) {
	v := &stubValidator{errs: []string{"[1] bogus"}}
	calls := 0
	var retryPrompt string
	run := func(_ context.Context, prompt string) (RawResult, error) {
		calls++
		if calls == 2 {
			retryPrompt = prompt
			v.cleared = true
		}
		return RawResult{Raw: `claim [1].

<<<CITATIONS
[{"kind":"slack_thread","channel_id":"C1","thread_ts":"1"}]
CITATIONS>>>`}, nil
	}
	_, err := Run(context.Background(), RunInput{Run: run, Validator: v, Prompt: "ask"})
	require.NoError(t, err)
	assert.NotContains(t, retryPrompt, "Reminder:",
		"no reminder configured → retry prompt must not contain reminder marker")
}
