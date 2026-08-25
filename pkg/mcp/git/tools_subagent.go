package git

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/citations"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

type analyzeChangeIn struct {
	Ref      string `json:"ref" jsonschema:"git ref to analyse (commit sha, tag, or branch)"`
	Question string `json:"question" jsonschema:"what to focus on, e.g. 'did this affect job timeout handling?' or 'what changed in the partition leader-election path?'"`
}

type analyzeChangeOut struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref"`
	// ResolvedRef is the ref `git` actually operated on (and that the
	// sub-agent prompt names). Differs from Ref when a bare branch name
	// was rewritten to origin/<name>.
	ResolvedRef         string               `json:"resolved_ref,omitempty"`
	Summary             string               `json:"summary"`
	Citations           []citations.Citation `json:"citations"`
	CitationsParseError string               `json:"citations_parse_error,omitempty"`
	TimedOut            bool                 `json:"timed_out,omitempty"`
}

type correlateIn struct {
	Findings string `json:"findings" jsonschema:"free-form description of incident findings (symptoms, error messages, affected components)"`
	RefRange string `json:"ref_range,omitempty" jsonschema:"optional git log range to scan, e.g. 'HEAD~50..HEAD' or 'v1.2.0..HEAD'. Defaults to commits within the last 30 days."`
}

type correlateOut struct {
	Repo                string               `json:"repo"`
	Summary             string               `json:"summary"`
	Citations           []citations.Citation `json:"citations"`
	CitationsParseError string               `json:"citations_parse_error,omitempty"`
	TimedOut            bool                 `json:"timed_out,omitempty"`
}

func (s *Server) analyzeChange(ctx context.Context, _ *mcp.CallToolRequest, in analyzeChangeIn) (*mcp.CallToolResult, analyzeChangeOut, error) {
	if in.Ref == "" || in.Question == "" {
		return errorResult("ref and question are required"), analyzeChangeOut{Repo: s.repoFull(), Ref: in.Ref, Citations: []citations.Citation{}}, nil
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), analyzeChangeOut{Repo: s.repoFull(), Ref: in.Ref, Citations: []citations.Citation{}}, nil
	}
	// Rewrite bare branch names to origin/<name> so the sub-agent's
	// git invocations operate on the fresh tip, not the stale local ref.
	resolvedRef, err := s.resolveRef(ctx, dir, in.Ref)
	if err != nil {
		return errorResult(err.Error()), analyzeChangeOut{Repo: s.repoFull(), Ref: in.Ref, Citations: []citations.Citation{}}, nil
	}
	prompt := fmt.Sprintf(
		`You are analysing a single git change in a repository for an incident investigation.
The change is at ref: %s
The investigator wants to know: %s

Use Read, Glob, Grep, and `+"`Bash(git show ...)`"+` to inspect the change and any code it touches. Stay within this repository — do not request external resources.

Reply with a focused answer under 400 words. Mark each concrete claim with a numeric citation [N] (e.g. "[1]") and add the corresponding entry to the citations block at the end. If the change does not appear relevant to the question, say so directly.

%s`,
		resolvedRef, in.Question, s.citationInstructions())

	parentID := telemetry.CurrentToolID(ctx)
	// sessionID is captured from the first sub-agent invocation and
	// passed back as Options.ResumeSessionID on the citation-correction
	// retry so the retry continues the same Claude conversation rather
	// than spawning a fresh one and re-paying the cost of repo
	// orientation.
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
	res := analyzeChangeOut{
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

func (s *Server) correlateWithFindings(ctx context.Context, _ *mcp.CallToolRequest, in correlateIn) (*mcp.CallToolResult, correlateOut, error) {
	if in.Findings == "" {
		return errorResult("findings is required"), correlateOut{Repo: s.repoFull(), Citations: []citations.Citation{}}, nil
	}
	refRange := in.RefRange
	if refRange == "" {
		refRange = "(commits within the last 30 days — use `git log --since='30 days'`)"
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), correlateOut{Repo: s.repoFull(), Citations: []citations.Citation{}}, nil
	}
	prompt := fmt.Sprintf(
		`You are correlating an incident's findings against recent code changes.

Findings: %s

Range to scan: %s

Use `+"`Bash(git log)`"+` to enumerate candidate commits in range, and Read/Glob/Grep to inspect any that look plausible. Rank up to 5 candidates by likelihood of having caused the symptoms. For each, write one or two sentences on why it correlates and a confidence label (high / medium / low).

Reply under 500 words total. Mark each concrete claim with a numeric citation [N] (e.g. "[1]") and add the corresponding entry to the citations block at the end. If nothing in the range looks relevant, say so directly. You must still emit the citations block — use an empty array if there is nothing to cite.

Citation format — REQUIRED. End your response with a block in this exact form:

<<<CITATIONS
[
  {"kind":"github_commit","repo":"%s","sha":"<full-40-char-sha>"},
  {"kind":"github_file","repo":"%s","path":"<relpath>","ref":"<sha-or-ref>"},
  {"kind":"github_pr","repo":"%s","pr_num":<n>}
]
CITATIONS>>>

Each [N] marker in your prose maps to citations[N-1] (1-based). Cite only artifacts in repo %s — the validator rejects entries pointing elsewhere. github_commit requires the full 40-char sha, never an abbreviation.

Self-verify before emitting the citations block:
  - github_commit: run `+"`Bash(git rev-parse <sha>^{commit})`"+` to confirm the sha resolves.
  - github_file: run `+"`Bash(git cat-file -e <ref>:<path>)`"+` to confirm the path exists at that ref.
  - github_pr: run `+"`Bash(gh pr view <n> --repo %s)`"+` to confirm.
Drop any citation whose check fails.`,
		in.Findings, refRange,
		s.repoFull(), s.repoFull(), s.repoFull(),
		s.repoFull(),
		s.repoFull())

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
	res := correlateOut{
		Repo:                s.repoFull(),
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
