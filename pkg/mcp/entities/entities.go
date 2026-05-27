// Package entities provides canonical entity-name validation and
// near-match resolution shared across wiki and playbook MCP tools.
package entities

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Field constants for keyword resolution. The string values surface in
// tool error messages and JSON output; keep them stable across consumers.
const (
	FieldServices = "services"
	FieldErrors   = "errors"
	FieldSymptoms = "symptoms"
)

// NamePattern is the canonical entity-name shape: lowercase letters, digits, and hyphens.
var NamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Resolution explains how one input keyword mapped against a
// known-entity set.
type Resolution struct {
	Field string   `json:"field"`
	Input string   `json:"input"`
	Exact bool     `json:"exact"`
	Near  []string `json:"near,omitempty"`
}

// ResolveKeywords runs every input keyword (across services / errors /
// symptoms) against the known set per type and returns one Resolution
// per (field, input). knownByType keys are "service" / "error" /
// "symptom" (singular).
func ResolveKeywords(services, errors, symptoms []string, knownByType map[string][]string) []Resolution {
	out := make([]Resolution, 0, len(services)+len(errors)+len(symptoms))
	for _, s := range services {
		out = append(out, resolveOne(FieldServices, s, knownByType["service"]))
	}
	for _, e := range errors {
		out = append(out, resolveOne(FieldErrors, e, knownByType["error"]))
	}
	for _, sym := range symptoms {
		out = append(out, resolveOne(FieldSymptoms, sym, knownByType["symptom"]))
	}
	return out
}

// resolveOne checks a single keyword against a known-name list.
// Exact matches short-circuit; otherwise we surface up to 5 near hits.
func resolveOne(field, input string, known []string) Resolution {
	for _, n := range known {
		if n == input {
			return Resolution{Field: field, Input: input, Exact: true}
		}
	}
	return Resolution{
		Field: field,
		Input: input,
		Exact: false,
		Near:  NearMatches(input, known, 5),
	}
}

// NearMatches returns up to max entries from candidates ranked
// closest-first to input. Ranking blends substring containment with
// Levenshtein distance (≤ max(3, len/3) edits). Substring beats
// pure edit-distance; ties break alphabetically.
func NearMatches(input string, candidates []string, max int) []string {
	if input == "" || len(candidates) == 0 {
		return nil
	}
	type scored struct {
		name   string
		subset int
		dist   int
	}
	threshold := 3
	if t := len(input) / 3; t > threshold {
		threshold = t
	}
	var hits []scored
	for _, c := range candidates {
		if c == input {
			continue
		}
		s := scored{name: c}
		if strings.Contains(c, input) {
			s.subset = 2
		} else if strings.Contains(input, c) {
			s.subset = 1
		}
		s.dist = levenshtein(input, c)
		if s.subset == 0 && s.dist > threshold {
			continue
		}
		hits = append(hits, s)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].subset != hits[j].subset {
			return hits[i].subset > hits[j].subset
		}
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// ValidateNames rejects malformed entity names with an actionable
// error. Returns nil when every value matches NamePattern.
// The error message names the field, the bad value, the shape rule,
// and (when canonicalisable) a "did you mean" hint.
//
// listSourceTool is the tool consumers should call to enumerate
// canonical names (e.g. "wiki_list_entities"). Empty omits the
// "use … to enumerate" suffix.
func ValidateNames(field, listSourceTool string, names []string) error {
	for _, n := range names {
		if NamePattern.MatchString(n) {
			continue
		}
		hint := CanonicaliseHint(n)
		var hintClause string
		if hint != "" && hint != n {
			hintClause = fmt.Sprintf(" (did you mean %q?)", hint)
		}
		suffix := ""
		if listSourceTool != "" {
			suffix = fmt.Sprintf(" Use %s to enumerate canonical names; pass them verbatim", listSourceTool)
		}
		return fmt.Errorf("%s contains %q which is not a valid entity name%s — names must match ^[a-z0-9][a-z0-9-]*$ (lowercase, hyphens only, no spaces / underscores / capitals).%s", field, n, hintClause, suffix)
	}
	return nil
}

// CanonicaliseHint converts an obviously-non-canonical input into the
// shape ValidateNames would accept, for inclusion in error messages.
// Returns "" when the result still wouldn't pass (e.g. leading
// hyphen, punctuation).
func CanonicaliseHint(in string) string {
	out := strings.ToLower(in)
	out = strings.ReplaceAll(out, " ", "-")
	out = strings.ReplaceAll(out, "_", "-")
	out = strings.Trim(out, "-")
	if !NamePattern.MatchString(out) {
		return ""
	}
	return out
}
