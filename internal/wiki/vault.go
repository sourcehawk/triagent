// Package wiki provides read access to the investigations wiki vault.
// This is a thin re-export of the vault primitives from mcp/internal/wiki
// adapted for use in the investigate launcher's HTTP handlers. The two
// modules are separate so we duplicate the small, stable subset we need
// here rather than creating a cross-module dependency.
package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the YAML frontmatter shape of a wiki entry.
type Frontmatter struct {
	SchemaVersion int      `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`
	ID            string   `yaml:"id" json:"id"`
	Date          string   `yaml:"date" json:"date"`
	Title         string   `yaml:"title" json:"title"`
	Status        string   `yaml:"status" json:"status"`
	Severity      string   `yaml:"severity,omitempty" json:"severity,omitempty"`
	Services      []string `yaml:"services" json:"services"`
	Errors        []string `yaml:"errors" json:"errors"`
	Symptoms      []string `yaml:"symptoms" json:"symptoms"`
	Links         Links    `yaml:"links,omitempty" json:"links,omitempty"`
}

// Links holds the optional reference URLs for a wiki entry. Every field is
// optional — empty Links must validate.
type Links struct {
	Investigation string `yaml:"investigation,omitempty" json:"investigation,omitempty"`
	IncidentIO    string `yaml:"incident_io,omitempty" json:"incident_io,omitempty"`
	SlackChannel  string `yaml:"slack_channel,omitempty" json:"slack_channel,omitempty"`
	SlackMessage  string `yaml:"slack_message,omitempty" json:"slack_message,omitempty"`
}

// EntityFrontmatter is the YAML frontmatter shape of an entity stub.
type EntityFrontmatter struct {
	SchemaVersion int    `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`
	Type          string `yaml:"type" json:"type"`
	Name          string `yaml:"name" json:"name"`
	Created       string `yaml:"created" json:"created"`
}

// EntityRef is a lightweight pointer to an entity stub.
type EntityRef struct {
	Name string `json:"name"`
	Type string `json:"type"` // service | error | symptom | component
	Path string `json:"path"` // relative to vault root
}

// EntryNote is a parsed wiki entry file.
type EntryNote struct {
	Path        string      `json:"path"`
	Frontmatter Frontmatter `json:"frontmatter"`
	Body        string      `json:"body"`
}

// entityTypes maps directory name → singular type.
var entityTypes = []struct {
	Dir  string
	Type string
}{
	{"services", "service"},
	{"errors", "error"},
	{"symptoms", "symptom"},
	{"components", "component"},
}

// ListEntries returns every wiki entry note path in the vault.
func ListEntries(vaultPath string) ([]string, error) {
	dir := filepath.Join(vaultPath, "entries")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read entries dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, filepath.Join("entries", e.Name()))
	}
	return out, nil
}

// ReadEntry loads and parses one wiki entry note by slug.
func ReadEntry(vaultPath, slug string) (*EntryNote, error) {
	rel := filepath.Join("entries", slug+".md")
	full := filepath.Join(vaultPath, rel)
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	fm, body, err := SplitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var parsed Frontmatter
	if err := yaml.Unmarshal(fm, &parsed); err != nil {
		return nil, fmt.Errorf("parse frontmatter %s: %w", rel, err)
	}
	return &EntryNote{
		Path:        rel,
		Frontmatter: parsed,
		Body:        string(body),
	}, nil
}

// ReadNote loads any markdown file under the vault by its vault-relative path.
func ReadNote(vaultPath, relPath string) ([]byte, error) {
	clean := filepath.Clean(relPath)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("relPath %q escapes vault root", relPath)
	}
	return os.ReadFile(filepath.Join(vaultPath, clean))
}

// SplitFrontmatter splits a markdown file with YAML frontmatter.
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

// ListEntities returns every entity stub in the vault, optionally filtered by type.
func ListEntities(vaultPath, typeFilter string) ([]EntityRef, error) {
	var out []EntityRef
	for _, et := range entityTypes {
		if typeFilter != "" && typeFilter != et.Type {
			continue
		}
		dir := filepath.Join(vaultPath, "entities", et.Dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read entity dir %q: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			out = append(out, EntityRef{
				Name: name,
				Type: et.Type,
				Path: filepath.Join("entities", et.Dir, e.Name()),
			})
		}
	}
	return out, nil
}

// TypeToDir maps entity type singular name to its directory name.
func TypeToDir(t string) string {
	switch t {
	case "service":
		return "services"
	case "error":
		return "errors"
	case "symptom":
		return "symptoms"
	case "component":
		return "components"
	}
	return ""
}

// ParseEntityFrontmatter parses an entity stub's YAML frontmatter.
func ParseEntityFrontmatter(raw []byte) (*EntityFrontmatter, error) {
	fm, _, err := SplitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var ef EntityFrontmatter
	if err := yaml.Unmarshal(fm, &ef); err != nil {
		return nil, err
	}
	return &ef, nil
}
