package wiki

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// ── wiki_search ───────────────────────────────────────────────────────────

type wikiSearchIn struct {
	Query   string            `json:"query,omitempty" jsonschema:"free-text keyword query against title and body; case-insensitive substring match"`
	Filters wikiSearchFilters `json:"filters,omitempty" jsonschema:"frontmatter filters; entity-name fields (services/errors/symptoms) require canonical lowercase-with-hyphens names — call wiki_list_entities to enumerate. Filters are OR within a field, AND across fields"`
	Limit   int               `json:"limit,omitempty" jsonschema:"max hits to return; default 10"`
}

type wikiSearchFilters struct {
	Services []string `json:"services,omitempty" jsonschema:"canonical service entity names, e.g. ['zeebe-broker']; must match ^[a-z0-9][a-z0-9-]*$"`
	Errors   []string `json:"errors,omitempty" jsonschema:"canonical error entity names, e.g. ['oom-kill']; must match ^[a-z0-9][a-z0-9-]*$"`
	Symptoms []string `json:"symptoms,omitempty" jsonschema:"canonical symptom entity names, e.g. ['stuck-reconciliation']; must match ^[a-z0-9][a-z0-9-]*$"`
	Severity []string `json:"severity,omitempty" jsonschema:"one or more of sev1|sev2|sev3"`
	Status   []string `json:"status,omitempty" jsonschema:"one or more of resolved|open|wontfix"`
}

type wikiSearchHit struct {
	ID          string      `json:"id"`
	Path        string      `json:"path"`
	Title       string      `json:"title"`
	Snippet     string      `json:"snippet,omitempty"`
	Frontmatter Frontmatter `json:"frontmatter"`
	Score       int         `json:"score"`
}

type wikiSearchOut struct {
	// Hits are the matching incident notes, highest-score first.
	Hits []wikiSearchHit `json:"hits"`
	// Resolution explains how each entity-name filter value mapped
	// against the vault's known entity set. One entry per (field, input)
	// pair. When Exact=false and Near is non-empty the agent should
	// consider re-running with the suggested canonical name. Empty
	// when no entity-name filters were passed.
	Resolution []EntityResolution `json:"resolution,omitempty"`
}

func (s *Server) wikiSearch(ctx context.Context, _ *mcp.CallToolRequest, in wikiSearchIn) (*mcp.CallToolResult, wikiSearchOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	// Reject malformed entity-name filters loudly. Silent empty
	// results train the agent to give up; an actionable error trains
	// it to call wiki_list_entities and pass canonical names.
	if err := entities.ValidateNames("filters.services", "wiki_list_entities", in.Filters.Services); err != nil {
		return errorResult(err.Error()), wikiSearchOut{}, nil
	}
	if err := entities.ValidateNames("filters.errors", "wiki_list_entities", in.Filters.Errors); err != nil {
		return errorResult(err.Error()), wikiSearchOut{}, nil
	}
	if err := entities.ValidateNames("filters.symptoms", "wiki_list_entities", in.Filters.Symptoms); err != nil {
		return errorResult(err.Error()), wikiSearchOut{}, nil
	}
	// Enum filters are loud-rejected for the same reason: a typo'd
	// "Resolved" silently matches no incidents and looks like
	// "no resolved incidents in the wiki", which is wrong.
	if err := validateStatusFilter(in.Filters.Status); err != nil {
		return errorResult(err.Error()), wikiSearchOut{}, nil
	}
	if err := validateSeverityFilter(in.Filters.Severity); err != nil {
		return errorResult(err.Error()), wikiSearchOut{}, nil
	}
	// Build the resolution table now (one vault walk for entity
	// names) so we can return it whether or not any incident matched.
	var resolution []EntityResolution
	if len(in.Filters.Services) > 0 || len(in.Filters.Errors) > 0 || len(in.Filters.Symptoms) > 0 {
		known, err := loadKnownEntitiesByType(s.vaultPath)
		if err != nil {
			return errorResult(err.Error()), wikiSearchOut{}, nil
		}
		resolution = entities.ResolveKeywords(in.Filters.Services, in.Filters.Errors, in.Filters.Symptoms, known)
	}
	paths, err := ListEntries(s.vaultPath)
	if err != nil {
		return errorResult(err.Error()), wikiSearchOut{}, nil
	}
	queryLower := strings.ToLower(strings.TrimSpace(in.Query))
	// Initialise non-nil so an empty result marshals as `"hits":[]`
	// instead of `"hits":null`. Agents read `null` as failure; `[]` reads
	// as "zero results" — the actual semantics we want here.
	hits := make([]wikiSearchHit, 0)
	for _, p := range paths {
		raw, err := ReadNote(s.vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, body, err := SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm Frontmatter
		if err := yamlUnmarshal(fmBytes, &fm); err != nil {
			continue
		}
		if !matchesFilters(fm, in.Filters) {
			continue
		}
		score := 0
		snippet := ""
		if queryLower != "" {
			titleHits := strings.Count(strings.ToLower(fm.Title), queryLower)
			bodyHits := strings.Count(strings.ToLower(string(body)), queryLower)
			score = 5*titleHits + bodyHits
			if score == 0 {
				continue
			}
			snippet = makeSnippet(string(body), queryLower)
		}
		hits = append(hits, wikiSearchHit{
			ID:          fm.ID,
			Path:        p,
			Title:       fm.Title,
			Snippet:     snippet,
			Frontmatter: fm,
			Score:       score,
		})
	}
	// Sort: higher score first; ties broken by date desc (newer first).
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Frontmatter.Date > hits[j].Frontmatter.Date
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return nil, wikiSearchOut{Hits: hits, Resolution: resolution}, nil
}

// matchesFilters returns true if fm satisfies every non-empty filter set
// (OR within a field, AND across fields). Empty filter sets pass.
func matchesFilters(fm Frontmatter, f wikiSearchFilters) bool {
	if len(f.Services) > 0 && !anyOverlap(fm.Services, f.Services) {
		return false
	}
	if len(f.Errors) > 0 && !anyOverlap(fm.Errors, f.Errors) {
		return false
	}
	if len(f.Symptoms) > 0 && !anyOverlap(fm.Symptoms, f.Symptoms) {
		return false
	}
	if len(f.Severity) > 0 && !contains(f.Severity, fm.Severity) {
		return false
	}
	if len(f.Status) > 0 && !contains(f.Status, fm.Status) {
		return false
	}
	return true
}

func anyOverlap(a, b []string) bool {
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if set[y] {
			return true
		}
	}
	return false
}

