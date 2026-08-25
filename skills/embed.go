// Package skills embeds the skills shared by every Claude session the
// launcher spawns (investigation, editor, operator, and the sub-agents
// that draft PRs and wiki entries).
//
// Two delivery paths:
//
//   - Extract writes the skills into <root>/.claude/skills/<slug>/ so a
//     claude CLI whose cwd is <root> discovers them. Only the operator
//     agent has a launcher-owned cwd today.
//   - WritingSimply returns the writing-simply body for prompt builders
//     to append verbatim, which is how the investigation and editor
//     sessions (cwd = the user's launch directory) and the sub-agents get
//     it. Appending guarantees the rules are loaded; discovery alone
//     leaves it to the model to decide to read them.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed */SKILL.md */references/*.md
var files embed.FS

const writingSimplySlug = "writing-simply"

// Extract writes every embedded skill into <root>/.claude/skills/. It
// overwrites on every call: the embedded set is the source of truth.
func Extract(root string) error {
	skillsRoot := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	return fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := files.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		dest := filepath.Join(skillsRoot, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		return os.WriteFile(dest, data, 0o600)
	})
}

// WritingSimply returns the writing-simply SKILL.md body with its YAML
// frontmatter removed, ready to append to a prompt.
func WritingSimply() string {
	data, err := files.ReadFile(writingSimplySlug + "/SKILL.md")
	if err != nil {
		panic(fmt.Sprintf("skills: cannot read %s: %v", writingSimplySlug, err))
	}
	return stripFrontmatter(string(data))
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return s
	}
	return strings.TrimLeft(rest[end+len("\n---\n"):], "\n")
}
