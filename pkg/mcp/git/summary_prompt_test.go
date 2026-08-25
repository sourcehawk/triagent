package git

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The summary is a durable artifact written by a fresh sub-agent, so the
// writing rules must be inside the prompt.
func TestArchitectureSummaryPrompt_AppendsWritingStyle(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{Repo: "o/n", Kind: "freeform"})
	assert.Contains(t, got, "# Writing style")
	assert.Contains(t, got, "### Self-check")
}

func TestArchitectureSummaryPrompt_Freeform_StructureAndConstraints(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/example-service",
		Kind: "freeform",
	})

	// The prompt must name the repo (sub-agent uses it as orientation).
	assert.Contains(t, got, "example-org/example-service")

	// Each top-level section the spec mandates must appear by name so the
	// sub-agent produces a consistent output shape. The sections are
	// engineered for incident triage — each one carries a specific kind
	// of signal (orientation, edges + failure direction, branching
	// configuration, observable failure strings, canonical vocabulary,
	// invariants/anti-patterns). Reordering or renaming requires
	// updating this list and the example block in the # Output section
	// of the prompt in lockstep.
	for _, section := range []string{
		"Orientation",
		"Runtime topology",
		"Configuration pivots",
		"Failure surfaces",
		"Domain language",
		"Invariants and anti-patterns",
	} {
		assert.Contains(t, got, section,
			"prompt missing required output section: %s", section)
	}

	// Hard constraints must be present verbatim — the sub-agent is told
	// to honour these or we produce a shallow / over-long summary.
	assert.Contains(t, got, "300-line", "soft target absent")
	assert.Contains(t, got, "500", "hard cap absent")
	assert.Contains(t, got, "5 lines", "code-block cap absent")
	assert.Contains(t, got, "Do not invent", "no-fabrication rule absent")
}

// The prompt previously asked for entry points / module layout / a
// directory map under a "Top-level structure" heading. Operators
// reported that those sections bloated the summary with repo trivia
// that goes stale fast and rarely helps incident triage (an agent
// chasing a code fix can rediscover layout with one Glob anyway). The
// rewrite bans those framings explicitly; lock in the ban so a future
// edit can't reintroduce them.
func TestArchitectureSummaryPrompt_BansSourceLayoutFraming(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/zeebe",
		Kind: "freeform",
	})
	assert.Contains(t, got, "out of scope",
		"prompt must frame source-code geography as out of scope")
	assert.Contains(t, got, "directory map",
		"prompt must explicitly ban directory maps")
	assert.Contains(t, got, "build instructions",
		"prompt must explicitly ban build/test instruction sections")
	assert.False(t,
		strings.Contains(got, "## Top-level structure") ||
			strings.Contains(got, "## Common breakage patterns"),
		"prompt must not reintroduce the old layout-/git-archaeology-oriented sections")
}

func TestArchitectureSummaryPrompt_FocusInjected(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo:  "example-org/zeebe",
		Kind:  "freeform",
		Focus: "concentrate on the partition leader-election path",
	})
	assert.Contains(t, got, "concentrate on the partition leader-election path",
		"focus hint must appear in the prompt verbatim")
}

func TestArchitectureSummaryPrompt_FocusOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/zeebe",
		Kind: "freeform",
	})
	assert.False(t, strings.Contains(got, "# Focus\n"),
		"empty focus must not produce a stub focus section")
}

func TestArchitectureSummaryPrompt_DescriptionInjected(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo:        "example-org/example-service",
		Kind:        "freeform",
		Description: "Kubernetes operator that reconciles cluster CRs and owns the zeebe-cluster condition strings.",
	})
	assert.Contains(t, got,
		"Kubernetes operator that reconciles cluster CRs and owns the zeebe-cluster condition strings.",
		"description must appear verbatim so the sub-agent can use it for emphasis")
	assert.Contains(t, got, "What this repo is",
		"description section header must be present")
}

func TestArchitectureSummaryPrompt_DescriptionOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/zeebe",
		Kind: "freeform",
	})
	assert.False(t, strings.Contains(got, "What this repo is"),
		"empty description must not produce a stub description section")
}

func TestArchitectureSummaryPrompt_OperatorEditsInjected(t *testing.T) {
	t.Parallel()
	diff := "--- baseline\n+++ active\n@@ -10,3 +10,4 @@\n line A\n+operator added: redis is the cache layer\n line B\n line C"
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo:              "example-org/zeebe",
		Kind:              "freeform",
		OperatorEditsDiff: diff,
	})
	assert.Contains(t, got, "Prior operator edits to consider",
		"section header must be present")
	assert.Contains(t, got, "operator added: redis is the cache layer",
		"diff body must appear verbatim so the agent can see the actual change")
	assert.Contains(t, got, "advisory, not authoritative",
		"key framing must be locked in — agent decides per-hunk whether to incorporate")
	assert.Contains(t, got, "omit it silently",
		"must instruct the agent NOT to mention edits it discarded; cached output ≠ changelog")
}

func TestArchitectureSummaryPrompt_OperatorEditsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/zeebe",
		Kind: "freeform",
	})
	assert.False(t, strings.Contains(got, "Prior operator edits"),
		"empty diff must not produce a stub edits section")
}

// Sub-agents demonstrably leak preambles ("I have enough context to
// write the architecture summary. Now I'll produce the markdown body.")
// into their final response. Telling the agent "don't do that" was
// insufficient on its own — generations kept landing in the cache with
// the preamble still attached. The current design wraps the body in
// BEGIN_SUMMARY / SUMMARY>>> sentinels and the orchestrator extracts
// only the fenced content. Lock in both sentinel strings and the
// surrounding framing so a prompt edit can't drop them without
// breaking the extractor's contract.
func TestArchitectureSummaryPrompt_OutputSentinels(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/zeebe",
		Kind: "freeform",
	})
	assert.Contains(t, got, summaryBeginMarker,
		"prompt must instruct the agent to emit the BEGIN_SUMMARY marker — the extractor matches it literally")
	assert.Contains(t, got, summaryEndMarker,
		"prompt must instruct the agent to emit the SUMMARY>>> end marker — the extractor matches it literally")
	assert.Contains(t, got, "preamble",
		"output instruction must explicitly name the preamble failure mode the sentinels are guarding against")
	assert.Contains(t, got, "extracts everything between",
		"prompt must explain that only sentinel-fenced content reaches the cache, so the agent understands narration outside is safe")
}

// The cache used to store `file:line` references the agent extracted
// from the repo at generation time. Line numbers go stale on every
// unrelated edit — operators reported references pointing at the wrong
// code days after generation. Lock in the file-path-only rule so a
// future prompt edit can't reintroduce line suffixes.
func TestArchitectureSummaryPrompt_ForbidsLineNumberReferences(t *testing.T) {
	t.Parallel()
	got := ArchitectureSummaryPrompt(ArchitectureSummaryPromptArgs{
		Repo: "example-org/zeebe",
		Kind: "freeform",
	})
	assert.Contains(t, got, "file path only",
		"prompt must direct the agent to file-path-only references (no line suffix)")
	assert.Contains(t, got, "stale",
		"prompt must explain WHY line numbers are forbidden, so the agent doesn't second-guess and reintroduce them")
}
