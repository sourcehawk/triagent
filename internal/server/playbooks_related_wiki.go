package server

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/sourcehawk/triagent/internal/wiki"
	"gopkg.in/yaml.v3"
)

// relatedWikiMatch is one entry in the GET /api/playbooks/{id}/related-wiki
// response. Shape parallels relatedMatch but is wiki-flavoured: incident
// notes are identified by slug + title, not playbook id + symptom, and
// there's no `lifted` shape since wiki entries don't delegate / handoff.
type relatedWikiMatch struct {
	ID          string                  `json:"id"`
	Title       string                  `json:"title,omitempty"`
	Path        string                  `json:"path"`
	Status      string                  `json:"status,omitempty"`
	Severity    string                  `json:"severity,omitempty"`
	Score       int                     `json:"score"`
	MatchPath   relatedWikiMatchPath    `json:"match_path"`
}

type relatedWikiMatchPath struct {
	Direct []string `json:"direct,omitempty"`
}

// scoreWikiEntry tallies direct entity overlap between a wiki entry's
// frontmatter tags and the query sets. Score = 3 * |direct|. No lifting
// — wiki entries don't have cross-entry references, so there's no
// gateway shape to surface.
func scoreWikiEntry(fm wiki.Frontmatter, qSvc, qErr, qSym map[string]bool) (score int, direct []string) {
	seen := map[string]bool{}
	for _, s := range fm.Services {
		if qSvc[s] && !seen[s] {
			seen[s] = true
			direct = append(direct, s)
		}
	}
	for _, e := range fm.Errors {
		if qErr[e] && !seen[e] {
			seen[e] = true
			direct = append(direct, e)
		}
	}
	for _, sym := range fm.Symptoms {
		if qSym[sym] && !seen[sym] {
			seen[sym] = true
			direct = append(direct, sym)
		}
	}
	score = 3 * len(direct)
	return score, direct
}

// handleRelatedWikiEntries returns up to 5 wiki entries correlated with
// the queried playbook's entity tags. Same query-param override pattern
// as /related: the operator's in-progress chip edits are forwarded via
// ?services=&errors=&symptoms= so the panel previews correlations live
// before save.
//
// GET /api/playbooks/{id}/related-wiki[?services=...&errors=...&symptoms=...]
func (a *apiHandlers) handleRelatedWikiEntries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	meta := a.metaCache.get()
	if meta == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"related": []relatedWikiMatch{}})
		return
	}

	mp, ok := meta.Playbooks[id]
	if !ok {
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

	// Override-then-fall-back pattern, same as /related.
	query := queryTagsFromRequest(r)
	if !query.hasAny() {
		query = extractEntityTags(mp.YAML)
	}

	emptyOut := func() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"related": []relatedWikiMatch{}})
	}

	if len(query.Services) == 0 && len(query.Errors) == 0 && len(query.Symptoms) == 0 {
		emptyOut()
		return
	}

	vaultPath := a.opts.WikiPath
	if vaultPath == "" {
		// No wiki configured — degrade gracefully rather than 500.
		emptyOut()
		return
	}

	paths, err := wiki.ListEntries(vaultPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	qSvc := makeSet(query.Services)
	qErr := makeSet(query.Errors)
	qSym := makeSet(query.Symptoms)

	matches := []relatedWikiMatch{}
	for _, p := range paths {
		raw, err := wiki.ReadNote(vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, _, err := wiki.SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm wiki.Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			continue
		}
		score, direct := scoreWikiEntry(fm, qSvc, qErr, qSym)
		if score == 0 {
			continue
		}
		matches = append(matches, relatedWikiMatch{
			ID:       fm.ID,
			Title:    fm.Title,
			Path:     p,
			Status:   fm.Status,
			Severity: fm.Severity,
			Score:    score,
			MatchPath: relatedWikiMatchPath{
				Direct: direct,
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
