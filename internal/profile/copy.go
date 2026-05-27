package profile

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// CopyEmbeddedProfile materialises the named embedded profile onto disk
// at dst. dst must not yet exist — the helper refuses to clobber, since
// callers are usually operator-driven (`triagent create-profile`) and a
// surprise overwrite would lose in-flight edits.
//
// Every file under `profiles/<name>/` in the embedded FS is written
// verbatim; directory structure is preserved. Use this when an operator
// wants a starter copy of a built-in profile that they can edit in
// place (custom prompts, custom kinds.json) without forking the source
// tree.
//
// The copy is a byte-for-byte transcription: comments, blank lines, and
// field ordering in profile.yaml are preserved. Callers that need to
// rewrite specific lines (e.g. setting `name:` to the new identifier)
// do so as a line-rewrite pass after the copy.
func CopyEmbeddedProfile(name, dst string) error {
	if name == "" {
		return fmt.Errorf("profile name is empty")
	}
	src := path.Join("profiles", name)
	if _, err := fs.Stat(embedded, src); err != nil {
		return fmt.Errorf("embedded profile %q not found", name)
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination %s already exists", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	return fs.WalkDir(embedded, src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := fs.ReadFile(embedded, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}
