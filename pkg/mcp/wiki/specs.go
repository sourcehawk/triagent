package wiki

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the wiki server's tool catalog. Each tool's
// input shape is reflected from its Go struct (and jsonschema tags).
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{Server: "triagent-wiki", Name: "propose_wiki_draft", Description: "Draft an incident wiki entry for the current investigation. Spawns a focused sub-agent that writes a schema-conformant markdown file to the user's local proposals dir; the launcher's UI surfaces a review/approve/decline card.", Inputs: toolspec.FromStruct(proposeWikiDraftIn{})},
		{Server: "triagent-wiki", Name: "wiki_search", Description: "Free-text + filter search over the wiki. Primary audience: wiki-proposal drafting (find similar prior entries by keyword to model the new draft). For investigation-time recall use wiki_correlate instead. Entity-name filters are exact-match against canonical lowercase-with-hyphens names — call wiki_resolve_entities first to canonicalize fuzzy guesses. Output includes a `resolution` field with near-canonical suggestions for any non-exact input.", Inputs: toolspec.FromStruct(wikiSearchIn{})},
		{Server: "triagent-wiki", Name: "wiki_get", Description: "Fetch a single wiki note by vault-relative path. For incident notes, returns parsed frontmatter; for entity notes, returns backlinks.", Inputs: toolspec.FromStruct(wikiGetIn{})},
		{Server: "triagent-wiki", Name: "wiki_list_entities", Description: "List entity stubs in the wiki, optionally filtered by type (service|error|symptom|component). Names are lowercase-with-hyphens. Call before wiki_search/wiki_correlate so canonical names get used. Includes incident_count per entity. For targeted keyword-guess lookups use wiki_resolve_entities.", Inputs: toolspec.FromStruct(wikiListEntitiesIn{})},
		{Server: "triagent-wiki", Name: "wiki_resolve_entities", Description: "Canonicalise a candidate keyword set (services / errors / symptoms) against the vault's known entity names. Targeted alternative to wiki_list_entities — pass fuzzy guesses and get one Resolution per input with exact-match + near-matches by edit distance / substring.", Inputs: toolspec.FromStruct(resolveEntitiesIn{})},
		{Server: "triagent-wiki", Name: "wiki_correlate", Description: "Find prior incidents that share entities with the current findings, ranked by overlap. Primary audience: live investigation recall (strong opening move at triage time). For curation/proposal-flow keyword lookups use wiki_search instead. Entity names are exact-match — call wiki_resolve_entities first to canonicalize fuzzy guesses. Output's `resolution` field also surfaces near-canonical names for any input that didn't exact-match.", Inputs: toolspec.FromStruct(wikiCorrelateIn{})},
		{Server: "triagent-wiki", Name: "validate_wiki", Description: "Validate wiki markdown against the canonical schema. Auto-detects incident notes vs entity stubs. Returns {ok, kind, errors[]}.", Inputs: toolspec.FromStruct(validateWikiIn{})},
		{Server: "triagent-wiki", Name: "wiki_schema", Description: "Return the wiki frontmatter schema + authoring conventions as markdown.", Inputs: toolspec.FromStruct(wikiSchemaIn{})},
	}
}
