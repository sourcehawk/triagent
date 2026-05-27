package citations

import (
	"context"
	"fmt"
	"strings"
)

// RawResult is what the caller's sub-agent runner produces — the
// unparsed sub-agent stdout plus pass-through flags. The runner parses
// the citations block out of Raw before returning.
type RawResult struct {
	Raw      string
	TimedOut bool
}

// SubAgentFunc is the per-MCP sub-agent invocation seam. The runner
// calls it twice on the retry path. Transport-level errors (subprocess
// failure, context cancellation) propagate; protocol-level issues
// (malformed citations, validation failures) are signalled via the
// returned RawResult and the runner's parse/validate logic.
type SubAgentFunc func(ctx context.Context, prompt string) (RawResult, error)

// RunInput bundles the runner's dependencies. RetryReminder is the
// optional caller-supplied paragraph appended to the corrective-retry
// prompt — used by handlers whose sub-agent must preserve side effects
// across the retry (e.g. draft_pr's "you must have committed" / "keep
// the PR_TITLE/PR_BODY markers" contract). Empty for pure prose tools.
type RunInput struct {
	Run           SubAgentFunc
	Validator     Validator
	Prompt        string
	RetryReminder string
}

// Outcome is the parsed result the caller assembles into its tool output.
// On soft-fail (parse or validation failed both times), Citations is
// empty and CitationsParseError carries the diagnosis.
type Outcome struct {
	Prose               string
	Citations           []Citation
	CitationsParseError string
	TimedOut            bool
}

// Run invokes the sub-agent, parses the citations block, and validates.
// On failure (parse error, shape error, marker mismatch, or validator
// error) it retries once with a corrective re-prompt that includes the
// previous response verbatim and the diagnosis. Soft-fails to empty
// Citations + populated CitationsParseError if the retry also fails.
//
// First-call transport errors propagate as Go errors. Retry-call
// transport errors soft-fail, since the parent agent should still see
// the first attempt's prose.
func Run(ctx context.Context, in RunInput) (Outcome, error) {
	raw1, err := in.Run(ctx, in.Prompt)
	if err != nil {
		return Outcome{}, err
	}

	prose1, cits1, errs1 := parseAndValidate(raw1.Raw, in.Validator)
	if len(errs1) == 0 {
		return Outcome{Prose: prose1, Citations: cits1, TimedOut: raw1.TimedOut}, nil
	}

	retryPrompt := buildRetryPrompt(errs1, raw1.Raw, in.RetryReminder)
	raw2, err := in.Run(ctx, retryPrompt)
	if err != nil {
		return Outcome{
			Prose:               prose1,
			CitationsParseError: combine(errs1) + " (retry transport error: " + err.Error() + ")",
			TimedOut:            raw1.TimedOut,
		}, nil
	}
	prose2, cits2, errs2 := parseAndValidate(raw2.Raw, in.Validator)
	if len(errs2) == 0 {
		return Outcome{Prose: prose2, Citations: cits2, TimedOut: raw2.TimedOut}, nil
	}

	// Both attempts failed — surface the latest prose attempt + latest errors.
	finalProse := prose2
	if finalProse == "" {
		finalProse = prose1
	}
	return Outcome{
		Prose:               finalProse,
		CitationsParseError: combine(errs2),
		TimedOut:            raw2.TimedOut,
	}, nil
}

// parseAndValidate runs ParseBlock, ShapeCheck, MarkerCheck, and the
// validator's existence check. Returns the prose (best-effort even on
// parse error) and accumulated errors.
func parseAndValidate(raw string, v Validator) (string, []Citation, []string) {
	prose, cits, parseErr := ParseBlock(raw)
	if parseErr != nil {
		return prose, nil, []string{parseErr.Error()}
	}
	var errs []string
	errs = append(errs, ShapeCheck(cits)...)
	errs = append(errs, MarkerCheck(prose, cits)...)
	errs = append(errs, v.Validate(cits)...)
	return prose, cits, errs
}

func buildRetryPrompt(errs []string, previousResponse, reminder string) string {
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, "  - %s\n", e)
	}
	prompt := fmt.Sprintf(`Your previous response had citation errors:

%s
Your previous response (verbatim):
---
%s
---

Please re-emit the response with the citations corrected. Keep the same prose claims (or revise if they reference non-existent artifacts), but fix the <<<CITATIONS … CITATIONS>>> block so every claim's [N] marker resolves to a valid citation. Verify each citation against ground truth before re-emitting.`,
		b.String(), previousResponse)
	if reminder != "" {
		// Append the caller's side-effect reminder at the tail so it
		// lands close to the agent's next emit and is unambiguous.
		prompt += "\n\n" + reminder
	}
	return prompt
}

func combine(errs []string) string {
	return strings.Join(errs, "; ")
}
