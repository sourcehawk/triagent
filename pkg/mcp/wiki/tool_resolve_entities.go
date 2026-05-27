package wiki

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// resolveEntitiesIn is the input for wiki_resolve_entities. Each
// field carries keyword guesses (which may be exact-match canonical
// names, near-misses, or wildly off) — the tool returns one
// Resolution per (field, input) telling the agent which canonical
// name (if any) it should retry with.
type resolveEntitiesIn struct {
	Services []string `json:"services,omitempty" jsonschema:"keyword guesses for service entity names; may be exact or near-miss. Pass them as you have them — the tool returns the canonical mapping. e.g. ['Zeebe Broker', 'oom kill']."`
	Errors   []string `json:"errors,omitempty"   jsonschema:"keyword guesses for error entity names. e.g. ['CrashLoopBackOff', 'circuit breaker']."`
	Symptoms []string `json:"symptoms,omitempty" jsonschema:"keyword guesses for symptom entity names. e.g. ['failing reconciliations', 'CPU throttle']."`
}

type resolveEntitiesOut struct {
	// Resolution is one entry per input keyword: {field, input,
	// exact, near}. Use `near[0]` as the canonical name to pass into
	// wiki_correlate / wiki_search.
	Resolution []entities.Resolution `json:"resolution"`
}

// wikiResolveEntities canonicalises a candidate keyword set against
// the vault's known entities. Unlike wiki_correlate / wiki_search,
// this tool does NOT validate input shape — its whole purpose is to
// take fuzzy input (mixed case, spaces, underscores) and return the
// canonical names. The agent calls this BEFORE wiki_correlate /
// wiki_search to canonicalise its keywords, then passes the
// canonical names through.
//
// Empty input returns an empty resolution. Reuses the shared
// resolver in mcp/internal/entities.
func (s *Server) wikiResolveEntities(ctx context.Context, _ *mcp.CallToolRequest, in resolveEntitiesIn) (*mcp.CallToolResult, resolveEntitiesOut, error) {
	emptyOut := resolveEntitiesOut{Resolution: []entities.Resolution{}}

	if len(in.Services) == 0 && len(in.Errors) == 0 && len(in.Symptoms) == 0 {
		return nil, emptyOut, nil
	}

	known, err := loadKnownEntitiesByType(s.vaultPath)
	if err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	resolution := entities.ResolveKeywords(in.Services, in.Errors, in.Symptoms, known)
	if resolution == nil {
		resolution = []entities.Resolution{}
	}
	return nil, resolveEntitiesOut{Resolution: resolution}, nil
}
