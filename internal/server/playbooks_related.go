package server

import (
	"encoding/json"
	"net/http"
	"sort"

	"gopkg.in/yaml.v3"
)

// relatedMatch is one entry in the GET /api/playbooks/{id}/related
// response. Shape mirrors the strategies MCP's playbookCorrelateMatch
// (playbook_correlate tool) so the TypeScript client can share a type.
type relatedMatch struct {
	ID          string           `json:"id"`
	Symptom     string           `json:"symptom,omitempty"`
	Description string           `json:"description,omitempty"`
	Type        string           `json:"type"`
	Score       int              `json:"score"`
	MatchPath   relatedMatchPath `json:"match_path"`
}

type relatedMatchPath struct {
	Direct []string        `json:"direct,omitempty"`
	Lifted []relatedLifted `json:"lifted,omitempty"`
}

type relatedLifted struct {
	Entity string `json:"entity"`
	Via    string `json:"via"`
}

// playbookEntityTags holds the entity tags extracted from a playbook's YAML,
// including node-level cross-playbook references (delegate_to / handoff[*])
// used for one-hop lifting.
type playbookEntityTags struct {
	Services []string                          `yaml:"services"`
	Errors   []string                          `yaml:"errors"`
	Symptoms []string                          `yaml:"symptoms"`
	Nodes    map[string]playbookEntityTagsNode `yaml:"nodes"`
}

type playbookEntityTagsNode struct {
	DelegateTo string   `yaml:"delegate_to"`
	Handoff    []string `yaml:"handoff"`
}

// extractEntityTags parses the entity tags (services / errors / symptoms)
// and node-level cross-playbook references from a playbook YAML body.
// Malformed YAML returns an empty struct — callers treat that as "no tags",
// which produces a zero score.
func extractEntityTags(body string) playbookEntityTags {
	var out playbookEntityTags
	_ = yaml.Unmarshal([]byte(body), &out)
	return out
}

