package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// playbookTypeItem is the JSON shape for one entry in
// GET /api/playbook-types. Mirrors `playbook_types` MCP tool output
// (PlaybookType in the strategies package). Source is "system" for
// types with a dir + type.txt in the upstream clone, "user" for
// operator-only additions that haven't been pushed yet.
type playbookTypeItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	// Tracked is true when <name>/type.txt exists at origin/HEAD in the
	// system clone. Used by the frontend to decide whether deleting the
	// type should pop the propose-as-PR modal: untracked types can be
	// removed locally with a single confirm; tracked types need the PR
	// flow because the next sync would otherwise restore them.
	Tracked bool `json:"tracked"`
}

// handleListPlaybookTypes walks the system clone's subdirectories and
// returns each as a type. Frontend uses this to populate the "+ new
// type" modal's "is the name taken?" check + the proposal flow's
// type picker.
//
// GET /api/playbook-types
func (a *apiHandlers) handleListPlaybookTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.opts.PluginPlaybooksDir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"types": []playbookTypeItem{}})
		return
	}
	types, err := readTypeDirs(r.Context(), a.opts.PluginPlaybooksDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"types": types})
}

// handleCreatePlaybookType creates a new type slot:
//
//  1. Writes <systemDir>/<name>/type.txt with the operator-supplied
//     description.
//  2. If the systemDir is a git checkout AND gh is wired, pushes a
//     fresh branch + opens a PR via gh against the upstream repo.
//
// Failing the push doesn't undo the local file write — operators in
// air-gapped envs (no gh / no network) still get the new type
// available locally, which is the more important UX. Push errors
// surface in the response as a non-fatal warning.
//
// POST /api/playbook-types  body: {"name": "...", "description": "..."}
func (a *apiHandlers) handleCreatePlaybookType(w http.ResponseWriter, r *http.Request) {
	if a.opts.PluginPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "system playbooks dir is not configured")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !typeNamePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, "type name must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}")
		return
	}
	typeDir := filepath.Join(a.opts.PluginPlaybooksDir, name)
	if _, err := os.Stat(typeDir); err == nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("type %q already exists", name))
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.MkdirAll(typeDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create type dir: %v", err))
		return
	}
	desc := strings.TrimSpace(body.Description)
	if !strings.HasSuffix(desc, "\n") {
		desc += "\n"
	}
	if err := os.WriteFile(filepath.Join(typeDir, "type.txt"), []byte(desc), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write type.txt: %v", err))
		return
	}

	// Try to push as PR. Failures don't fail the call — the local
	// type is usable immediately; the PR is the share-with-team step.
	prURL, prErr := a.tryPushNewTypeAsPR(r.Context(), name, desc)

	resp := map[string]any{
		"ok":          true,
		"name":        name,
		"description": strings.TrimRight(desc, "\n"),
	}
	if prURL != "" {
		resp["prUrl"] = prURL
	}
	if prErr != nil {
		resp["pushWarning"] = prErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// tryPushNewTypeAsPR creates a fresh branch in the upstream clone,
// commits the new type's files, pushes, and opens a PR via gh.
// Returns (prURL, nil) on success, (..., err) when any step fails so
// the operator sees the failure mode without losing the local write.
func (a *apiHandlers) tryPushNewTypeAsPR(ctx context.Context, typeName, description string) (string, error) {
	repoPath := a.opts.PluginPlaybooksDir
	if repoPath == "" {
		return "", fmt.Errorf("system playbooks dir is not configured")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return "", fmt.Errorf("playbooks dir is not a git checkout — local write only")
	}
	if !a.capabilities.GH.Authenticated {
		return "", fmt.Errorf("gh CLI not ready: %s", a.capabilities.GH.Reason)
	}
	upstream := PlaybooksRepoFor(a.prof, a.opts.PlaybooksRepo)
	base := "main"
	branch := fmt.Sprintf("type/%s-%s", typeName, time.Now().UTC().Format("20060102-150405"))

	if err := resetTreeIfDirty(ctx, repoPath); err != nil {
		return "", err
	}
	if out, err := runGit(ctx, repoPath, "fetch", "origin", base); err != nil {
		return "", fmt.Errorf("git fetch: %w (%s)", err, out)
	}
	if out, err := runGit(ctx, repoPath, "checkout", "-B", branch, "origin/"+base); err != nil {
		return "", fmt.Errorf("git checkout: %w (%s)", err, out)
	}
	if out, err := runGit(ctx, repoPath, "add", typeName+"/type.txt"); err != nil {
		return "", fmt.Errorf("git add: %w (%s)", err, out)
	}
	title := fmt.Sprintf("type(%s): add new playbook type", typeName)
	if out, err := runGit(ctx, repoPath, "commit", "-m", title); err != nil {
		return "", fmt.Errorf("git commit: %w (%s)", err, out)
	}
	// --force-with-lease + best-effort fetch — see repo_pr.go for the
	// rationale (re-push of the same branch updates the PR).
	_, _ = runGit(ctx, repoPath, "fetch", "origin", branch)
	if out, err := runGit(ctx, repoPath, "push", "--force-with-lease", "-u", "origin", branch); err != nil {
		return "", fmt.Errorf("git push: %w (%s)", err, out)
	}
	prBody := fmt.Sprintf("Adds the `%s` playbook type.\n\nDescription:\n\n> %s", typeName, strings.TrimSpace(description))
	return openGHPullRequest(ctx, repoPath, upstream, base, branch, title, prBody)
}

// readTypeDirs walks systemDir and returns one entry per
// subdirectory, with the description from <type>/type.txt and
// `tracked` reflecting whether <type>/type.txt exists at origin/HEAD.
//
// `tracked` is computed via `git cat-file -e origin/HEAD:<name>/type.txt`
// in systemDir. Failures of any kind (no .git, no origin/HEAD, file not
// at that ref) collapse to tracked=false — worst case, the operator
// gets the simple-delete UX for an upstream-tracked type and is mildly
// surprised when sync brings it back.
func readTypeDirs(ctx context.Context, systemDir string) ([]playbookTypeItem, error) {
	entries, err := os.ReadDir(systemDir)
	if err != nil {
		return nil, fmt.Errorf("read system dir: %w", err)
	}
	hasGit := false
	if _, statErr := os.Stat(filepath.Join(systemDir, ".git")); statErr == nil {
		hasGit = true
	}
	var out []playbookTypeItem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !typeNamePattern.MatchString(name) {
			continue
		}
		desc := ""
		if data, err := os.ReadFile(filepath.Join(systemDir, name, "type.txt")); err == nil {
			desc = strings.TrimSpace(string(data))
		}
		tracked := false
		if hasGit {
			if _, gerr := runGit(ctx, systemDir, "cat-file", "-e", "origin/HEAD:"+name+"/type.txt"); gerr == nil {
				tracked = true
			}
		}
		out = append(out, playbookTypeItem{
			Name:        name,
			Description: desc,
			Source:      "system",
			Tracked:     tracked,
		})
	}
	return out, nil
}

