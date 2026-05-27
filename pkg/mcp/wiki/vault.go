package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// EntityRef is a lightweight pointer to an entity stub.
type EntityRef struct {
	Name string `json:"name"`
	Type string `json:"type"` // service | error | symptom | component
	Path string `json:"path"` // relative to vault root
}

// EntryNote is a parsed wiki entry.
type EntryNote struct {
	Path        string      `json:"path"`
	Frontmatter Frontmatter `json:"frontmatter"`
	Body        string      `json:"body"`
}

// entityTypes maps directory name → singular type. Order matters for
// link resolution (Task 8 falls back through this list).
var entityTypes = []struct {
	Dir  string
	Type string
}{
	{"services", "service"},
	{"errors", "error"},
	{"symptoms", "symptom"},
	{"components", "component"},
}

// ListEntities returns every entity stub in the vault, optionally filtered
// by type ("service", "error", "symptom", "component", or "" for all).
// A missing directory yields an empty list (the vault may not have any
// entities of that type yet); other read errors propagate.
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

// ListEntries returns every wiki entry path in the vault.
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

// ReadEntry loads and parses one wiki entry by slug.
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

// ReadNote loads any markdown file under the vault by its vault-relative
// path. Returns the raw bytes; callers parse as appropriate.
func ReadNote(vaultPath, relPath string) ([]byte, error) {
	clean := filepath.Clean(relPath)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("relPath %q escapes vault root", relPath)
	}
	return os.ReadFile(filepath.Join(vaultPath, clean))
}

// SplitFrontmatter splits a markdown file with YAML frontmatter into
// (frontmatter bytes, body bytes). Returns an error if the file does
// not start with a `---` line.
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
