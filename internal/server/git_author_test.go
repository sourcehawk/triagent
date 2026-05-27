package server

import (
	"os"
	"strings"
	"testing"
)

func TestResolveGitAuthor_FromGitConfig(t *testing.T) {
	// Fake an isolated HOME so this test doesn't pick up the developer's
	// ~/.gitconfig. Then write a config file under it.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	if err := os.WriteFile(home+"/.gitconfig", []byte("[user]\n  name = Test User\n  email = test@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := resolveGitAuthor()
	if a.Name != "Test User" || a.Email != "test@example.com" {
		t.Fatalf("got %+v, want Test User / test@example.com", a)
	}
}

func TestResolveGitAuthor_FallbackToOSUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	a := resolveGitAuthor()
	if a.Name == "" || a.Email == "" {
		t.Fatalf("expected non-empty fallback, got %+v", a)
	}
	if !strings.Contains(a.Email, "@unknown") {
		t.Fatalf("expected fallback email to end in @unknown, got %q", a.Email)
	}
}
