package strategies

import (
	"context"
	"sort"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type playbookCorrelateIn struct {
	Services []string `json:"services,omitempty" jsonschema:"canonical service entity names lifted from findings, e.g. ['zeebe-broker']; ^[a-z0-9][a-z0-9-]*$"`
	Errors   []string `json:"errors,omitempty"   jsonschema:"canonical error entity names from findings, e.g. ['oom-kill']; ^[a-z0-9][a-z0-9-]*$"`
	Symptoms []string `json:"symptoms,omitempty" jsonschema:"canonical symptom entity names from findings, e.g. ['stuck-reconciliation']; ^[a-z0-9][a-z0-9-]*$"`
	Type     string   `json:"type,omitempty"     jsonschema:"optional type filter — 'investigation' / 'general'. Same semantics as list_playbooks. Omit for all."`
	Limit    int      `json:"limit,omitempty"    jsonschema:"max correlations to return; default 5"`
}

type playbookCorrelateMatch struct {
	ID          string            `json:"id"`
	Symptom     string            `json:"symptom"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"`
	Score       int               `json:"score"`
	MatchPath   playbookMatchPath `json:"match_path"`
}

type playbookMatchPath struct {
	Direct []string         `json:"direct,omitempty"`
	Lifted []playbookLifted `json:"lifted,omitempty"`
}

type playbookLifted struct {
	Entity string `json:"entity"`
	Via    string `json:"via"`
}

type playbookCorrelateOut struct {
	Correlations []playbookCorrelateMatch `json:"correlations"`
	Resolution   []entities.Resolution    `json:"resolution,omitempty"`
}

// playbookCorrelate ranks the loaded playbook set by entity overlap
// with the query set. Score = 3 * |direct hits| + 1 * |lifted hits
// not already direct|. Lifting walks one hop via delegate_to /
// handoff[*] only (next.goto is intra-playbook, validated by
// validateOne). Body is never returned — metadata only.
//
// Naive O(N) scan; the loaded set is dozens, not thousands. If the
// library grows large enough to hurt, swap in an inverted index
// (entityName → []playbookID) built once at New().
func (s *Server) playbookCorrelate(ctx context.Context, _ *mcp.CallToolRequest, in playbookCorrelateIn) (*mcp.CallToolResult, playbookCorrelateOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	emptyOut := playbookCorrelateOut{Correlations: []playbookCorrelateMatch{}}

	if len(in.Services) == 0 && len(in.Errors) == 0 && len(in.Symptoms) == 0 {
		return nil, emptyOut, nil
	}

	if err := entities.ValidateNames("services", "", in.Services); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	if err := entities.ValidateNames("errors", "", in.Errors); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}
	if err := entities.ValidateNames("symptoms", "", in.Symptoms); err != nil {
		return errorResult(err.Error()), emptyOut, nil
	}

	// Build the known-set lazily from the union of tags across all loaded
	// playbooks for resolution / near-match hints. Cheap (dozens of items).
	known := buildKnownEntities(s.playbooks)
	resolution := entities.ResolveKeywords(in.Services, in.Errors, in.Symptoms, known)

	querySvc := makeStringSet(in.Services)
	queryErr := makeStringSet(in.Errors)
	querySym := makeStringSet(in.Symptoms)

	matches := []playbookCorrelateMatch{}
	for id, pb := range s.playbooks {
		if in.Type != "" && pb.EffectiveType() != in.Type {
			continue
		}
		direct := directHits(pb, querySvc, queryErr, querySym)
		lifted := liftedHits(pb, s.playbooks, querySvc, queryErr, querySym, direct)
		score := 3*len(direct) + len(lifted)
		if score == 0 {
			continue
		}
		matches = append(matches, playbookCorrelateMatch{
			ID:          id,
			Symptom:     pb.Symptom,
			Description: pb.Description,
			Type:        pb.EffectiveType(),
			Score:       score,
			MatchPath: playbookMatchPath{
				Direct: direct,
				Lifted: lifted,
			},
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ID < matches[j].ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return nil, playbookCorrelateOut{Correlations: matches, Resolution: resolution}, nil
}

// buildKnownEntities collects canonical names from every loaded
// playbook's services / errors / symptoms arrays, grouped by type
// (keys "service" / "error" / "symptom" — must match the keys
// entities.ResolveKeywords indexes into).
// Duplicates collapse via the map; output is sorted for determinism.
func buildKnownEntities(books map[string]*Playbook) map[string][]string {
	seenSvc, seenErr, seenSym := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, pb := range books {
		for _, s := range pb.Services {
			seenSvc[s] = true
		}
		for _, e := range pb.Errors {
			seenErr[e] = true
		}
		for _, sym := range pb.Symptoms {
			seenSym[sym] = true
		}
	}
	out := map[string][]string{
		"service": sortedKeys(seenSvc),
		"error":   sortedKeys(seenErr),
		"symptom": sortedKeys(seenSym),
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func makeStringSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, v := range in {
		out[v] = true
	}
	return out
}

// directHits returns entities from the playbook's own services / errors
// / symptoms arrays that also appear in the query sets, deduped (each
// entity once per playbook even if it's tagged in multiple arrays).
// Order is preserved relative to the playbook's arrays.
func directHits(pb *Playbook, qSvc, qErr, qSym map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range pb.Services {
		if qSvc[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, e := range pb.Errors {
		if qErr[e] && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, sym := range pb.Symptoms {
		if qSym[sym] && !seen[sym] {
			seen[sym] = true
			out = append(out, sym)
		}
	}
	return out
}

// liftedHits walks the playbook's one-hop cross-playbook references
// (delegate_to / handoff[*]) and returns entities on those children
// that match the query sets, EXCLUDING entities already counted as
// direct hits on the parent. Per (entity, parent) we attribute one
// `via` — the first child encountered in deterministic-id order that
// supplied the entity. Cycle-safe via a visited set; depth = 1 only.
func liftedHits(parent *Playbook, books map[string]*Playbook, qSvc, qErr, qSym map[string]bool, directOnParent []string) []playbookLifted {
	directSet := map[string]bool{}
	for _, e := range directOnParent {
		directSet[e] = true
	}

	// Collect one-hop child ids in deterministic order.
	childIDs := collectOneHopChildren(parent)
	sort.Strings(childIDs)

	// seenEntity deduplicates on entity name regardless of which type array
	// supplied it — "x" as a service on child1 and a symptom on child2 counts once.
	seenEntity := map[string]bool{}
	var out []playbookLifted
	for _, cid := range childIDs {
		child, ok := books[cid]
		if !ok {
			continue
		}
		for _, s := range child.Services {
			if !qSvc[s] || directSet[s] || seenEntity[s] {
				continue
			}
			seenEntity[s] = true
			out = append(out, playbookLifted{Entity: s, Via: cid})
		}
		for _, e := range child.Errors {
			if !qErr[e] || directSet[e] || seenEntity[e] {
				continue
			}
			seenEntity[e] = true
			out = append(out, playbookLifted{Entity: e, Via: cid})
		}
		for _, sym := range child.Symptoms {
			if !qSym[sym] || directSet[sym] || seenEntity[sym] {
				continue
			}
			seenEntity[sym] = true
			out = append(out, playbookLifted{Entity: sym, Via: cid})
		}
	}
	return out
}

// collectOneHopChildren returns the unique set of OTHER playbook ids
// referenced by any node of `pb` via delegate_to or handoff[*]. Excludes
// self-references (the parent's own id) so a playbook can't lift from
// itself.
func collectOneHopChildren(pb *Playbook) []string {
	seen := map[string]bool{}
	for _, node := range pb.Nodes {
		if node.DelegateTo != "" && node.DelegateTo != pb.ID {
			seen[node.DelegateTo] = true
		}
		for _, h := range node.Handoff {
			if h != "" && h != pb.ID {
				seen[h] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}
