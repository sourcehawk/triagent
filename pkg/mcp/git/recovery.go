package git

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// citationSuffix returns " (citations: <err>)" for a non-empty citations
// parse error, empty string otherwise. Folded into the various draft_pr
// bail strings so the operator sees both the worktree-state diagnosis
// and the citations soft-fail reason when both are present.
func citationSuffix(parseErr string) string {
	if parseErr == "" {
		return ""
	}
	return fmt.Sprintf(" (citations: %s)", parseErr)
}

// needsClarification is the structured signal the draft_pr sub-agent
// emits when it cannot proceed because of missing information from the
// operator. Same shape as recoveryDecision: human-readable Missing
// list + Rationale, surfaced verbatim in the host's bail string so
// the operator sees what to provide on the next iteration.
type needsClarification struct {
	Missing   []string `json:"missing"`
	Rationale string   `json:"rationale"`
}

var needsClarificationPattern = regexp.MustCompile(
	`(?s)<<<NEEDS_CLARIFICATION\s*\n(.*?)\nNEEDS_CLARIFICATION>>>`)

// parseNeedsClarification extracts the marker from sub-agent prose.
// Returns ok=false on missing marker or malformed JSON — the host then
// falls through to the generic bail diagnostic.
func parseNeedsClarification(s string) (needsClarification, bool) {
	m := needsClarificationPattern.FindStringSubmatch(s)
	if len(m) < 2 {
		return needsClarification{}, false
	}
	var nc needsClarification
	if err := json.Unmarshal([]byte(m[1]), &nc); err != nil {
		return needsClarification{}, false
	}
	return nc, true
}

// buildBailDiagnostic composes the rich "what did the sub-agent
// actually do?" message folded into the no-changes bail string.
// Includes Summary (cleaned prose), PR_TITLE / PR_BODY (the structured
// markers the agent emitted, if any), and the citations parse error.
// Each piece is included only when non-empty so the diagnostic stays
// terse for the common case of a truly silent sub-agent.
func buildBailDiagnostic(summary, prTitle, prBody, citationsErr string) string {
	var parts []string
	if s := strings.TrimSpace(summary); s != "" {
		parts = append(parts, fmt.Sprintf("summary: %s", trimTo(s, 600)))
	}
	if s := strings.TrimSpace(prTitle); s != "" {
		parts = append(parts, fmt.Sprintf("PR_TITLE: %s", trimTo(s, 200)))
	}
	if s := strings.TrimSpace(prBody); s != "" {
		parts = append(parts, fmt.Sprintf("PR_BODY: %s", trimTo(s, 600)))
	}
	if s := strings.TrimSpace(citationsErr); s != "" {
		parts = append(parts, fmt.Sprintf("citations: %s", trimTo(s, 400)))
	}
	if len(parts) == 0 {
		return ""
	}
	return " — sub-agent state: " + strings.Join(parts, "; ")
}

// recoveryDecision is the structured signal the recovery sub-agent emits
// inside a <<<RECOVERY_DECISION ... RECOVERY_DECISION>>> marker. Action
// is one of "committed" | "abandoned"; Reason is a one-sentence
// rationale the host surfaces in the bail string. The marker carries
// the agent's stated *intent*; the host trusts but verifies against
// the actual worktree + git log post-state.
type recoveryDecision struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

var recoveryDecisionPattern = regexp.MustCompile(
	`(?s)<<<RECOVERY_DECISION\s*\n(.*?)\nRECOVERY_DECISION>>>`)

// parseRecoveryDecision extracts and JSON-decodes the recovery decision
// marker from the sub-agent's output. Returns ok=false on missing marker
// or malformed JSON — the host then treats it the same as "no signal"
// and falls through to the post-state-based bail.
func parseRecoveryDecision(s string) (recoveryDecision, bool) {
	m := recoveryDecisionPattern.FindStringSubmatch(s)
	if len(m) < 2 {
		return recoveryDecision{}, false
	}
	var dec recoveryDecision
	if err := json.Unmarshal([]byte(m[1]), &dec); err != nil {
		return recoveryDecision{}, false
	}
	return dec, true
}

// buildRecoveryPrompt constructs the focused prompt for the recovery
// sub-agent. The agent has only two outcomes — commit-and-emit-marker
// OR clean-up-and-emit-marker — and is forbidden from editing files
// (only stage + commit OR revert + remove). Scoped tightly to avoid
// drifting into "let me also fix this other thing".
func buildRecoveryPrompt(issueURL, statusPorcelain string) string {
	return fmt.Sprintf(`Your previous turn (for issue %s) made these file changes but did not commit them:

%s

Decide which of the two outcomes applies:

  (a) The changes are good — run `+"`git add`"+` + `+"`git commit -m \"<concise subject>\"`"+`
      to land them on the current branch. Match the repo's existing commit
      style (check `+"`git log --oneline -10`"+`).

  (b) The changes are not good — run `+"`git restore --staged --worktree .`"+`
      (and `+"`rm`"+` any new files you created) to bring the worktree back to a
      clean state, then explain why the codefix is being abandoned. The
      operator will see your reason in the chat.

Do not edit any files in this turn — just stage+commit OR revert+remove.
After you finish, emit exactly this marker so the host can record your decision:

<<<RECOVERY_DECISION
{"action":"committed"|"abandoned","reason":"<one sentence>"}
RECOVERY_DECISION>>>`, issueURL, statusPorcelain)
}
