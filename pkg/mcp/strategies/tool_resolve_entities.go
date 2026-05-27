package strategies

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// resolveEntitiesIn is the input for playbook_resolve_entities. Each
// field carries keyword guesses (exact, near-misses, or wildly off)
// — the tool returns one Resolution per (field, input) telling the
// agent which canonical entity name (if any) it should retry with.
type resolveEntitiesIn struct {
	Services []string `json:"services,omitempty" jsonschema:"keyword guesses for service entity names; may be exact or near-miss. Pass them as you have them — the tool returns the canonical mapping. e.g. ['Zeebe Broker', 'crash looping']."`
	Errors   []string `json:"errors,omitempty"   jsonschema:"keyword guesses for error entity names. e.g. ['CrashLoopBackOff', 'circuit breaker']."`
	Symptoms []string `json:"symptoms,omitempty" jsonschema:"keyword guesses for symptom entity names. e.g. ['failing reconciliations', 'CPU throttle']."`
}

type resolveEntitiesOut struct {
	// Resolution is one entry per input keyword: {field, input,
	// exact, near}. Use `near[0]` as the canonical name to pass into
	// playbook_correlate.
	Resolution []entities.Resolution `json:"resolution"`
}

// playbookResolveEntities canonicalises a candidate keyword set
// against the union of all loaded playbooks' services / errors /
// symptoms tags. Unlike playbook_correlate, this tool does NOT
// validate input shape — its whole purpose is to accept fuzzy input
// (mixed case, spaces, underscores) and return the canonical names.
// The agent calls this BEFORE playbook_correlate to canonicalise its
// keywords, then passes the canonical names through.
//
// Empty input returns an empty resolution. Reuses the shared
// resolver in mcp/internal/entities.
func (s *Server) playbookResolveEntities(ctx context.Context, _ *mcp.CallToolRequest, in resolveEntitiesIn) (*mcp.CallToolResult, resolveEntitiesOut, error) {
	emptyOut := resolveEntitiesOut{Resolution: []entities.Resolution{}}

	if len(in.Services) == 0 && len(in.Errors) == 0 && len(in.Symptoms) == 0 {
		return nil, emptyOut, nil
	}

	known := buildKnownEntities(s.playbooks)
	resolution := entities.ResolveKeywords(in.Services, in.Errors, in.Symptoms, known)
	if resolution == nil {
		resolution = []entities.Resolution{}
	}
	return nil, resolveEntitiesOut{Resolution: resolution}, nil
}
