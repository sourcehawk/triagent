package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHandleSessionsUpstreamStatus_NoCheckout(t *testing.T) {
	a := &apiHandlers{opts: Options{SessionsPath: ""}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions-upstream", nil)
	a.handleSessionsUpstreamStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}
	var body sessionsUpstreamStatus
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error == "" {
		t.Fatalf("expected error explaining missing path, got empty")
	}
	if body.GitCheckout {
		t.Fatalf("expected GitCheckout=false")
	}
}

func TestHandleSessionsUpstreamStatus_GitCheckout(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "--initial-branch=main", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Make at least one commit so HEAD resolves.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=T", "add", "."},
		{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-m", "init", "--quiet"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}

	const wantRepo = "sourcehawk/triagent-sessions"
	a := &apiHandlers{opts: Options{SessionsPath: dir, SessionsRepo: wantRepo}}
	rr := httptest.NewRecorder()
	a.handleSessionsUpstreamStatus(rr, httptest.NewRequest(http.MethodGet, "/api/sessions-upstream", nil))
	var body sessionsUpstreamStatus
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.GitCheckout {
		t.Fatalf("expected GitCheckout=true")
	}
	if body.Commit == "" {
		t.Fatalf("expected non-empty commit")
	}
	if body.Repo != wantRepo {
		t.Fatalf("got Repo=%q, want %q", body.Repo, wantRepo)
	}
}

func TestHandleSessionsUpstream_ListAndGet(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sessions", "2026-05", "2026-05-08-prod-eu-1-a3f0b2"), 0o755)
	md := `---
schema_version: 1
id: 2026-05-08-prod-eu-1-a3f0b2
date: 2026-05-08
title: "Demo"
author:
  name: T
  email: t@example.com
namespace: prod-eu-1
context_name: prod-eu-1
sources:
  bundle: session.triagent.json
---

## Summary
hi
`
	if err := os.WriteFile(filepath.Join(dir, "sessions", "2026-05", "2026-05-08-prod-eu-1-a3f0b2", "session.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", "2026-05", "2026-05-08-prod-eu-1-a3f0b2", "session.triagent.json"), []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// SessionsPath is the work-dir under the clone where session dirs
	// land. With the default profile (sessions_path=sessions) that's
	// `<clone>/sessions/`, which is what the on-disk fixture above
	// mirrors.
	a := &apiHandlers{opts: Options{SessionsPath: filepath.Join(dir, "sessions")}}

	// list
	rr := httptest.NewRecorder()
	a.handleSessionsUpstreamList(rr, httptest.NewRequest(http.MethodGet, "/api/sessions-upstream/sessions", nil))
	if rr.Code != 200 {
		t.Fatalf("list got %d body=%s", rr.Code, rr.Body.String())
	}
	var list struct{ Sessions []sessionCard `json:"sessions"` }
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Slug != "2026-05-08-prod-eu-1-a3f0b2" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// single
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions-upstream/sessions/2026-05-08-prod-eu-1-a3f0b2", nil)
	req.SetPathValue("slug", "2026-05-08-prod-eu-1-a3f0b2")
	a.handleSessionsUpstreamGet(rr, req)
	if rr.Code != 200 {
		t.Fatalf("get got %d body=%s", rr.Code, rr.Body.String())
	}

	// bundle
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/sessions-upstream/sessions/2026-05-08-prod-eu-1-a3f0b2/bundle", nil)
	req.SetPathValue("slug", "2026-05-08-prod-eu-1-a3f0b2")
	a.handleSessionsUpstreamBundle(rr, req)
	if rr.Code != 200 {
		t.Fatalf("bundle got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "schemaVersion") {
		t.Fatalf("bundle body missing payload: %q", rr.Body.String())
	}
}

// The list endpoint must (a) decorate each card with a per-card
// SyncState computed from the matching local Investigation, and
// (b) filter out closed-PR sessions even though their session.md
// is still on disk. The base test (TestHandleSessionsUpstream_ListAndGet)
// only exercises the manager-nil branch, so a regression that drops
// the SyncState assignment or breaks the closed-PR filter would ship
// green there. This test wires a real manager.
func TestHandleSessionsUpstreamList_DecoratesAndFilters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Three upstream sessions with hex-id slugs (sessionSlugPattern
	// requires the trailing 6 hex chars). One open-PR, one merged-PR,
	// one closed-PR. The closed one must NOT appear in the response.
	sessions := []struct{ slug string }{
		{slug: "2026-05-08-prod-eu-1-aaaaaa"},
		{slug: "2026-05-08-prod-eu-1-bbbbbb"},
		{slug: "2026-05-08-prod-eu-1-cccccc"},
	}
	for _, s := range sessions {
		sessionDir := filepath.Join(dir, "sessions", "2026-05", s.slug)
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "---\nschema_version: 1\nid: " + s.slug + "\ndate: 2026-05-08\ntitle: \"x\"\nauthor:\n  name: T\n  email: t@x\nnamespace: prod-eu-1\ncontext_name: prod-eu-1\nsources:\n  bundle: session.triagent.json\n---\n\n## Summary\nhi\n"
		if err := os.WriteFile(filepath.Join(sessionDir, "session.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Local Investigations that match each slug. computeSessionSlug
	// is YYYY-MM-DD-<namespace-clean>-<id-prefix-6>; we pick id prefixes
	// that match the slugs above by setting CreatedAt + Namespace +
	// matching IDs.
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	created := time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC)
	openPushedAt := created.Add(time.Hour)
	mergedAt := created.Add(2 * time.Hour)
	closedAt := created.Add(3 * time.Hour)

	openInv, err := mgr.Register(&Investigation{
		ID:        "aaaaaa0000000000000000000000000a",
		Namespace: "prod-eu-1",
		CreatedAt: created,
	})
	require.NoError(t, err)
	openInv.PushedAt = &openPushedAt
	openInv.PushURL = "https://github.com/example/sessions/pull/1"
	openInv.PRState = PRStateOpen

	mergedInv, err := mgr.Register(&Investigation{
		ID:        "bbbbbb0000000000000000000000000b",
		Namespace: "prod-eu-1",
		CreatedAt: created,
	})
	require.NoError(t, err)
	mergedInv.PushedAt = &mergedAt
	mergedInv.PushURL = "https://github.com/example/sessions/pull/2"
	mergedInv.PRState = PRStateMerged
	mergedInv.PRMergedAt = &mergedAt

	closedInv, err := mgr.Register(&Investigation{
		ID:        "cccccc0000000000000000000000000c",
		Namespace: "prod-eu-1",
		CreatedAt: created,
	})
	require.NoError(t, err)
	closedInv.PushedAt = &closedAt
	closedInv.PushURL = "https://github.com/example/sessions/pull/3"
	closedInv.PRState = PRStateClosed
	closedInv.PRClosedAt = &closedAt

	a := &apiHandlers{opts: Options{SessionsPath: filepath.Join(dir, "sessions")}, manager: mgr}
	rr := httptest.NewRecorder()
	a.handleSessionsUpstreamList(rr, httptest.NewRequest(http.MethodGet, "/api/sessions-upstream/sessions", nil))
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body)

	var list struct {
		Sessions []sessionCard `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))

	bySlug := map[string]sessionCard{}
	for _, c := range list.Sessions {
		bySlug[c.Slug] = c
	}
	require.Len(t, list.Sessions, 2, "closed-PR session must be filtered out; got: %+v", list.Sessions)
	require.NotContains(t, bySlug, "2026-05-08-prod-eu-1-cccccc", "closed-PR slug must not appear in the list")

	openCard := bySlug["2026-05-08-prod-eu-1-aaaaaa"]
	require.Equal(t, SyncStatusSynced, openCard.SyncState.Status, "open PR is on upstream → synced")
	require.NotNil(t, openCard.SyncState.PR, "synced session with PushURL must carry the PR sidecar")
	require.Equal(t, "open", openCard.SyncState.PR.State)

	mergedCard := bySlug["2026-05-08-prod-eu-1-bbbbbb"]
	require.Equal(t, SyncStatusSynced, mergedCard.SyncState.Status)
	require.NotNil(t, mergedCard.SyncState.PR)
	require.Equal(t, "merged", mergedCard.SyncState.PR.State)
	require.NotEmpty(t, mergedCard.SyncState.PR.MergedAt, "merged PR ships RFC3339 timestamp")
}