// handleDeletePlaybookType removes an empty type slot locally. Refuses
// to delete a type whose directory still contains anything other than
// type.txt — the operator must clear out the contained playbooks first.
//
// This handler does NOT touch git. The local working tree is mutated;
// if the type also exists at origin/HEAD, the next sync will restore
// it. Operators who want the removal to stick upstream call
// POST /api/playbook-types/{name}/propose-removal instead.
//
// DELETE /api/playbook-types/{name}
func (a *apiHandlers) handleDeletePlaybookType(w http.ResponseWriter, r *http.Request) {
	if a.opts.PluginPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "system playbooks dir is not configured")
		return
	}
	name := r.PathValue("name")
	if !typeNamePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, "type name must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}")
		return
	}
	typeDir := filepath.Join(a.opts.PluginPlaybooksDir, name)
	info, err := os.Stat(typeDir)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("type %q not found", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusConflict, fmt.Sprintf("%q exists but is not a directory", name))
		return
	}
	entries, err := os.ReadDir(typeDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var stray []string
	for _, e := range entries {
		if e.Name() == "type.txt" {
			continue
		}
		stray = append(stray, e.Name())
	}
	if len(stray) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"cannot delete: type %q still contains: %s",
			name, strings.Join(stray, ", "),
		))
		return
	}
	if err := os.RemoveAll(typeDir); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("remove type dir: %v", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// typeNamePattern restricts what we accept as a type name. Same
// shape as a playbook id; ends up in the URL/filename so we keep
// it conservative.
var typeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// handleProposePlaybookTypeRemoval removes an empty type slot upstream
// by opening a PR via gh. Mirrors handleCreatePlaybookType: pre-flight
// checks bail without mutating git state, then we cut a fresh branch
// from origin/main, `git rm -r <name>`, commit, push, and `gh pr
// create`. Push/gh failure surfaces as a non-fatal `pushWarning` in
// the response — same shape as create's pushWarning, so the operator's
// next move is clear (retry, or fix gh auth).
//
// POST /api/playbook-types/{name}/propose-removal
func (a *apiHandlers) handleProposePlaybookTypeRemoval(w http.ResponseWriter, r *http.Request) {
	if a.opts.PluginPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "system playbooks dir is not configured")
		return
	}
	name := r.PathValue("name")
	if !typeNamePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, "type name must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}")
		return
	}
	typeDir := filepath.Join(a.opts.PluginPlaybooksDir, name)
	info, err := os.Stat(typeDir)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("type %q not found", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusConflict, fmt.Sprintf("%q exists but is not a directory", name))
		return
	}
	entries, err := os.ReadDir(typeDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var stray []string
	for _, e := range entries {
		if e.Name() == "type.txt" {
			continue
		}
		stray = append(stray, e.Name())
	}
	if len(stray) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"cannot remove: type %q still contains: %s",
			name, strings.Join(stray, ", "),
		))
		return
	}

	// The clone is launcher-managed (see resetTreeIfDirty's comment in
	// repo_pr.go) — auto-recover from any uncommitted state before we
	// `git checkout -B` to a fresh branch instead of refusing the
	// request, since the operator has no in-app affordance to clean it.
	repoPath := a.opts.PluginPlaybooksDir
	if _, gerr := os.Stat(filepath.Join(repoPath, ".git")); gerr != nil {
		writeError(w, http.StatusPreconditionFailed, "playbooks dir is not a git checkout — propose-removal needs a cloned repo")
		return
	}
	if err := resetTreeIfDirty(r.Context(), repoPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	prURL, prErr := a.tryRemoveTypeAsPR(r.Context(), name)
	resp := map[string]any{
		"ok":   true,
		"name": name,
	}
	if prURL != "" {
		resp["prUrl"] = prURL
	}
	if prErr != nil {
		resp["pushWarning"] = prErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// tryRemoveTypeAsPR mirrors tryPushNewTypeAsPR in reverse: cut a fresh
// branch from origin/main, `git rm -r <name>`, commit, push, open a PR
// via gh. Returns (prURL, nil) on success; (..., err) on any failure
// after the pre-flight checks the caller already did.
//
// Like the create flow, we leave the operator on the new branch on
// success — the local working tree no longer has <name>/, which
// matches the operator's intent. If the push or gh step fails the
// branch is still in the desired shape; a retry re-runs `checkout -B`
// (which resets the branch unconditionally) and tries the push again.
//
// TOCTOU note: the caller's emptiness check (in
// handleProposePlaybookTypeRemoval) runs before the branch is cut.
// If a playbook file is written into the type dir in the narrow
// window between that check and the `git rm -r` below, the file
// would be included in the removal commit. On the single-operator
// launcher this window is very narrow, and the operator sees the
// resulting PR's diff before it merges, so we accept it rather than
// re-stat the dir immediately before `git rm`.
func (a *apiHandlers) tryRemoveTypeAsPR(ctx context.Context, typeName string) (string, error) {
	repoPath := a.opts.PluginPlaybooksDir
	upstream := PlaybooksRepoFor(a.prof, a.opts.PlaybooksRepo)
	base := "main"
	branch := fmt.Sprintf("type-remove/%s-%s", typeName, time.Now().UTC().Format("20060102-150405"))

	// Local mutations happen unconditionally — the operator's intent
	// "remove this type" should land in the working tree even if gh is
	// not wired. Push + PR are gated below; failure surfaces as a
	// non-fatal pushWarning while the local tree stays in the
	// "type gone" state.
	if out, err := runGit(ctx, repoPath, "fetch", "origin", base); err != nil {
		return "", fmt.Errorf("git fetch: %w (%s)", err, out)
	}
	if out, err := runGit(ctx, repoPath, "checkout", "-B", branch, "origin/"+base); err != nil {
		return "", fmt.Errorf("git checkout: %w (%s)", err, out)
	}
	if out, err := runGit(ctx, repoPath, "rm", "-r", typeName); err != nil {
		return "", fmt.Errorf("git rm: %w (%s)", err, out)
	}
	title := fmt.Sprintf("type(%s): remove playbook type", typeName)
	if out, err := runGit(ctx, repoPath, "commit", "-m", title); err != nil {
		return "", fmt.Errorf("git commit: %w (%s)", err, out)
	}

	// Push + open PR only when gh is wired. Local working tree is
	// already in the desired state above; this guard simply avoids
	// burning a push on a known-doomed call.
	if !a.capabilities.GH.Authenticated {
		return "", fmt.Errorf("gh CLI not ready: %s", a.capabilities.GH.Reason)
	}
	// Best-effort fetch of the branch in case a prior attempt left a
	// remote ref behind — keeps `--force-with-lease` happy on retries.
	_, _ = runGit(ctx, repoPath, "fetch", "origin", branch)
	if out, err := runGit(ctx, repoPath, "push", "--force-with-lease", "-u", "origin", branch); err != nil {
		return "", fmt.Errorf("git push: %w (%s)", err, out)
	}
	prBody := fmt.Sprintf("Removes the `%s` playbook type. The slot has no playbooks under it locally.", typeName)
	return openGHPullRequest(ctx, repoPath, upstream, base, branch, title, prBody)
}
