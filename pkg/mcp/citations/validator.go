package citations

// Validator is the per-MCP plug-in seam for citation existence checks.
// The runner (Run) handles ParseBlock, ShapeCheck, and MarkerCheck before
// invoking Validate, so validators should focus on ground-truth lookups
// for their kinds (does this thread exist in the cache snapshot? does
// this file exist at this ref in the cloned repo?).
//
// Validate returns one human-readable error per problem found; an empty
// slice means all entries are valid. Errors are surfaced verbatim into
// the corrective re-prompt, so phrase them as actionable diagnostics.
type Validator interface {
	Validate(citations []Citation) []string
}
