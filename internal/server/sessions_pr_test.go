package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComputeSessionSlug(t *testing.T) {
	cases := []struct {
		name      string
		createdAt time.Time
		namespace string
		id        string
		want      string
	}{
		{"basic", time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC), "prod-eu-1", "a3f0b2c1d4e5", "2026-05-08-prod-eu-1-a3f0b2"},
		{"namespace cleanup", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "Prod EU/1", "abcdef0123", "2026-01-02-prod-eu-1-abcdef"},
		{"namespace truncation", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), strings.Repeat("a", 50), "abcdef0123", "2026-01-02-" + strings.Repeat("a", 32) + "-abcdef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeSessionSlug(c.createdAt, c.namespace, c.id)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if !sessionSlugPattern.MatchString(got) {
				t.Fatalf("slug %q does not match validation pattern", got)
			}
		})
	}
}

func TestSessionFrontmatterValidation(t *testing.T) {
	good := sessionFrontmatter{
		SchemaVersion: 1,
		ID:            "2026-05-08-prod-eu-1-a3f0b2",
		Date:          "2026-05-08",
		Title:         "OOMKilled on zeebe-broker-2",
		Author:        sessionAuthor{Name: "T", Email: "t@example.com"},
		Namespace:     "prod-eu-1",
	}
	if errs := validateSessionFrontmatter(good); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}

	bad := good
	bad.Author.Email = ""
	if errs := validateSessionFrontmatter(bad); len(errs) == 0 {
		t.Fatalf("expected validation error for missing author email")
	}

	bad2 := good
	bad2.SchemaVersion = 2
	if errs := validateSessionFrontmatter(bad2); len(errs) == 0 {
		t.Fatalf("expected validation error for unsupported schema version")
	}

	bad3 := good
	bad3.ID = "not-a-slug"
	if errs := validateSessionFrontmatter(bad3); len(errs) == 0 {
		t.Fatalf("expected validation error for bad slug")
	}

	// Scope-unknown sessions push frontmatter with an empty namespace; it
	// must now pass validation.
	emptyNS := good
	emptyNS.Namespace = ""
	if errs := validateSessionFrontmatter(emptyNS); len(errs) != 0 {
		t.Fatalf("empty namespace must pass validation now that scope-unknown sessions exist; got %v", errs)
	}
}

func TestSessionBodyHeadersValidation(t *testing.T) {
	good := "## Summary\nx\n## Timeline\nx\n## What was tried\nx\n## Findings\nx\n## Outcome\nx\n"
	if errs := validateSessionBodyHeaders(good); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	bad := "## Summary\nx\n## Outcome\nx\n"
	if errs := validateSessionBodyHeaders(bad); len(errs) == 0 {
		t.Fatalf("expected errors for missing headers")
	}
}

// initBareAndClone creates a bare upstream and a clone with one commit.
// Returns (bare, clone) temp dirs.
func initBareAndClone(t *testing.T) (bare, clone string) {
	t.Helper()
	bare = t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "--initial-branch=main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, out)
	}
	clone = t.TempDir()
	if out, err := exec.Command("git", "clone", bare, clone).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		// Configure identity on the clone so production code under test
		// (pushSessionPR's `git commit`) can use it without depending on
		// the host's global git config — CI runners have no identity set.
		{"-C", clone, "config", "user.email", "t@x"},
		{"-C", clone, "config", "user.name", "t"},
		{"-C", clone, "add", "."},
		{"-C", clone, "commit", "-m", "init", "--quiet"},
		{"-C", clone, "push", "origin", "main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return bare, clone
}

