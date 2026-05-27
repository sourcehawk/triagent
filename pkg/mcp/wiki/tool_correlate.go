package wiki

import (
	"context"
	"sort"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

type wikiCorrelateIn struct {
	Services []string `json:"services,omitempty" jsonschema:"canonical service entity names lifted from current findings, e.g. ['zeebe-broker']; must match ^[a-z0-9][a-z0-9-]*$ — call wiki_list_entities first"`
	Errors   []string `json:"errors,omitempty" jsonschema:"canonical error entity names from current findings, e.g. ['oom-kill']; must match ^[a-z0-9][a-z0-9-]*$"`
	Symptoms []string `json:"symptoms,omitempty" jsonschema:"canonical symptom entity names from current findings, e.g. ['stuck-reconciliation']; must match ^[a-z0-9][a-z0-9-]*$"`
	Limit    int      `json:"limit,omitempty" jsonschema:"max correlations to return; default 5"`
}

type wikiCorrelateOverlap struct {
	Services []string `json:"services,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	Symptoms []string `json:"symptoms,omitempty"`
}

type wikiCorrelateMatch struct {
	Slug        string               `json:"slug"`
	Title       string               `json:"title"`
	Path        string               `json:"path"`
	Score       int                  `json:"score"`
	Overlap     wikiCorrelateOverlap `json:"overlap"`
	Frontmatter Frontmatter          `json:"frontmatter"`
}

type wikiCorrelateOut struct {
	// Correlations are the top-N most-overlapping past incidents.
	Correlations []wikiCorrelateMatch `json:"correlations"`
	// Resolution explains how each input keyword mapped against the
	// vault's known entity set. Same shape as wiki_search.Resolution:
	// when Exact=false and Near is non-empty the agent should consider
	// re-running with the suggested canonical name. Empty when no
	// keywords were passed.
	Resolution []EntityResolution `json:"resolution,omitempty"`
}

// wikiCorrelate ranks all incidents by entity-overlap with the input
// candidate set, returns the top Limit. Score = total count of overlapping
// entities across services / errors / symptoms (each entity counted once).
//
// Naive O(N) scan over incidents in v1 — read frontmatter, intersect with
// query, score, sort. If the vault grows large enough that latency hurts,
// swap in an in-memory inverted index (entityName → []incidentID) built at
// MCP server startup. The tool surface is implementation-agnostic so the
// switch is a same-day refactor.
func (s *Server) wikiCorrelate(ctx context.Context, _ *mcp.CallToolRequest, in wikiCorrelateIn) (*mcp.CallToolResult, wikiCorrelateOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	// Empty result is always a non-nil slice so the JSON serialises
	// as [] rather than null. Agents see the difference: null reads
	// as "tool errored / index missing"; [] reads as "no matches".
	emptyOut := wikiCorrelateOut{Correlations: []wikiCorrelateMatch{}}

	// Empty query → empty result; the agent should pass at least one
	// candidate entity. Don't error — return an empty list so chained
	// callers don't have to special-case.
	if len(in.Services) == 0 && len(in.Errors) == 0 && len(in.Symptoms) == 0 {
		return nil, emptyOut, nil
	}

	// Reject malformed names loudly. Same rationale as wiki_search.
	if err := entities.ValidateNames("services", "wiki_list_entities", in.Services); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	if err := entities.ValidateNames("errors", "wiki_list_entities", in.Errors); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	if err := entities.ValidateNames("symptoms", "wiki_list_entities", in.Symptoms); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}

	known, err := loadKnownEntitiesByType(s.vaultPath)
	if err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	resolution := entities.ResolveKeywords(in.Services, in.Errors, in.Symptoms, known)

	paths, err := ListEntries(s.vaultPath)
	if err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}

	queryServices := makeSet(in.Services)
	queryErrors := makeSet(in.Errors)
	querySymptoms := makeSet(in.Symptoms)

	matches := []wikiCorrelateMatch{}
	for _, p := range paths {
		raw, err := ReadNote(s.vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, _, err := SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			continue
		}
		olServices := intersect(fm.Services, queryServices)
		olErrors := intersect(fm.Errors, queryErrors)
		olSymptoms := intersect(fm.Symptoms, querySymptoms)
		score := len(olServices) + len(olErrors) + len(olSymptoms)
		if score == 0 {
			continue
		}
		matches = append(matches, wikiCorrelateMatch{
			Slug:       fm.ID,
			Title:      fm.Title,
			Path:       p,
			Score:      score,
			Overlap: wikiCorrelateOverlap{
				Services: olServices,
				Errors:   olErrors,
				Symptoms: olSymptoms,
			},
			Frontmatter: fm,
		})
	}

	// Sort: highest score first. Ties broken by date desc (newer first)
	// so recent incidents float to the top of equal-overlap clusters.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Frontmatter.Date > matches[j].Frontmatter.Date
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return nil, wikiCorrelateOut{Correlations: matches, Resolution: resolution}, nil
}

// makeSet returns a set-like map for fast lookup.
func makeSet(in []string) map[string]bool {
	s := make(map[string]bool, len(in))
	for _, v := range in {
		s[v] = true
	}
	return s
}

// intersect returns names from `have` that are present in `query`,
// preserving the order of `have`.
func intersect(have []string, query map[string]bool) []string {
	if len(have) == 0 || len(query) == 0 {
		return nil
	}
	var out []string
	for _, name := range have {
		if query[name] {
			out = append(out, name)
		}
	}
	return out
}
