package git

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

type researchCodebaseIn struct {
	Question string `json:"question" jsonschema:"what to find out about the codebase, e.g. 'list every Prometheus metric the operator exposes with exact names' or 'which CRD condition types and reasons does the controller set?'"`
	Ref      string `json:"ref,omitempty" jsonschema:"git ref whose tree to inspect (commit sha, tag, or branch). Defaults to the remote default branch."`
}

type researchCodebaseOut struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref"`
	// ResolvedRef is the ref the sub-agent actually inspected. Differs
	// from Ref when it was empty (default branch) or a bare branch name
	// rewritten to origin/<name>.
	ResolvedRef         string               `json:"resolved_ref,omitempty"`
	Summary             string               `json:"summary"`
	Citations           []citations.Citation `json:"citations"`
	CitationsParseError string               `json:"citations_parse_error,omitempty"`
	TimedOut            bool                 `json:"timed_out,omitempty"`
}

// researchCodebase answers a question about the repository's tree at a
// ref. It is the whole-repo counterpart to analyze_change: that tool's
// prompt frames everything around one commit, so asking it "what
// metrics exist?" yields a relevance verdict on an unrelated change
// before the actual answer.
func (s *Server) researchCodebase(ctx context.Context, _ *mcp.CallToolRequest, in researchCodebaseIn) (*mcp.CallToolResult, researchCodebaseOut, error) {
	fail := func(msg string) (*mcp.CallToolResult, researchCodebaseOut, error) {
		return errorResult(msg), researchCodebaseOut{Repo: s.repoFull(), Ref: in.Ref, Citations: []citations.Citation{}}, nil
	}
	if in.Question == "" {
		return fail("question is required")
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return fail(err.Error())
	}
	resolvedRef, err := s.resolveRef(ctx, dir, in.Ref)
	if err != nil {
		return fail(err.Error())
	}
	prompt := fmt.Sprintf(
		`You are researching a repository's codebase to answer a question for an incident-triage playbook or investigation.
Inspect the tree as it exists at ref: %s
The question: %s

Use Read, Glob, Grep, and `+"`Bash(git ...)`"+` to inspect the code at that ref (e.g. `+"`git show <ref>:<path>`"+`, `+"`git ls-tree -r <ref>`"+`, `+"`git grep <pattern> <ref>`"+`). Stay within this repository — do not request external resources. Do not summarise what the commit at the ref changed; the question is about the codebase as a whole.

Reply with a focused answer under 600 words. Be exact with identifiers (metric names, kinds, condition types, flag names, file paths) — the answer feeds PromQL and kubectl queries. Mark each concrete claim with a numeric citation [N] (e.g. "[1]") and add the corresponding entry to the citations block at the end. If the codebase does not contain what was asked about, say so directly.

%s`,
		resolvedRef, in.Question, s.citationInstructions())

	parentID := telemetry.CurrentToolID(ctx)
	// sessionID threads claude's conversation id from the first sub-agent
	// turn into the citation-correction retry — same idea as analyze_change.
	var sessionID string
	adapter := func(ctx context.Context, p string) (citations.RawResult, error) {
		res, err := s.runSubAgent(ctx, dir, p, parentID, sessionID)
		if err != nil {
			return citations.RawResult{}, err
		}
		if res.SessionID != "" {
			sessionID = res.SessionID
		}
		return citations.RawResult{Raw: res.Summary, TimedOut: res.TimedOut}, nil
	}
	v := &gitValidator{repo: s.repoFull(), repoDir: dir, ctx: ctx}
	out, runErr := citations.Run(ctx, citations.RunInput{Run: adapter, Validator: v, Prompt: prompt})
	res := researchCodebaseOut{
		Repo:                s.repoFull(),
		Ref:                 in.Ref,
		ResolvedRef:         resolvedRef,
		Summary:             out.Prose,
		Citations:           out.Citations,
		CitationsParseError: out.CitationsParseError,
		TimedOut:            out.TimedOut,
	}
	if runErr != nil {
		return errorResult(runErr.Error()), res, nil
	}
	return nil, res, nil
}

// citationInstructions is the citation-format and self-verification
// tail shared by the read-only sub-agent prompts (analyze_change,
// research_codebase).
func (s *Server) citationInstructions() string {
	return fmt.Sprintf(
		`Citation format — REQUIRED. End your response with a block in this exact form:

<<<CITATIONS
[
  {"kind":"github_commit","repo":"%s","sha":"<full-40-char-sha>"},
  {"kind":"github_file","repo":"%s","path":"<relpath>","ref":"<sha-or-ref>","line_start":<n>,"line_end":<m>},
  {"kind":"github_pr","repo":"%s","pr_num":<n>}
]
CITATIONS>>>

Each [N] marker in your prose maps to citations[N-1] (1-based). Cite only artifacts in repo %s — the validator rejects entries pointing elsewhere. line_start/line_end are optional on github_file. github_commit requires the full 40-char sha, never an abbreviation.

Self-verify before emitting the citations block:
  - github_file: run `+"`Bash(git cat-file -e <ref>:<path>)`"+` to confirm the path exists at that ref. Drop on non-zero exit.
  - github_commit: run `+"`Bash(git rev-parse <sha>^{commit})`"+` to confirm the sha resolves. Drop on non-zero exit.
  - github_pr: run `+"`Bash(gh pr view <n> --repo %s)`"+` to confirm. Drop on non-zero exit.`,
		s.repoFull(), s.repoFull(), s.repoFull(), s.repoFull(), s.repoFull())
}