// collectOneHopChildren returns the unique ids of OTHER playbooks
// referenced by any node via delegate_to or handoff[]. Excludes
// self-references. Output is sorted for deterministic lift attribution.
func collectOneHopChildren(self string, tags playbookEntityTags) []string {
	seen := map[string]bool{}
	for _, n := range tags.Nodes {
		if n.DelegateTo != "" && n.DelegateTo != self {
			seen[n.DelegateTo] = true
		}
		for _, h := range n.Handoff {
			if h != "" && h != self {
				seen[h] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scorePlaybook computes a correlation score: 3 * direct + 1 * lifted-not-already-direct.
// candidateID is the id of the candidate (used to exclude self-references in lift).
// allTags maps every loaded playbook's id to its parsed tags (for child lookup).
//
// This mirrors the scoring logic in mcp/internal/strategies/tool_correlate.go.
// If that logic changes, update both places — or promote a shared package.
func scorePlaybook(candidateID string, candidate playbookEntityTags, allTags map[string]playbookEntityTags, qSvc, qErr, qSym map[string]bool) (score int, direct []string, lifted []relatedLifted) {
	seen := map[string]bool{}
	for _, s := range candidate.Services {
		if qSvc[s] && !seen[s] {
			seen[s] = true
			direct = append(direct, s)
		}
	}
	for _, e := range candidate.Errors {
		if qErr[e] && !seen[e] {
			seen[e] = true
			direct = append(direct, e)
		}
	}
	for _, sym := range candidate.Symptoms {
		if qSym[sym] && !seen[sym] {
			seen[sym] = true
			direct = append(direct, sym)
		}
	}

	// Lifting: walk one-hop children via delegate_to / handoff[], collect
	// matching entities NOT already in `direct`. Per-entity dedup: same
	// entity from multiple children counts once (attributed to the first
	// child encountered in sorted-id order).
	directSet := map[string]bool{}
	for _, e := range direct {
		directSet[e] = true
	}
	seenLifted := map[string]bool{}
	for _, cid := range collectOneHopChildren(candidateID, candidate) {
		child, ok := allTags[cid]
		if !ok {
			continue
		}
		for _, s := range child.Services {
			if !qSvc[s] || directSet[s] || seenLifted[s] {
				continue
			}
			seenLifted[s] = true
			lifted = append(lifted, relatedLifted{Entity: s, Via: cid})
		}
		for _, e := range child.Errors {
			if !qErr[e] || directSet[e] || seenLifted[e] {
				continue
			}
			seenLifted[e] = true
			lifted = append(lifted, relatedLifted{Entity: e, Via: cid})
		}
		for _, sym := range child.Symptoms {
			if !qSym[sym] || directSet[sym] || seenLifted[sym] {
				continue
			}
			seenLifted[sym] = true
			lifted = append(lifted, relatedLifted{Entity: sym, Via: cid})
		}
	}

	score = 3*len(direct) + len(lifted)
	return score, direct, lifted
}

func makeSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, v := range in {
		out[v] = true
	}
	return out
}

// hasAny reports whether any of the three entity arrays is non-empty.
// Used to distinguish "operator passed an explicit override" from
// "fall back to the playbook's saved tags".
func (t playbookEntityTags) hasAny() bool {
	return len(t.Services) > 0 || len(t.Errors) > 0 || len(t.Symptoms) > 0
}

// queryTagsFromRequest parses ?services=&errors=&symptoms= repeated
// query params into a playbookEntityTags struct. Empty struct when no
// query params are present; the caller decides whether to fall back to
// the playbook's saved tags. Nodes field is left nil — query overrides
// only affect direct scoring, not lifting (which would need the full
// playbook structure, not just the tag set).
func queryTagsFromRequest(r *http.Request) playbookEntityTags {
	q := r.URL.Query()
	return playbookEntityTags{
		Services: q["services"],
		Errors:   q["errors"],
		Symptoms: q["symptoms"],
	}
}

// handleRelatedPlaybooks returns up to 5 playbooks correlated with the
// queried playbook's own entity tags (services / errors / symptoms).
// The queried playbook is filtered out of the results — a playbook is
// never related to itself.
//
// Accepts optional ?services=&errors=&symptoms= query params (repeated)
// that OVERRIDE the playbook's saved tags as the query set. The frontend
// uses this to preview correlations as the operator edits chip inputs
// in the editor, without having to save first.
//
// GET /api/playbooks/{id}/related[?services=...&errors=...&symptoms=...]
func (a *apiHandlers) handleRelatedPlaybooks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	meta := a.metaCache.get()
	if meta == nil {
		// Cache not populated (loadMeta failed at startup or during
		// tests). Treat as "no playbooks" → empty related set.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"related": []relatedMatch{}})
		return
	}

	mp, ok := meta.Playbooks[id]
	if !ok {
		// Also check user playbooks from disk as a fallback when the id
		// is user-only (not in the cached meta). Use the same YAML parse
		// path as collectPlaybooks pass-2.
		groups, err := loadUserPlaybookGroups(a.opts.UserPlaybooksDir)
		if err != nil || groups == nil {
			http.Error(w, "playbook not found", http.StatusNotFound)
			return
		}
		g, found := groups[id]
		if !found {
			http.Error(w, "playbook not found", http.StatusNotFound)
			return
		}
		mp = MetaPlaybook{YAML: g.YAML, Source: "user", Type: g.Type}
	}

	// Query set: prefer ?services=&errors=&symptoms= overrides when
	// present (live-preview from the editor's chip inputs before save);
	// otherwise read the playbook's saved tags.
	query := queryTagsFromRequest(r)
	if !query.hasAny() {
		query = extractEntityTags(mp.YAML)
	}

	// If the query has no entity tags, return an empty set —
	// there's nothing to correlate on.
	if len(query.Services) == 0 && len(query.Errors) == 0 && len(query.Symptoms) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"related": []relatedMatch{}})
		return
	}

	qSvc := makeSet(query.Services)
	qErr := makeSet(query.Errors)
	qSym := makeSet(query.Symptoms)

	// Score all candidate playbooks; self-filter at iteration time and
	// trim to maxRelated after sorting by score / id.
	var matches []relatedMatch

	// Collect candidate playbooks from meta cache.
	allPlaybooks := make(map[string]MetaPlaybook, len(meta.Playbooks))
	for k, v := range meta.Playbooks {
		allPlaybooks[k] = v
	}
	// Also include user playbooks not in cache.
	if groups, err := loadUserPlaybookGroups(a.opts.UserPlaybooksDir); err == nil {
		for k, g := range groups {
			if _, exists := allPlaybooks[k]; !exists {
				allPlaybooks[k] = MetaPlaybook{YAML: g.YAML, Source: "user", Type: g.Type}
			}
		}
	}

	// Pre-parse every candidate's tags into a map (used for child lookup
	// during lifting). Cheap — dozens of playbooks at most.
	allTags := make(map[string]playbookEntityTags, len(allPlaybooks))
	for cid, cmp := range allPlaybooks {
		allTags[cid] = extractEntityTags(cmp.YAML)
	}

	for cid, cmp := range allPlaybooks {
		if cid == id {
			continue // never match self
		}
		tags := allTags[cid]
		score, direct, lifted := scorePlaybook(cid, tags, allTags, qSvc, qErr, qSym)
		if score == 0 {
			continue
		}
		pbType := cmp.Type
		if pbType == "" {
			pbType = "investigation"
		}
		summary := summarisePlaybookYAML(cmp.YAML)
		matches = append(matches, relatedMatch{
			ID:          cid,
			Symptom:     summary.symptom,
			Description: summary.description,
			Type:        pbType,
			Score:       score,
			MatchPath: relatedMatchPath{
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

	const maxRelated = 5
	if len(matches) > maxRelated {
		matches = matches[:maxRelated]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"related": matches})
}
