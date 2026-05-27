package sessions

import (
	"fmt"
	"regexp"
	"strings"
)

// CurrentSchemaVersion is the version stamped on every session.md
// frontmatter the launcher writes. Bumped together with any breaking
// frontmatter shape change so old launchers refuse to validate
// content from a newer schema (or vice-versa).
const CurrentSchemaVersion = 1

// SlugPattern is the canonical regex for the per-session slug used as
// both the directory name (`sessions/<YYYY-MM>/<slug>/`) and the
// frontmatter `id` field. Format:
//
//	YYYY-MM-DD-<namespace-slug>-<id6>
//
// e.g. "2026-05-08-prod-eu-1-a3f0b2".
var SlugPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-[a-z0-9-]+-[a-f0-9]{6,}$`)

// RequiredHeaders are the body section headers every session.md must
// include, exactly as written, case-sensitive, no trailing punctuation.
var RequiredHeaders = []string{
	"## Summary",
	"## Timeline",
	"## What was tried",
	"## Findings",
	"## Outcome",
}

// Author is the captured git author identity stamped on each session
// at the time the launcher kicked off the investigation.
type Author struct {
	Name  string `yaml:"name"  json:"name"`
	Email string `yaml:"email" json:"email"`
}

// Sources records optional cross-references the operator (or auto-
// imported flow) attached to the investigation. `bundle` is always set
// to the relative filename of the c1inv.json that ships alongside the
// markdown.
type Sources struct {
	IncidentIO string `yaml:"incident_io,omitempty" json:"incident_io,omitempty"`
	Slack      string `yaml:"slack,omitempty"       json:"slack,omitempty"`
	Bundle     string `yaml:"bundle"                json:"bundle"`
}

// Frontmatter is the YAML frontmatter shape for a session.md file. The
// launcher overlays identity-bearing fields after the AI sub-agent
// drafts the body so the agent can never set `id`, `date`, `author`,
// `namespace`, or `sources` to anything other than what
// the launcher computed from the captured metadata.
// ContextName is deprecated in favor of ClustersTouched.
type Frontmatter struct {
	SchemaVersion  int      `yaml:"schema_version"    json:"schema_version"`
	ID             string   `yaml:"id"                json:"id"`
	Date           string   `yaml:"date"              json:"date"`
	Title          string   `yaml:"title"             json:"title"`
	Author         Author   `yaml:"author"            json:"author"`
	Namespace      string   `yaml:"namespace"         json:"namespace"`
	ContextName    string   `yaml:"context_name,omitempty"  json:"context_name,omitempty"`
	ClustersTouched []string `yaml:"clusters_touched,omitempty" json:"clusters_touched,omitempty"`
	Sources        Sources  `yaml:"sources"           json:"sources"`
}

// ValidateFrontmatter returns a list of validation errors (empty when
// ok). schema_version 0 (absent) is treated as the current version for
// back-compat with sessions written before the field was stamped;
// non-zero must match exactly so a newer-schema file fails loudly on an
// older validator.
func ValidateFrontmatter(fm Frontmatter) []string {
	var errs []string
	if fm.SchemaVersion != 0 && fm.SchemaVersion != CurrentSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version %d unsupported; this build understands %d", fm.SchemaVersion, CurrentSchemaVersion))
	}
	if !SlugPattern.MatchString(fm.ID) {
		errs = append(errs, fmt.Sprintf("id %q does not match the session slug pattern %s", fm.ID, SlugPattern.String()))
	}
	if strings.TrimSpace(fm.Date) == "" {
		errs = append(errs, "date is required (YYYY-MM-DD)")
	}
	if strings.TrimSpace(fm.Title) == "" {
		errs = append(errs, "title is required")
	}
	if strings.TrimSpace(fm.Author.Name) == "" {
		errs = append(errs, "author.name is required")
	}
	if strings.TrimSpace(fm.Author.Email) == "" {
		errs = append(errs, "author.email is required")
	}
	if strings.TrimSpace(fm.Namespace) == "" {
		errs = append(errs, "namespace is required")
	}
	if strings.TrimSpace(fm.Sources.Bundle) == "" {
		errs = append(errs, "sources.bundle is required (must be the relative filename of the c1inv.json shipped alongside)")
	}
	return errs
}

// ValidateBodyHeaders checks the markdown body has each of
// RequiredHeaders. Returns an empty slice when all are present.
func ValidateBodyHeaders(body string) []string {
	var errs []string
	for _, h := range RequiredHeaders {
		if !strings.Contains(body, h) {
			errs = append(errs, fmt.Sprintf("missing required header %q", h))
		}
	}
	return errs
}

// SplitFrontmatter splits a markdown file with YAML frontmatter into
// (frontmatter bytes, body bytes). Mirrors mcp/internal/wiki.SplitFrontmatter
// — the two formats are deliberately the same so tooling can swap.
func SplitFrontmatter(raw []byte) (fm, body []byte, err error) {
	const sep = "---"
	s := string(raw)
	if !strings.HasPrefix(s, sep+"\n") && !strings.HasPrefix(s, sep+"\r\n") {
		return nil, nil, fmt.Errorf("no frontmatter delimiter at top of file")
	}
	rest := s[len(sep)+1:]
	end := strings.Index(rest, "\n"+sep+"\n")
	if end < 0 {
		end = strings.Index(rest, "\n"+sep+"\r\n")
	}
	if end < 0 {
		return nil, nil, fmt.Errorf("no closing frontmatter delimiter")
	}
	fm = []byte(rest[:end])
	bodyStart := end + len("\n"+sep+"\n")
	if bodyStart > len(rest) {
		bodyStart = len(rest)
	}
	body = []byte(rest[bodyStart:])
	return fm, body, nil
}