// writeTestSessionDir writes a minimal metadata.json and events.jsonl to dir
// so pushSessionPR can read the session data without hitting os.ReadFile
// errors. Used by handler tests that want the goroutine to reach the drafter.
func writeTestSessionDir(t *testing.T, dir string) {
	t.Helper()
	meta := `{"id":"abc123def456","namespace":"prod-eu-1","createdAt":"2026-05-08T00:00:00Z","author":{"name":"T","email":"t@example.com"}}`
	if err := os.WriteFile(filepath.Join(dir, fileMetadata), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// blockUntilCancelledDrafter returns a sessionDrafter that blocks on
// the supplied context until it is cancelled — used to keep the push
// goroutine "in progress" long enough for assertions.
func blockUntilCancelledDrafter(t *testing.T) sessionDrafter {
	t.Helper()
	return func(ctx context.Context, _, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
}

// goodDrafter is a stub sessionDrafter that writes a valid session.md.
func goodDrafter(_ context.Context, _, _, outPath string) error {
	body := `---
schema_version: 1
id: 2026-05-08-prod-eu-1-abc123
date: 2026-05-08
title: "Demo session"
author:
  name: T
  email: t@example.com
namespace: prod-eu-1
sources:
  bundle: session.triagent.json
---

## Summary
hi
## Timeline
hi
## What was tried
hi
## Findings
hi
## Outcome
hi
`
	return os.WriteFile(outPath, []byte(body), 0o644)
}

func TestPushSessionPR_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, clone := initBareAndClone(t)

	sessionDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(sessionDir, fileMetadata),
		[]byte(`{"id":"abc123def456","namespace":"prod-eu-1","createdAt":"2026-05-08T00:00:00Z","author":{"name":"T","email":"t@example.com"}}`),
		0o644)
	// Seed a successful switch_context event so ContextsTouched returns "example-prod-cluster-1".
	switchEvent := `{"phase":"end","traceId":"t","toolId":"a","toolName":"mcp__triagent-k8s__switch_context","result":"{\"activeContext\":\"example-prod-cluster-1\"}"}` + "\n"
	_ = os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(switchEvent), 0o644)

	proposalsDir := t.TempDir()

	// Fake gh binary that prints a PR URL and exits 0.
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "gh"), []byte("#!/usr/bin/env bash\necho https://example.test/pr/1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	caps := Capabilities{
		GH:       GHStatus{Installed: true, Authenticated: true},
		Sessions: SessionsStatus{Configured: true, Valid: true},
	}

	res, errs, err := pushSessionPR(context.Background(), caps, clone, clone, "owner/repo", proposalsDir, sessionDir, sessionPushRequest{}, goodDrafter)
	if err != nil {
		t.Fatalf("pushSessionPR: %v errs=%v", err, errs)
	}
	if len(errs) != 0 {
		t.Fatalf("validation errs: %v", errs)
	}
	if res.URL != "https://example.test/pr/1" {
		t.Fatalf("got URL %q, want https://example.test/pr/1", res.URL)
	}
	if res.Slug != "2026-05-08-prod-eu-1-abc123" {
		t.Fatalf("got slug %q, want 2026-05-08-prod-eu-1-abc123", res.Slug)
	}
}

func TestPushSessionPR_RefusesDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, clone := initBareAndClone(t)

	// Modify a tracked file to make the working tree dirty
	// (gitWorkingTreeDirty uses --untracked-files=no, so only tracked
	// modifications count).
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(sessionDir, fileMetadata),
		[]byte(`{"id":"abc123def456","namespace":"prod-eu-1","createdAt":"2026-05-08T00:00:00Z"}`),
		0o644)
	_ = os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte("{}\n"), 0o644)

	proposalsDir := t.TempDir()

	caps := Capabilities{
		GH:       GHStatus{Installed: true, Authenticated: true},
		Sessions: SessionsStatus{Configured: true, Valid: true},
	}

	_, _, err := pushSessionPR(context.Background(), caps, clone, clone, "owner/repo", proposalsDir, sessionDir, sessionPushRequest{}, goodDrafter)
	if err == nil {
		t.Fatal("expected error for dirty tree, got nil")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected 'dirty' in error, got: %v", err)
	}
}
