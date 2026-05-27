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
)

// gitInit runs `git init -b main` in dir and configures a deterministic
// identity so commits don't depend on the running user's git config.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"-C", dir, "init", "--initial-branch=main", "--quiet"},
		{"-C", dir, "config", "user.email", "t@example.com"},
		{"-C", dir, "config", "user.name", "T"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// gitCommitAll stages everything and commits with the given message.
func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"-C", dir, "add", "-A"},
		{"-C", dir, "commit", "-m", msg, "--quiet"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// gitSetupOriginWithMain creates a bare "origin" sibling of dir, pushes
// main to it, and sets origin/HEAD so the tracked check can resolve
// `origin/HEAD:<path>` lookups offline.
func gitSetupOriginWithMain(t *testing.T, dir string) {
	t.Helper()
	originDir := dir + ".origin.git"
	if out, err := exec.Command("git", "init", "--bare", "--initial-branch=main", "--quiet", originDir).CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"-C", dir, "remote", "add", "origin", originDir},
		{"-C", dir, "push", "--quiet", "-u", "origin", "main"},
		{"-C", dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestHandleListPlaybookTypes_EmptyDir_ReturnsEmptyArray locks in the
// JSON contract for the empty case: `types` must serialize to `[]`, never
// `null`. The frontend's PlaybookList passes the response into a
// `for (const t of availableTypes)` loop inside a useMemo, which throws
// "$ is not iterable" on null — a default-profile install with no
// upstream playbook types on disk would otherwise crash the page.
func TestHandleListPlaybookTypes_EmptyDir_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-types", nil).
		WithContext(context.Background())
	a.handleListPlaybookTypes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, rr.Body.String())
	}
	if got := string(raw["types"]); got != "[]" {
		t.Errorf("types must serialize as []; got %s (full body: %s)", got, rr.Body.String())
	}
}

func TestHandleListPlaybookTypes_TrackedField(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)

	dir := t.TempDir()
	gitInit(t, dir)

	// Tracked type: committed to main, pushed to origin.
	if err := os.MkdirAll(filepath.Join(dir, "investigation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "investigation", "type.txt"), []byte("inv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir, "init")
	gitSetupOriginWithMain(t, dir)

	// Untracked type: dir + type.txt on disk, never committed.
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "type.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/playbook-types", nil).
		WithContext(context.Background())
	a.handleListPlaybookTypes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Types []playbookTypeItem `json:"types"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ty := range body.Types {
		got[ty.Name] = ty.Tracked
	}
	if v, ok := got["investigation"]; !ok || !v {
		t.Errorf("investigation should be tracked=true, got %v (present=%v)", v, ok)
	}
	if v, ok := got["scratch"]; !ok || v {
		t.Errorf("scratch should be tracked=false, got %v (present=%v)", v, ok)
	}
}

func TestHandleDeletePlaybookType_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "type.txt"), []byte("desc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/playbook-types/scratch", nil)
	req.SetPathValue("name", "scratch")
	a.handleDeletePlaybookType(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch")); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone, stat err=%v", err)
	}
}

func TestHandleDeletePlaybookType_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/playbook-types/missing", nil)
	req.SetPathValue("name", "missing")
	a.handleDeletePlaybookType(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleDeletePlaybookType_NonEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "type.txt"), []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "pod-loop.yaml"), []byte("id: pod-loop\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/playbook-types/scratch", nil)
	req.SetPathValue("name", "scratch")
	a.handleDeletePlaybookType(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pod-loop.yaml") {
		t.Errorf("expected error to name the offending entry, got %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "scratch", "pod-loop.yaml")); err != nil {
		t.Errorf("contents must remain on disk: %v", err)
	}
}

func TestHandleDeletePlaybookType_BadName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/playbook-types/..%2Fetc", nil)
	req.SetPathValue("name", "../etc")
	a.handleDeletePlaybookType(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProposePlaybookTypeRemoval_BadName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbook-types/..%2Fetc/propose-removal", nil)
	req.SetPathValue("name", "../etc")
	a.handleProposePlaybookTypeRemoval(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProposePlaybookTypeRemoval_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbook-types/missing/propose-removal", nil)
	req.SetPathValue("name", "missing")
	a.handleProposePlaybookTypeRemoval(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProposePlaybookTypeRemoval_NonEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "type.txt"), []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "p.yaml"), []byte("id: p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &apiHandlers{opts: Options{PluginPlaybooksDir: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbook-types/scratch/propose-removal", nil)
	req.SetPathValue("name", "scratch")
	a.handleProposePlaybookTypeRemoval(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProposePlaybookTypeRemoval_GHNotAuthenticated_RemovesLocally(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)

	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch", "type.txt"), []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir, "init")
	gitSetupOriginWithMain(t, dir)

	a := &apiHandlers{
		opts: Options{PluginPlaybooksDir: dir, PlaybooksRepo: "sourcehawk/triagent-playbooks"},
		// Authenticated=false — gh check should still let the local
		// mutations land before the push step bails.
		capabilities: Capabilities{GH: GHStatus{Installed: true, Authenticated: false, Reason: "test"}},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbook-types/scratch/propose-removal", nil)
	req.SetPathValue("name", "scratch")
	a.handleProposePlaybookTypeRemoval(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pushWarning") {
		t.Errorf("expected pushWarning in response, got %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "gh CLI not ready") {
		t.Errorf("expected pushWarning to name gh, got %s", rr.Body.String())
	}
	// The critical assertion: the local working tree no longer has the
	// type dir, even though the push step bailed. This is what
	// distinguishes the fix from the bug.
	if _, err := os.Stat(filepath.Join(dir, "scratch")); !os.IsNotExist(err) {
		t.Errorf("expected scratch/ to be removed locally, stat err=%v", err)
	}
}

// TestHandleProposePlaybookTypeRemoval_DirtyTreeAutoRecovers verifies the
// handler clears uncommitted state in the launcher-managed clone instead
// of refusing — operators don't author here, and refusing left them with
// no in-app way to recover after a prior PR flow left dirt behind.
//
// The handler still bails (here on missing gh), but only after the
// recovery has already restored the tree to HEAD.
func TestHandleProposePlaybookTypeRemoval_DirtyTreeAutoRecovers(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)

	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(dir, "scratch", "type.txt")
	if err := os.WriteFile(tracked, []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, dir, "init")
	// Mutate a tracked file so the working tree is dirty per
	// `git status --porcelain --untracked-files=no`.
	if err := os.WriteFile(tracked, []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &apiHandlers{
		opts:         Options{PluginPlaybooksDir: dir, PlaybooksRepo: "sourcehawk/triagent-playbooks"},
		capabilities: Capabilities{GH: GHStatus{Installed: false, Authenticated: false, Reason: "gh not installed"}},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbook-types/scratch/propose-removal", nil)
	req.SetPathValue("name", "scratch")
	a.handleProposePlaybookTypeRemoval(rr, req)

	// The dirty-tree path no longer short-circuits with 412 — recovery
	// runs first, then the request continues into tryRemoveTypeAsPR
	// which surfaces a non-fatal pushWarning when gh is missing.
	if rr.Code == http.StatusPreconditionFailed && strings.Contains(rr.Body.String(), "dirty") {
		t.Fatalf("expected dirty tree to auto-recover, got 412 dirty error; body=%s", rr.Body.String())
	}
	// And the tracked file must be back at its committed content.
	got, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatalf("read tracked file: %v", err)
	}
	if string(got) != "d\n" {
		t.Errorf("tracked file not restored to HEAD; got %q want %q", string(got), "d\n")
	}
}
