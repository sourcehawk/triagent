// Package operatorskills embeds the operator-agent skill markdown files
// (siblings of this Go file) and extracts them into a launch-cwd-local
// .claude/skills/<slug>/SKILL.md layout that the Claude Code CLI
// auto-discovers.
//
// The umbrella skill (operator-role) is also exposed verbatim via
// UmbrellaContent() so the AutoOperator can append it to the operator's
// --append-system-prompt — guaranteeing it's always loaded even if
// Claude's skill-discovery declines to invoke it.
package operatorskills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed */SKILL.md
var files embed.FS

const umbrellaSlug = "operator-role"

// Extract writes every embedded skill into <root>/.claude/skills/<slug>/SKILL.md.
// Re-extracts every call — the embedded set is source of truth and any
// stale on-disk content is overwritten.
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

// UmbrellaContent returns the operator-role SKILL.md body bytes.
func UmbrellaContent() []byte {
	data, err := files.ReadFile(umbrellaSlug + "/SKILL.md")
	if err != nil {
		panic(fmt.Sprintf("operator-skills: cannot read umbrella: %v", err))
	}
	return data
}
