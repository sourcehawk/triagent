package prom

import (
	"sort"
	"strings"
)

const (
	searchHardCap = 50
)

// SearchResult is the JSON-returned shape of prom_list_metrics.
type SearchResult struct {
	Matches  []SearchMatch   `json:"matches"`
	Overflow *OverflowReport `json:"overflow,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type SearchMatch struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type OverflowReport struct {
	Total  int           `json:"total"`
	Facets []OverflowFacet `json:"facets"`
	Hint   string        `json:"hint"`
}

type OverflowFacet struct {
	Prefix string `json:"prefix"`
	Count  int    `json:"count"`
}

// searchMetrics implements the prom_list_metrics rules:
//   - non-empty, non-wildcard query
//   - tokens AND-match against name OR HELP (case-insensitive substring)
//   - rank matches alphabetically by name
//   - cap at min(limit, searchHardCap)
//   - if more than the cap match, return a facet breakdown instead of the
//     first N — never silently truncate a useful match set.
func searchMetrics(cat *catalog, query string, limit int) SearchResult {
	q := strings.TrimSpace(query)
	if q == "" {
		return SearchResult{Error: "query is required (non-empty)"}
	}
	if strings.ContainsAny(q, "*?") {
		return SearchResult{Error: "no wildcard characters in query — use plain space-separated tokens"}
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > searchHardCap {
		limit = searchHardCap
	}
	tokens := tokenize(q)
	if len(tokens) == 0 {
		return SearchResult{Error: "query is required (non-empty)"}
	}

	type scored struct {
		name string
		typ  string
	}
	var matches []scored
	for _, name := range cat.names {
		hay := strings.ToLower(name + " " + cat.metadata[name].Help)
		hit := 0
		for _, t := range tokens {
			if strings.Contains(hay, t) {
				hit++
			}
		}
		if hit == len(tokens) {
			matches = append(matches, scored{name, cat.metadata[name].Type})
		}
	}
	if len(matches) == 0 {
		return SearchResult{Matches: []SearchMatch{}}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].name < matches[j].name
	})
	if len(matches) > limit {
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.name
		}
		return SearchResult{
			Matches:  []SearchMatch{},
			Overflow: buildOverflow(names),
		}
	}
	out := make([]SearchMatch, len(matches))
	for i, m := range matches {
		out[i] = SearchMatch{Name: m.name, Type: m.typ}
	}
	return SearchResult{Matches: out}
}

func tokenize(q string) []string {
	return strings.Fields(strings.ToLower(q))
}

// buildOverflow groups names by their next path segment after the
// longest common prefix shared by the set. Caps at 8 facets.
func buildOverflow(names []string) *OverflowReport {
	lcp := longestCommonPrefix(names)
	bucket := map[string]int{}
	for _, n := range names {
		rest := strings.TrimPrefix(n, lcp)
		seg := nextSegment(rest)
		if seg == "" {
			seg = rest
		}
		bucket[lcp+seg]++
	}
	var facets []OverflowFacet
	for p, c := range bucket {
		facets = append(facets, OverflowFacet{Prefix: p, Count: c})
	}
	sort.Slice(facets, func(i, j int) bool {
		if facets[i].Count != facets[j].Count {
			return facets[i].Count > facets[j].Count
		}
		return facets[i].Prefix < facets[j].Prefix
	})
	if len(facets) > 8 {
		facets = facets[:8]
	}
	return &OverflowReport{
		Total:  len(names),
		Facets: facets,
		Hint:   "Refine: add tokens (e.g. one more keyword) or pick a sub-prefix and search again.",
	}
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	pref := ss[0]
	for _, s := range ss[1:] {
		i := 0
		for i < len(pref) && i < len(s) && pref[i] == s[i] {
			i++
		}
		pref = pref[:i]
		if pref == "" {
			break
		}
	}
	return pref
}

// nextSegment returns the chars up to (and including) the next '_' in s,
// or the whole string when there's no further '_'.
func nextSegment(s string) string {
	if i := strings.IndexByte(s, '_'); i >= 0 {
		return s[:i+1]
	}
	return s
}
