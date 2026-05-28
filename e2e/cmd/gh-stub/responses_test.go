//go:build e2e

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// loadResponses reads responses.json from GH_STUB_SCRIPT; an unset script
// dir yields an empty table (so the unmatched-argv path fires for an
// unscripted gh call, e.g. the launcher's startup capability probe).
func TestLoadResponses(t *testing.T) {
	t.Run("unset script dir -> empty table", func(t *testing.T) {
		t.Setenv("GH_STUB_SCRIPT", "")
		got, err := loadResponses()
		if err != nil {
			t.Fatalf("loadResponses: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d entries, want 0", len(got))
		}
	})

	t.Run("reads + parses responses.json", func(t *testing.T) {
		dir := t.TempDir()
		body := `[{"argv":["issue","list"],"stdout":"[]"}]`
		if err := os.WriteFile(filepath.Join(dir, "responses.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GH_STUB_SCRIPT", dir)
		got, err := loadResponses()
		if err != nil {
			t.Fatalf("loadResponses: %v", err)
		}
		if len(got) != 1 || got[0].Stdout != "[]" {
			t.Fatalf("unexpected entries: %+v", got)
		}
	})

	t.Run("malformed responses.json errors", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "responses.json"), []byte("{bad"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GH_STUB_SCRIPT", dir)
		if _, err := loadResponses(); err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})
}
