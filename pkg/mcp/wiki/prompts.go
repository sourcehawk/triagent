package wiki

import (
	"fmt"
	"path/filepath"
	"strings"
)

// proposeWikiSubAgentPrompt assembles the curated prompt sent to the
// sub-agent that drafts a wiki entry. Keep this in sync with the schema
// described in docs/superpowers/specs/2026-05-06-investigations-wiki-design.md
// (note: schema_version stays at 1; the shape is the post-decoupling form).
func proposeWikiSubAgentPrompt(args proposeWikiPromptArgs) string {
	var entityList strings.Builder
	if len(args.Entities) == 0 {
		entityList.WriteString("(none yet — you will be the first to introduce entities)\n")
	} else {
		for _, e := range args.Entities {
			fmt.Fprintf(&entityList, "- [[%s]] (%s)\n", e.Name, e.Type)
		}
	}

	updateClause := ""
	if args.BaseMD != "" {
		updateClause = "\n\nThis is an UPDATE to an existing wiki entry. The current entry follows; revise it rather than rewriting from scratch. Preserve any human-edited content unless the new findings supersede it.\n\n----- existing entry -----\n" + args.BaseMD + "\n-----\n"
	}

	proposalsDir := filepath.Dir(args.DraftPath)
	entityStubPathTemplate := filepath.Join(proposalsDir, args.ProposalID+"__entity__<type>__<name>.md")

	severityLine := args.Severity
	if severityLine == "" {
		severityLine = "(omitted — unknown)"
	}

	transcriptSection := ""
	if strings.TrimSpace(args.InvestigationEventsPath) != "" {
		transcriptSection = fmt.Sprintf(`

# Investigation transcript (primary evidence)

The operator attached an investigation session. Read its transcript before drafting:

  %s

It's a JSON-lines file — one event per line (assistant turns, tool calls, tool results). For long files, read in chunks (offset + limit). Use what you find as the primary source for ## Summary / ## Root cause / ## Fix.`,
			args.InvestigationEventsPath,
		)
	}

	return fmt.Sprintf(`You are drafting a single wiki entry for the c1 investigations vault.

# Schema (must conform exactly)

Frontmatter (YAML, between %[1]s---%[1]s lines at top of file):
- schema_version: 1   # the wiki schema contract version; always 1 today
- id: %[2]s   # the orchestrator-supplied slug — echo VERBATIM, do not modify
- date: %[3]s
- title: a short human-readable title (one line, no trailing period)
- status: %[4]s     # filled in by the orchestrator after you finish — leave this value exactly as shown
- severity: %[5]s   # same — leave alone, do NOT change this value
- services: YAML array of canonical service names (use existing ones below where applicable; may be empty %[1]s[]%[1]s)
- errors: YAML array of canonical error names (may be empty %[1]s[]%[1]s)
- symptoms: YAML array of canonical symptom names (may be empty %[1]s[]%[1]s)

  Prefer SPECIFIC entity names over broad catchalls. Tags like %[1]sunhealthy%[1]s,
  %[1]sdegraded%[1]s, %[1]sslow%[1]s, %[1]sfailing%[1]s match almost any incident and
  inflate correlation gateway signals — they water down match precision in
  wiki_correlate and playbook_correlate. If the precise canonical name doesn't
  exist yet, coin a narrow new one (e.g. %[1]szeebe-backpressure%[1]s instead of
  %[1]sbackpressure%[1]s, %[1]selasticsearch-unhealthy%[1]s instead of %[1]sunhealthy%[1]s).
- links: {}   # the orchestrator fills in investigation / incident_io / slack_channel / slack_message after you finish — emit an empty mapping (or omit) and do NOT add any keys yourself

Body sections (in this order, each with the exact %[1]s##%[1]s heading):
- ## Summary — 1-2 paragraphs, what happened, impact
- ## Root cause — prose with [[wikilinks]] to entities (e.g. [[zeebe-broker]], [[oom-kill]])
- ## Fix — what resolved it, plus things that were tried but didn't work
- ## Lessons — optional but encouraged. Capture two flavours of learning side by side:
  - *Operator-facing* — signals to watch for next time, runbook gaps, alerting blind spots, environmental factors that masked the symptom.
  - *Agent-workflow retrospective* (prose, kept short) — which tool sequences paid off, which playbook branches mismatched the real failure mode, which evidence pointed reliably at root cause vs which was misleading, and any dead ends a future agent investigation should skip.

# Slug convention (informational — the id is already supplied above)

The orchestrator's slug follows a prefix convention so future readers can tell at a glance what kind of evidence anchors this entry:
- %[1]sinc-…%[1]s when there is an incident.io ticket (formal incident)
- %[1]sinv-…%[1]s when the strongest source is an investigation session (no external ticket)
- %[1]salert-…%[1]s when the entry is anchored on an alert / Slack message thread

You don't pick the slug — just respect the one above.

# Existing entities in the vault — prefer these names

%[6]s
When you reference an entity in body prose, use [[entity-name]] wikilink syntax. Match an existing name above when one is a clear fit; only invent a new entity name if nothing fits. Use lowercase-with-hyphens (no spaces).

# New entity stubs (REQUIRED for every new [[wikilink]])

For EVERY entity name you reference in body prose via [[wikilinks]] that is NOT in the existing entity list above, you MUST ALSO write a short stub file alongside the main draft. This is not optional — the propose_wiki_draft tool fails the call (and the sub-agent must re-run) when any new [[wikilink]] is missing its stub or has an empty description.

Use the Write tool to create:

  %[7]s

Where <type> is one of service|error|symptom|component (your best guess; the operator can re-classify later) and <name> is the entity's lowercase-with-hyphens slug, exactly matching the [[wikilink]] in the body.

The file must be only a frontmatter block (no markdown sections):

  ---
  type: <type>
  name: <name>
  description: <1-2 sentences describing what this entity is and why it matters for incident investigation — non-empty>
  ---

# Inputs

Investigation summary:
%[8]s

Additional context from the investigator agent:
%[9]s%[10]s%[12]s

# Output

Write the full markdown file (frontmatter + body) to:

  %[11]s

Use the Write tool. Do not print the content to stdout — the orchestrator reads the file you wrote. After writing the main draft (and any entity stubs), reply with one short sentence confirming the path you wrote to (no other output).
`,
		"`",                    // %[1]s
		args.Slug,              // %[2]s
		args.Date,              // %[3]s
		args.Status,            // %[4]s
		severityLine,           // %[5]s
		entityList.String(),    // %[6]s
		entityStubPathTemplate, // %[7]s
		args.Summary,           // %[8]s
		args.AdditionalContext, // %[9]s
		updateClause,           // %[10]s
		args.DraftPath,         // %[11]s
		transcriptSection,      // %[12]s
	)
}

type proposeWikiPromptArgs struct {
	Slug                    string
	Date                    string
	Status                  string
	Severity                string
	Summary                 string
	AdditionalContext       string
	InvestigationEventsPath string
	Entities                []EntityRef
	BaseMD                  string
	DraftPath               string
	ProposalID              string
}
