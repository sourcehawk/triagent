package git

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/sourcehawk/triagent/skills"
)

// ArchitectureSummaryPromptArgs configures the freeform prompt for v1.
// Kind is reserved for future specialisations (controller / sdk / cli /
// service) but the design instead leans on Description — letting the
// sub-agent self-allocate emphasis based on the consumer-authored
// description of what the repo is replaces a multi-template engineering
// effort with one knob, and keeps consumer-specific framing out of the
// generic prompt.
type ArchitectureSummaryPromptArgs struct {
	Repo string // "owner/name"
	Kind string // "freeform" for v1
	// Description is the consumer-authored description of what this repo
	// is and when to consult it. When non-empty it's surfaced to the
	// sub-agent so it can bias section depth toward the parts that
	// match the description — e.g. a controller's reconcile loop, an
	// SDK's public API surface — without us shipping per-kind prompt
	// templates and without baking any specific consumer's domain
	// (incident response, code review, onboarding, …) into the prompt.
	Description string
	Focus       string // optional consumer hint, narrower than Description
	// OperatorEditsDiff is a unified diff representing edits a human
	// made to the previous cached summary. When non-empty it's
	// surfaced to the sub-agent as advisory context so the agent can
	// decide per-hunk whether each edit still applies and weave the
	// preserved intent into the fresh output. Empty means there's no
	// prior baseline or the operator hasn't edited.
	OperatorEditsDiff string
}

//go:embed summary_prompt.md
var architectureSummaryTmplSrc string

// architectureSummaryTmpl is the parsed prompt template. Parsed once
// at package init via template.Must — a malformed template fails the
// build's startup loudly rather than silently breaking on first call.
// The source lives at summary_prompt.md next to this file so prompt
// edits don't require touching Go code.
var architectureSummaryTmpl = template.Must(
	template.New("architecture_summary").Parse(architectureSummaryTmplSrc),
)

// ArchitectureSummaryPrompt builds the curated prompt sent to the
// sub-agent by executing the embedded summary_prompt.md template.
// Hard constraints in the prompt are restated multiple times because
// the sub-agent has demonstrably ignored single-mention constraints —
// edit the .md file to tune the wording.
func ArchitectureSummaryPrompt(args ArchitectureSummaryPromptArgs) string {
	// Trim the conditional fields so a whitespace-only value behaves
	// like an empty value at the template level: {{if .Description}}
	// considers a non-empty string truthy, but a value that's only
	// whitespace would render an effectively-empty section with stray
	// padding. Trimming here keeps the template's truthy-check honest
	// without needing a custom funcmap.
	args.Description = strings.TrimSpace(args.Description)
	args.Focus = strings.TrimSpace(args.Focus)
	args.OperatorEditsDiff = strings.TrimSpace(args.OperatorEditsDiff)

	var b strings.Builder
	if err := architectureSummaryTmpl.Execute(&b, args); err != nil {
		// The template was parsed at package init via Must; a runtime
		// Execute error would mean a field reference exists that
		// Parse accepted (e.g. {{.NewField}} added to the .md file
		// without adding it to the Go struct). Panic loudly rather
		// than ship a half-rendered prompt to the sub-agent.
		panic("architecture summary template execute: " + err.Error())
	}
	// The summary is a durable, operator-editable artifact and the
	// sub-agent has no launcher system prompt, so the writing rules
	// ride in the prompt itself.
	b.WriteString("\n\n# Writing style\n\nThe summary is descriptive prose. Every section obeys the rules below, with one exception: a section that has nothing to report is a single sentence, as the output structure above allows.\n\n")
	b.WriteString(skills.WritingSimply())
	return b.String()
}
