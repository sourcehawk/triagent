package wiki

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type wikiSchemaIn struct{}

type wikiSchemaOut struct {
	Schema string `json:"schema"`
}

// wikiSchema returns a markdown description of the wiki frontmatter
// schema + authoring conventions. Mirrors triagent-strategies/playbook_schema.
// Markdown rather than JSON Schema because agents authoring YAML do
// significantly better with prose + a worked example than with a raw
// schema document. The structural validator (validate_wiki) is the
// authoritative gate; this string is the "how to author" README.
func (s *Server) wikiSchema(ctx context.Context, req *mcp.CallToolRequest, _ wikiSchemaIn) (*mcp.CallToolResult, wikiSchemaOut, error) {
	return nil, wikiSchemaOut{Schema: wikiSchemaMarkdown}, nil
}

const wikiSchemaMarkdown = "# Wiki entry schema\n\n" +
	"An entry is a markdown file under `<vault>/entries/<slug>.md`. It has a YAML frontmatter block followed by markdown body sections.\n\n" +
	"## Frontmatter\n\n" +
	"| field          | type            | required | meaning |\n" +
	"|----------------|-----------------|----------|---------|\n" +
	"| `schema_version` | int           | yes      | always `1` for the current contract |\n" +
	"| `id`           | string          | yes      | the entry's slug — `^[a-z0-9][a-z0-9-]*$`. Match the filename. |\n" +
	"| `date`         | string (YYYY-MM-DD) | yes  | when the underlying incident occurred / was logged |\n" +
	"| `title`        | string          | yes      | one-line human title, no trailing punctuation |\n" +
	"| `status`       | string          | yes      | one of `resolved`, `open`, `wontfix` |\n" +
	"| `severity`     | string          | no       | one of `sev1`, `sev2`, `sev3`; omit when unknown |\n" +
	"| `services`     | list<string>    | yes      | canonical service-entity names; lowercase-with-hyphens; may be empty `[]` |\n" +
	"| `errors`       | list<string>    | yes      | canonical error-entity names; may be empty `[]` |\n" +
	"| `symptoms`     | list<string>    | yes      | canonical symptom-entity names; may be empty `[]` |\n" +
	"| `links`        | mapping         | no       | citations — see below |\n\n" +
	"### `links` mapping\n\n" +
	"All fields optional. Every value is a URL string.\n\n" +
	"| field            | meaning |\n" +
	"|------------------|---------|\n" +
	"| `investigation`  | launcher URL for the investigation session (`/investigations/?id=<session-id>`) |\n" +
	"| `incident_io`    | incident.io ticket URL |\n" +
	"| `slack_channel`  | Slack channel URL |\n" +
	"| `slack_message`  | Slack message permalink (alert/thread) |\n\n" +
	"## Slug convention\n\n" +
	"The slug is free-form (`^[a-z0-9][a-z0-9-]*$`) but by convention prefixes hint at the strongest source:\n\n" +
	"- `inc-…` when an incident.io ticket exists (formal incident)\n" +
	"- `inv-…` when an investigation session is the strongest anchor\n" +
	"- `alert-…` when anchored on an alert / Slack message thread\n\n" +
	"## Body\n\n" +
	"Required headings (case-sensitive, in this order):\n\n" +
	"- `## Summary` — 1-2 paragraphs of what happened and impact\n" +
	"- `## Root cause` — prose with `[[wikilink]]` entity references\n" +
	"- `## Fix` — what resolved it, plus things tried that didn't work\n\n" +
	"Optional but encouraged: `## Lessons` (operator-facing + agent-retrospective).\n\n" +
	"## Prose style\n\n" +
	"Body prose obeys the Writing style section of your system prompt. `## Summary` and `## Root cause` are descriptive: simple past, sentences under 25 words, active voice. `## Fix` states what resolved the incident, then what was tried and did not work. `## Lessons` bullets are imperative. Do not repeat a fact in two sections.\n\n" +
	"## Entity stubs\n\n" +
	"Every new `[[wikilink]]` requires a sibling stub at `<vault>/entities/<type>/<name>.md`. Stub frontmatter:\n\n" +
	"```yaml\n" +
	"---\n" +
	"schema_version: 1\n" +
	"type: service|error|symptom|component\n" +
	"name: lowercase-with-hyphens\n" +
	"created: YYYY-MM-DD\n" +
	"---\n" +
	"```\n"
