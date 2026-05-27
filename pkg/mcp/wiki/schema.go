// Package wiki implements the triagent-mcp `wiki` MCP server: tools the
// investigation agent uses to draft and read entries in the
// investigations wiki vault. Approved drafts land in a git-backed
// vault (default ~/.triagent/investigations-wiki); in-flight drafts live
// in ~/.triagent/wiki-proposals (never enters the vault).
package wiki

import (
	"fmt"
	"regexp"
	"strings"
)

// CurrentSchemaVersion is the contract version this code understands.
// Bumped only when the schema breaks. New authoring should always stamp
// this; absence in a parsed file is treated as 1 for back-compat.
const CurrentSchemaVersion = 1

// Frontmatter is the YAML frontmatter shape of a wiki entry.
// Matches the schema in docs/superpowers/specs/2026-05-06-investigations-wiki-design.md.
type Frontmatter struct {
	SchemaVersion int      `yaml:"schema_version,omitempty"`
	ID            string   `yaml:"id"`
	Date          string   `yaml:"date"`
	Title         string   `yaml:"title"`
	Status        string   `yaml:"status"`
	Severity      string   `yaml:"severity,omitempty"`
	Services      []string `yaml:"services"`
	Errors        []string `yaml:"errors"`
	Symptoms      []string `yaml:"symptoms"`
	Links         Links    `yaml:"links,omitempty"`
}

// Links holds the optional source citations for a wiki entry. Every
// field is optional — an entry with no links is allowed.
type Links struct {
	Investigation string `yaml:"investigation,omitempty"`
	IncidentIO    string `yaml:"incident_io,omitempty"`
	SlackChannel  string `yaml:"slack_channel,omitempty"`
	SlackMessage  string `yaml:"slack_message,omitempty"`
}

// EntityFrontmatter is the YAML frontmatter shape of an entity stub.
type EntityFrontmatter struct {
	SchemaVersion int    `yaml:"schema_version,omitempty"`
	Type          string `yaml:"type"`
	Name          string `yaml:"name"`
	Created       string `yaml:"created"`
}

// slugPattern matches a free-form lowercase-with-hyphens slug, like
// "inc-12345-broker-ooms", "inv-broker-restart", "alert-cpu-spike".
// The wiki sub-agent is prompted to prefix slugs with inc- / inv- /
// alert- depending on the strongest source, but the validator accepts
// any valid slug — operators may pick whichever prefix matches their
// mental model.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateSlug rejects empty / malformed slugs. The format guidance
// (`inc-` / `inv-` / `alert-` prefixes) lives in the agent prompt, not
// the validator.
func ValidateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("slug is required")
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug %q must be lowercase-with-hyphens (^[a-z0-9][a-z0-9-]*$)", slug)
	}
	if strings.HasSuffix(slug, "-") {
		return fmt.Errorf("slug %q has empty trailing segment", slug)
	}
	return nil
}

var validStatus = map[string]bool{"resolved": true, "open": true, "wontfix": true}
var validSeverity = map[string]bool{"": true, "sev1": true, "sev2": true, "sev3": true}

// ValidateFrontmatter returns a list of validation errors (empty when ok).
// Returning a slice rather than a single error lets the sub-agent fix
// multiple issues in one pass.
func ValidateFrontmatter(fm Frontmatter) []string {
	var errs []string
	// schema_version: 0 (absent in YAML) is treated as CurrentSchemaVersion
	// for back-compat with files that predate the field. Any non-zero value
	// must match CurrentSchemaVersion or the loader can't trust the rest.
	if fm.SchemaVersion != 0 && fm.SchemaVersion != CurrentSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version %d unsupported; this build understands %d", fm.SchemaVersion, CurrentSchemaVersion))
	}
	if err := ValidateSlug(fm.ID); err != nil {
		errs = append(errs, err.Error())
	}
	if strings.TrimSpace(fm.Date) == "" {
		errs = append(errs, "date is required (YYYY-MM-DD)")
	}
	if strings.TrimSpace(fm.Title) == "" {
		errs = append(errs, "title is required")
	}
	if !validStatus[fm.Status] {
		errs = append(errs, fmt.Sprintf("status %q invalid; want one of resolved|open|wontfix", fm.Status))
	}
	if !validSeverity[fm.Severity] {
		errs = append(errs, fmt.Sprintf("severity %q invalid; want one of sev1|sev2|sev3 (or omit)", fm.Severity))
	}
	if fm.Services == nil {
		errs = append(errs, "services array is required (may be empty)")
	}
	if fm.Errors == nil {
		errs = append(errs, "errors array is required (may be empty)")
	}
	if fm.Symptoms == nil {
		errs = append(errs, "symptoms array is required (may be empty)")
	}
	return errs
}

// validEntityType / entityNamePattern / entityDatePattern are the shape
// constraints on entity stub frontmatter. Shared with the validate-wiki
// subcommand and the validate_wiki MCP tool.
var (
	validEntityType    = map[string]bool{"service": true, "error": true, "symptom": true, "component": true}
	entityNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	entityDatePattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ValidateEntityFrontmatter returns a list of validation errors (empty when
// ok). Same back-compat rule as ValidateFrontmatter: schema_version 0 is
// treated as CurrentSchemaVersion.
func ValidateEntityFrontmatter(ef EntityFrontmatter) []string {
	var errs []string
	if ef.SchemaVersion != 0 && ef.SchemaVersion != CurrentSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version %d unsupported; this build understands %d", ef.SchemaVersion, CurrentSchemaVersion))
	}
	if !validEntityType[ef.Type] {
		errs = append(errs, fmt.Sprintf("type %q invalid; want one of service|error|symptom|component", ef.Type))
	}
	if strings.TrimSpace(ef.Name) == "" {
		errs = append(errs, "name is required")
	} else if !entityNamePattern.MatchString(ef.Name) {
		errs = append(errs, fmt.Sprintf("name %q must be lowercase-with-hyphens (^[a-z0-9][a-z0-9-]*$)", ef.Name))
	}
	if strings.TrimSpace(ef.Created) == "" {
		errs = append(errs, "created is required (YYYY-MM-DD)")
	} else if !entityDatePattern.MatchString(ef.Created) {
		errs = append(errs, fmt.Sprintf("created %q must be YYYY-MM-DD", ef.Created))
	}
	return errs
}

// RequiredHeaders are the markdown body sections every incident note must
// contain. Order is not enforced (sub-agent prose order may vary), but
// presence is.
var RequiredHeaders = []string{"## Summary", "## Root cause", "## Fix"}

// ValidateBodyHeaders checks the markdown body has each of RequiredHeaders.
// Case-sensitive (we standardise on the exact casing in the spec).
func ValidateBodyHeaders(body string) []string {
	var errs []string
	for _, h := range RequiredHeaders {
		if !strings.Contains(body, h) {
			errs = append(errs, fmt.Sprintf("missing required header %q", h))
		}
	}
	return errs
}