// contains reports whether needle is in haystack (exact match).
// Used by matchesFilters (severity/status) and wikiGet (backlink computation).
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func makeSnippet(body, queryLower string) string {
	lower := strings.ToLower(body)
	idx := strings.Index(lower, queryLower)
	if idx < 0 {
		return ""
	}
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + len(queryLower) + 60
	if end > len(body) {
		end = len(body)
	}
	out := body[start:end]
	out = strings.ReplaceAll(out, "\n", " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(body) {
		out = out + "…"
	}
	return out
}

// yamlUnmarshal is a thin alias so callers in this file stay import-clean.
func yamlUnmarshal(in []byte, out interface{}) error {
	return yaml.Unmarshal(in, out)
}

// ── wiki_get ─────────────────────────────────────────────────────────────

type wikiGetIn struct {
	Path string `json:"path" jsonschema:"vault-relative path; must be of the form entries/<slug>.md or entities/<services|errors|symptoms|components>/<entity-name>.md. Both slug and entity-name are lowercase-with-hyphens (^[a-z0-9][a-z0-9-]*$)."`
}

type wikiGetOut struct {
	Path        string      `json:"path"`
	Content     string      `json:"content"`
	Frontmatter Frontmatter `json:"frontmatter,omitempty"`
	Backlinks   []string    `json:"backlinks,omitempty"`
}

func (s *Server) wikiGet(ctx context.Context, _ *mcp.CallToolRequest, in wikiGetIn) (*mcp.CallToolResult, wikiGetOut, error) {
	if err := validateNotePath(in.Path); err != nil {
		return errorResult(err.Error()), wikiGetOut{}, nil
	}
	raw, err := ReadNote(s.vaultPath, in.Path)
	if err != nil {
		return errorResult(err.Error()), wikiGetOut{}, nil
	}
	out := wikiGetOut{Path: in.Path, Content: string(raw)}

	// If this looks like an incident, parse the frontmatter.
	if strings.HasPrefix(in.Path, "entries/") {
		fmBytes, _, err := SplitFrontmatter(raw)
		if err == nil {
			var fm Frontmatter
			if err := yaml.Unmarshal(fmBytes, &fm); err == nil {
				out.Frontmatter = fm
			}
		}
	}

	// If this looks like an entity, compute backlinks.
	if strings.HasPrefix(in.Path, "entities/") {
		entityName := strings.TrimSuffix(filepath.Base(in.Path), ".md")
		paths, _ := ListEntries(s.vaultPath)
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
			if contains(fm.Services, entityName) || contains(fm.Errors, entityName) || contains(fm.Symptoms, entityName) {
				out.Backlinks = append(out.Backlinks, p)
			}
		}
	}

	return nil, out, nil
}

// ── wiki_list_entities ──────────────────────────────────────────────────

type wikiListEntitiesIn struct {
	Type string `json:"type,omitempty" jsonschema:"optional filter; must be one of service|error|symptom|component (singular, lowercase). Empty = list every type"`
}

type wikiListEntitiesOut struct {
	Entities []EntityRefWithCount `json:"entities"`
}

// EntityRefWithCount embeds EntityRef and adds a count of referencing incidents.
type EntityRefWithCount struct {
	EntityRef
	IncidentCount int `json:"incident_count"`
}

func (s *Server) wikiListEntities(ctx context.Context, _ *mcp.CallToolRequest, in wikiListEntitiesIn) (*mcp.CallToolResult, wikiListEntitiesOut, error) {
	if err := validateEntityType(in.Type); err != nil {
		return errorResult(err.Error()), wikiListEntitiesOut{}, nil
	}
	refs, err := ListEntities(s.vaultPath, in.Type)
	if err != nil {
		return errorResult(err.Error()), wikiListEntitiesOut{}, nil
	}
	// Compute incident counts in one pass.
	counts := make(map[string]int)
	paths, _ := ListEntries(s.vaultPath)
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
		for _, n := range fm.Services {
			counts["service:"+n]++
		}
		for _, n := range fm.Errors {
			counts["error:"+n]++
		}
		for _, n := range fm.Symptoms {
			counts["symptom:"+n]++
		}
	}
	out := wikiListEntitiesOut{}
	for _, r := range refs {
		out.Entities = append(out.Entities, EntityRefWithCount{
			EntityRef:     r,
			IncidentCount: counts[r.Type+":"+r.Name],
		})
	}
	// Stable order: type then name.
	sort.SliceStable(out.Entities, func(i, j int) bool {
		if out.Entities[i].Type != out.Entities[j].Type {
			return out.Entities[i].Type < out.Entities[j].Type
		}
		return out.Entities[i].Name < out.Entities[j].Name
	})
	return nil, out, nil
}
