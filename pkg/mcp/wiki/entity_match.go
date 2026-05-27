package wiki

import (
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/entities"
)

// Re-export for callers that still type wiki.EntityResolution. Drop
// these once external callers migrate to the entities package.
type EntityResolution = entities.Resolution

// loadKnownEntitiesByType reads ListEntities once and returns names
// grouped by type. Used by both wiki_search and wiki_correlate to
// power the resolution field. Errors propagate so the caller can
// decide whether to fail the whole tool call or degrade to skipping
// resolution.
func loadKnownEntitiesByType(vaultPath string) (map[string][]string, error) {
	refs, err := ListEntities(vaultPath, "")
	if err != nil {
		return nil, err
	}
	byType := make(map[string][]string)
	for _, r := range refs {
		byType[r.Type] = append(byType[r.Type], r.Name)
	}
	return byType, nil
}

// validateEntityType rejects an unknown `type` filter (used by
// wiki_list_entities). Empty is allowed — it means "no filter".
// Loud rejection beats a silent empty list, which would read as
// "no entities of that type exist" rather than "your filter typoed".
func validateEntityType(t string) error {
	if t == "" {
		return nil
	}
	if validEntityType[t] {
		return nil
	}
	hint := entities.CanonicaliseHint(t)
	var hintClause string
	if hint != "" && validEntityType[hint] {
		hintClause = fmt.Sprintf(" (did you mean %q?)", hint)
	}
	return fmt.Errorf("type %q is not one of service|error|symptom|component%s", t, hintClause)
}

// validateStatusFilter rejects unknown values in the wiki_search
// status filter. The filter accepts multiple values (OR within
// field), so we check each one.
func validateStatusFilter(values []string) error {
	for _, v := range values {
		if v == "resolved" || v == "open" || v == "wontfix" {
			continue
		}
		return fmt.Errorf("filters.status contains %q — must be one of resolved|open|wontfix", v)
	}
	return nil
}

// validateSeverityFilter rejects unknown values in the wiki_search
// severity filter. Same shape as validateStatusFilter.
func validateSeverityFilter(values []string) error {
	for _, v := range values {
		if v == "sev1" || v == "sev2" || v == "sev3" {
			continue
		}
		return fmt.Errorf("filters.severity contains %q — must be one of sev1|sev2|sev3", v)
	}
	return nil
}

// validateNotePath rejects malformed wiki_get paths before the read
// hits the filesystem. The vault layout is
// `entries/<slug>.md or entities/<type>/<name>.md`; anything else
// (a Windows-style separator, a backslash, an absolute path, a
// non-canonical entity-name segment) yields a structured error
// instead of a confusing "no such file" from os.Open.
//
// Doesn't enforce that the file exists — that's a runtime concern
// surfaced by the read itself; existence is a different failure
// shape from a malformed input.
func validateNotePath(path string) error {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return fmt.Errorf("path is required")
	}
	// Same safety as ReadNote, but as a typed error so callers see
	// the same shape they'd get from entities.ValidateNames.
	if strings.ContainsRune(clean, '\\') {
		return fmt.Errorf("path %q must use forward slashes", path)
	}
	if strings.HasPrefix(clean, "/") {
		return fmt.Errorf("path %q must be vault-relative (no leading slash)", path)
	}
	if strings.Contains(clean, "..") {
		return fmt.Errorf("path %q must not contain %q", path, "..")
	}
	switch {
	case strings.HasPrefix(clean, "entries/"):
		// entries/<slug>.md — the slug pattern is enforced by
		// ValidateSlug, but we'd rather reject a missing
		// .md or a stray subdirectory before the read.
		if !strings.HasSuffix(clean, ".md") {
			return fmt.Errorf("path %q must end in .md", path)
		}
		base := strings.TrimSuffix(strings.TrimPrefix(clean, "entries/"), ".md")
		if strings.Contains(base, "/") {
			return fmt.Errorf("path %q must be of the form entries/<slug>.md (no subdirs)", path)
		}
		if err := ValidateSlug(base); err != nil {
			return fmt.Errorf("path %q: %w", path, err)
		}
		return nil
	case strings.HasPrefix(clean, "entities/"):
		// entities/<type>/<name>.md
		if !strings.HasSuffix(clean, ".md") {
			return fmt.Errorf("path %q must end in .md", path)
		}
		rest := strings.TrimSuffix(strings.TrimPrefix(clean, "entities/"), ".md")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 {
			return fmt.Errorf("path %q must be of the form entities/<type>/<name>.md", path)
		}
		typeDir, name := parts[0], parts[1]
		// Map the directory name to its singular type. The vault
		// uses plural directories (services/, errors/, ...) so we
		// can't reuse validateEntityType wholesale.
		valid := map[string]bool{"services": true, "errors": true, "symptoms": true, "components": true}
		if !valid[typeDir] {
			return fmt.Errorf("path %q: type segment %q must be one of services|errors|symptoms|components", path, typeDir)
		}
		if !entities.NamePattern.MatchString(name) {
			hint := entities.CanonicaliseHint(name)
			var hintClause string
			if hint != "" && hint != name {
				hintClause = fmt.Sprintf(" (did you mean %q?)", hint)
			}
			return fmt.Errorf("path %q: entity name %q must match ^[a-z0-9][a-z0-9-]*$ (lowercase, hyphens only)%s", path, name, hintClause)
		}
		return nil
	}
	return fmt.Errorf("path %q must start with entries/ or entities/", path)
}
