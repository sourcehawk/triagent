package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/wiki"
	"gopkg.in/yaml.v3"
)

// wikiEntryBody is the slice of a resolved entry that
// wikiBundleAutoIncludePaths needs: vault-relative path + raw file
// contents (frontmatter+body). Decoupled from the local resolvedFile
// type so the auto-include helper is pure-data and unit-testable.
type wikiEntryBody struct {
	rel     string
	content []byte
}

// wikiBundleAutoIncludePaths picks vault-relative entity-stub paths
// the bundle handler should auto-attach to a wiki PR: for each entry
// in entries, walk [[wikilinks]] in its body, and emit any stub that
// (a) has an unsynced commit (i.e. exists in unsynced) and (b) is not
// already in included. Order is stable: (entry-order, then dir-order
// services→errors→symptoms→components, then wikilink-order); each
// path is emitted at most once even when referenced by multiple
// entries.
//
// The auto-include scope is the resolved entries' wikilinks because a
// broader scope (e.g. every file in unsynced) silently bundles other
// locally-committed-but-unpushed entries — and their stubs — into the
// current entry's PR. Those other entries are intended to ship as
// their own PRs and must not piggy-back.
func wikiBundleAutoIncludePaths(entries []wikiEntryBody, unsynced, included map[string]bool) []string {
	added := make(map[string]bool, len(included))
	for k := range included {
		added[k] = true
	}
	var out []string
	for _, eb := range entries {
		_, body, err := wikiSplitFrontmatter(eb.content)
		if err != nil {
			continue
		}
		matches := wikilinkPattern.FindAllStringSubmatch(string(body), -1)
		for _, m := range matches {
			name := m[1]
			for _, dir := range []string{"services", "errors", "symptoms", "components"} {
				stubRel := filepath.Join("entities", dir, name+".md")
				if !unsynced[stubRel] || added[stubRel] {
					continue
				}
				added[stubRel] = true
				out = append(out, stubRel)
			}
		}
	}
	return out
}

// ── PUT /api/wiki/entries/{slug} ─────────────────────────────────────────────
//
// handleUpdateWikiEntry accepts a partial patch of an existing entry:
// title, severity, and the markdown body. id/date/status/services/errors/
// symptoms/links are preserved verbatim from the on-disk frontmatter so
// the endpoint can't accidentally rewire entity links — those edits are
// out-of-scope for this iteration.
//
// Body: {"title"?: string, "severity"?: "sev1"|"sev2"|"sev3"|"", "markdown": string}
//
// On success: writes the file, commits with message `wiki: edit <slug>`,
// returns {"ok": true, "commit": "<sha>"}.
// On per-field validation failure: returns 400 with {"ok": false, "errors": [...]}.
// Refuses on a dirty working tree the same way resolve/delete do.
func (a *apiHandlers) handleUpdateWikiEntry(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := wikiValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.capabilities.Wiki.Valid {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "wiki vault not ready: " + a.capabilities.Wiki.Reason})
		return
	}

	var req struct {
		Title    *string `json:"title,omitempty"`
		Severity *string `json:"severity,omitempty"`
		Markdown string  `json:"markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}

	entryRel := filepath.Join("entries", slug+".md")
	entryAbs := filepath.Join(a.opts.WikiPath, entryRel)
	existing, err := os.ReadFile(entryAbs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "entry "+slug+" not found in vault")
			return
		}
		writeError(w, http.StatusInternalServerError, "read entry: "+err.Error())
		return
	}

	fmBytes, _, err := wikiSplitFrontmatter(existing)
	if err != nil {
		writeError(w, http.StatusBadRequest, "entry missing valid frontmatter: "+err.Error())
		return
	}
	var fm wikiFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		writeError(w, http.StatusBadRequest, "frontmatter parse: "+err.Error())
		return
	}

	// Per-field validation. Collect everything before bailing so the
	// operator sees all fixes at once.
	var errs []string
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" {
			errs = append(errs, "title cannot be empty")
		} else {
			fm.Title = t
		}
	}
	if req.Severity != nil {
		s := strings.TrimSpace(*req.Severity)
		if !wikiValidSeverity[s] {
			errs = append(errs, fmt.Sprintf("severity %q invalid; want one of sev1|sev2|sev3 (or empty)", s))
		} else {
			fm.Severity = s
		}
	}
	body := req.Markdown
	if bodyErrs := wikiValidateBodyHeaders(body); len(bodyErrs) > 0 {
		errs = append(errs, bodyErrs...)
	}
	if len(errs) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": errs})
		return
	}

	// Refuse on dirty tree — mixing operator changes with the edit
	// commit would surprise the next push-PR.
	if dirty, err := gitWorkingTreeDirty(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if dirty {
		writeError(w, http.StatusConflict, "wiki working tree is dirty — commit or stash before editing")
		return
	}

	fmOut, err := marshalWikiFrontmatter(fm)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal frontmatter: "+err.Error())
		return
	}
	newContent := "---\n" + fmOut + "---\n" + body
	if err := os.WriteFile(entryAbs, []byte(newContent), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "write entry: "+err.Error())
		return
	}

	if out, err := runGit(r.Context(), a.opts.WikiPath, "add", entryRel); err != nil {
		writeError(w, http.StatusInternalServerError, "git add: "+err.Error()+" ("+out+")")
		return
	}
	// No-op edits (e.g. saving the same content twice) leave the index
	// clean — return ok with an empty commit so the client can drop the
	// dirty draft without bothering the operator.
	if changed, err := gitHasStagedChanges(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !changed {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": "", "noop": true})
		return
	}
	commitMsg := "wiki: edit " + slug
	if out, err := runGit(r.Context(), a.opts.WikiPath, "commit", "-m", commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, "git commit: "+err.Error()+" ("+out+")")
		return
	}
	sha, _ := runGit(r.Context(), a.opts.WikiPath, "rev-parse", "--short", "HEAD")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": strings.TrimSpace(sha)})
}

// ── PUT /api/wiki/entities/{type}/{name} ─────────────────────────────────────
//
// handleUpdateWikiEntity accepts a body-only patch. Entity frontmatter
// (type/name/created) is effectively immutable — renaming would break
// every backlink; the type lives in the directory name; created is
// historical fact. So this endpoint preserves the frontmatter bytes
// verbatim and only swaps the body that follows the second ---.
func (a *apiHandlers) handleUpdateWikiEntity(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "wiki vault not ready: " + a.capabilities.Wiki.Reason})
		return
	}

	entityType := r.PathValue("type")
	entityName := r.PathValue("name")
	validEntityTypes := map[string]bool{"service": true, "error": true, "symptom": true, "component": true}
	if !validEntityTypes[entityType] {
		writeError(w, http.StatusBadRequest, "type must be one of: service, error, symptom, component")
		return
	}
	if !entityNameRe.MatchString(entityName) {
		writeError(w, http.StatusBadRequest, "name must match ^[a-z0-9][a-z0-9-]*$")
		return
	}

	var req struct {
		Markdown string `json:"markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}

	dir := wiki.TypeToDir(entityType)
	relPath := filepath.Join("entities", dir, entityName+".md")
	abs := filepath.Join(a.opts.WikiPath, relPath)
	existing, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "entity not found: "+relPath)
			return
		}
		writeError(w, http.StatusInternalServerError, "read entity: "+err.Error())
		return
	}
	fmBytes, _, err := wikiSplitFrontmatter(existing)
	if err != nil {
		writeError(w, http.StatusBadRequest, "entity missing valid frontmatter: "+err.Error())
		return
	}

	if dirty, err := gitWorkingTreeDirty(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if dirty {
		writeError(w, http.StatusConflict, "wiki working tree is dirty — commit or stash before editing")
		return
	}

	newContent := "---\n" + string(fmBytes) + "\n---\n" + req.Markdown
	if err := os.WriteFile(abs, []byte(newContent), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "write entity: "+err.Error())
		return
	}

	if out, err := runGit(r.Context(), a.opts.WikiPath, "add", relPath); err != nil {
		writeError(w, http.StatusInternalServerError, "git add: "+err.Error()+" ("+out+")")
		return
	}
	if changed, err := gitHasStagedChanges(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !changed {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": "", "noop": true})
		return
	}
	commitMsg := fmt.Sprintf("wiki: edit %s/%s", entityType, entityName)
	if out, err := runGit(r.Context(), a.opts.WikiPath, "commit", "-m", commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, "git commit: "+err.Error()+" ("+out+")")
		return
	}
	sha, _ := runGit(r.Context(), a.opts.WikiPath, "rev-parse", "--short", "HEAD")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": strings.TrimSpace(sha)})
}

// ── POST /api/wiki/push-pr-bundle ────────────────────────────────────────────
//
// handlePushWikiPRBundle opens a single upstream PR that bundles edits to
// an entry plus zero or more linked entity files. The client picks the
// bundle (typically: every dirty file from the editor session that is
// already locally committed). The handler:
//
//  1. Validates capabilities and the file list.
//  2. Reads each file from the local vault (post-commit, so the content
//     reflects the operator's saved state).
//  3. Refuses on a dirty working tree.
//  4. Fetches origin/<base>, checks out a fresh side branch.
//  5. Re-applies every bundled file onto the side branch.
//  6. Commits all changes in one commit, pushes, opens a PR.
//
// Mirrors pushWikiPR's structure, generalised for multiple files. The
// existing single-entry push-pr endpoint stays as a thin wrapper for
// one release before being removed.
type pushWikiBundleFile struct {
	Kind string `json:"kind"` // "entry" or "entity"
	// For entry kind:
	Slug string `json:"slug,omitempty"`
	// For entity kind:
	EntityType string `json:"entity_type,omitempty"`
	EntityName string `json:"entity_name,omitempty"`
}

type pushWikiBundleRequest struct {
	Files []pushWikiBundleFile `json:"files"`
	// Optional PR fields. Empty values mean "use defaults".
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	// RootDisplay is what to put in the default title — e.g. the
	// entry slug when the page is entry-rooted, or the entity name
	// when entity-rooted. Optional; falls back to the first file
	// in the bundle.
	RootDisplay string `json:"root_display,omitempty"`
}

func (a *apiHandlers) handlePushWikiPRBundle(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "wiki", a.opts.WikiRepo) {
		return
	}
	if !a.capabilities.GH.Authenticated {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "gh CLI not ready: " + a.capabilities.GH.Reason})
		return
	}
	if !a.capabilities.Wiki.Valid {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "wiki vault not ready: " + a.capabilities.Wiki.Reason})
		return
	}

	var req pushWikiBundleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "files is required and must contain at least one entry")
		return
	}

	type resolvedFile struct {
		rel     string
		abs     string
		content []byte
		display string // human-readable label for the PR body
	}

	resolved := make([]resolvedFile, 0, len(req.Files))
	validEntityTypes := map[string]bool{"service": true, "error": true, "symptom": true, "component": true}
	for _, f := range req.Files {
		switch f.Kind {
		case "entry":
			if err := wikiValidateSlug(f.Slug); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			rel := filepath.Join("entries", f.Slug+".md")
			abs := filepath.Join(a.opts.WikiPath, rel)
			data, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "entry "+f.Slug+" not in vault")
					return
				}
				writeError(w, http.StatusInternalServerError, "read entry: "+err.Error())
				return
			}
			resolved = append(resolved, resolvedFile{rel: rel, abs: abs, content: data, display: f.Slug})
		case "entity":
			if !validEntityTypes[f.EntityType] {
				writeError(w, http.StatusBadRequest, "entity_type must be one of: service, error, symptom, component")
				return
			}
			if !entityNameRe.MatchString(f.EntityName) {
				writeError(w, http.StatusBadRequest, "entity_name must match ^[a-z0-9][a-z0-9-]*$")
				return
			}
			rel := filepath.Join("entities", wiki.TypeToDir(f.EntityType), f.EntityName+".md")
			abs := filepath.Join(a.opts.WikiPath, rel)
			data, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "entity "+f.EntityType+"/"+f.EntityName+" not in vault")
					return
				}
				writeError(w, http.StatusInternalServerError, "read entity: "+err.Error())
				return
			}
			resolved = append(resolved, resolvedFile{rel: rel, abs: abs, content: data, display: f.EntityType + "/" + f.EntityName})
		default:
			writeError(w, http.StatusBadRequest, "kind must be one of: entry, entity")
			return
		}
	}

	// Auto-include unsynced ENTITY STUBS referenced by [[wikilinks]] in
	// the resolved entries. The editor only loads files into its
	// in-memory drafts when the operator navigates to them, so a
	// freshly-committed entity stub the operator never opened wouldn't
	// otherwise make it into the bundle.
	//
	// Scope matters: previously this auto-included EVERY unsynced wiki
	// file, which silently bundled other locally-committed-but-unpushed
	// entries (and their stubs) into the current entry's PR. Now we
	// walk only the resolved entries' wikilinks, so other unsynced
	// entries stay in their own PR when the operator pushes them.
	resolvedRels := make(map[string]bool, len(resolved))
	entryBodies := make([]wikiEntryBody, 0, len(resolved))
	for _, f := range resolved {
		resolvedRels[f.rel] = true
		if strings.HasPrefix(f.rel, "entries/") {
			entryBodies = append(entryBodies, wikiEntryBody{rel: f.rel, content: f.content})
		}
	}
	for _, stubRel := range wikiBundleAutoIncludePaths(entryBodies, unsyncedWikiFiles(r.Context(), a.opts.WikiPath), resolvedRels) {
		abs := filepath.Join(a.opts.WikiPath, stubRel)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		resolved = append(resolved, resolvedFile{rel: stubRel, abs: abs, content: data, display: stubRel})
		resolvedRels[stubRel] = true
	}

	if dirty, err := gitWorkingTreeDirty(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if dirty {
		writeError(w, http.StatusConflict, "wiki working tree is dirty — commit or stash before pushing a PR")
		return
	}

	rootDisplay := strings.TrimSpace(req.RootDisplay)
	if rootDisplay == "" && len(resolved) > 0 {
		rootDisplay = resolved[0].display
	}

	upstream := WikiRepoFor(a.prof, a.opts.WikiRepo)

	base := strings.TrimSpace(req.Base)
	if base == "" {
		base = "main"
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		// Sanitise rootDisplay for the branch name — entity displays
		// contain a slash which git rejects in the trailing segment.
		safeRoot := strings.ReplaceAll(rootDisplay, "/", "-")
		branch = fmt.Sprintf("wiki/edit-%s-%s", safeRoot, time.Now().UTC().Format("20060102"))
	}
	if !branchSafe.MatchString(branch) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("branch name %q has unsupported characters (allowed: [A-Za-z0-9._/-])", branch))
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		if len(resolved) == 1 {
			title = "wiki: edit " + rootDisplay
		} else {
			title = fmt.Sprintf("wiki: edit %s + %d linked", rootDisplay, len(resolved)-1)
		}
	}

	prBody := strings.TrimSpace(req.Body)
	if prBody == "" {
		var b strings.Builder
		b.WriteString("Edits to the following wiki files from the triagent launcher:\n\n")
		for _, f := range resolved {
			b.WriteString("- `")
			b.WriteString(f.rel)
			b.WriteString("`\n")
		}
		prBody = b.String()
	}

	// Side branch from origin/<base>; re-apply each file's local content.
	if out, err := runGit(r.Context(), a.opts.WikiPath, "fetch", "origin", base); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("git fetch origin %s: %s (%s)", base, err.Error(), out))
		return
	}
	if out, err := runGit(r.Context(), a.opts.WikiPath, "checkout", "-B", branch, "origin/"+base); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("git checkout -B %s origin/%s: %s (%s)", branch, base, err.Error(), out))
		return
	}

	for _, f := range resolved {
		if err := os.MkdirAll(filepath.Dir(f.abs), 0o700); err != nil {
			writeError(w, http.StatusInternalServerError, "mkdir: "+err.Error())
			return
		}
		if err := os.WriteFile(f.abs, f.content, 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, "write file: "+err.Error())
			return
		}
	}

	if out, err := runGit(r.Context(), a.opts.WikiPath, "add", "-A"); err != nil {
		writeError(w, http.StatusInternalServerError, "git add: "+err.Error()+" ("+out+")")
		return
	}
	if changed, err := gitHasStagedChanges(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !changed {
		// All files match origin/<base> already — nothing to PR.
		_, _ = runGit(r.Context(), a.opts.WikiPath, "checkout", base)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": []string{"no changes to commit — all bundled files already match " + base}})
		return
	}
	if out, err := runGit(r.Context(), a.opts.WikiPath, "commit", "-m", title); err != nil {
		writeError(w, http.StatusInternalServerError, "git commit: "+err.Error()+" ("+out+")")
		return
	}
	// Use --force-with-lease pinned to the actual current hash of
	// origin/<branch> as reported by `git ls-remote`. The simpler
	// "fetch + --force-with-lease without explicit value" pattern
	// relies on the local refs/remotes/origin/<branch> tracking ref
	// being in sync after the fetch — which doesn't always happen
	// reliably (we recreate the local branch from origin/<base>, and
	// some setups bounce the next push with "stale info" even after
	// an explicit-refspec fetch). Pinning the lease to the live remote
	// hash sidesteps the tracking-ref dance entirely. An empty result
	// means the branch isn't on origin yet (first push), so we drop
	// the lease — there's no upstream ref to protect.
	lsOut, lsErr := runGit(r.Context(), a.opts.WikiPath, "ls-remote", "origin", "refs/heads/"+branch)
	if lsErr != nil {
		writeError(w, http.StatusInternalServerError, "git ls-remote origin "+branch+": "+lsErr.Error()+" ("+lsOut+")")
		return
	}
	remoteHash := ""
	if fields := strings.Fields(lsOut); len(fields) > 0 {
		remoteHash = fields[0]
	}
	pushArgs := []string{"push"}
	if remoteHash != "" {
		pushArgs = append(pushArgs, "--force-with-lease=refs/heads/"+branch+":"+remoteHash)
	}
	pushArgs = append(pushArgs, "-u", "origin", branch)
	if out, err := runGit(r.Context(), a.opts.WikiPath, pushArgs...); err != nil {
		writeError(w, http.StatusInternalServerError, "git push: "+err.Error()+" ("+out+")")
		return
	}

	url, err := openGHPullRequest(r.Context(), a.opts.WikiPath, upstream, base, branch, title, prBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"url":    url,
		"branch": branch,
		"base":   base,
	})
}

// ── GET /api/wiki/open-prs ───────────────────────────────────────────────────
//
// handleListOpenWikiPRs returns the open upstream PRs whose head branch
// looks like a wiki edit for the given root display id. The push modal
// surfaces these so the operator sees existing in-flight work for the
// same wiki page before — and after — opening a fresh PR.
//
// Match heuristic: head branch starts with `wiki/edit-<safeRoot>-` or
// `wiki/<safeRoot>-` (covers both the bundle-edit naming and the older
// single-incident push naming). safeRoot mirrors the modal's branch
// derivation: rootDisplay with `/` swapped for `-` so entity-rooted
// pages match too (e.g. `service/foo` → `service-foo`).
func (a *apiHandlers) handleListOpenWikiPRs(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.GH.Authenticated {
		writeJSON(w, http.StatusOK, map[string]any{"prs": []any{}, "skipped": "gh not authenticated"})
		return
	}
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	if root == "" {
		writeError(w, http.StatusBadRequest, "root query parameter is required")
		return
	}
	safeRoot := strings.ReplaceAll(root, "/", "-")
	upstream := WikiRepoFor(a.prof, a.opts.WikiRepo)

	listCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(listCtx, "gh", "pr", "list",
		"--repo", upstream,
		"--state", "open",
		"--json", "number,url,title,headRefName,updatedAt",
		"--limit", "200",
	)
	out, err := cmd.Output()
	if err != nil {
		// gh failed (network, repo gone, etc.) — return empty so the modal
		// stays usable instead of bouncing on a non-blocking warning.
		writeJSON(w, http.StatusOK, map[string]any{"prs": []any{}, "skipped": "gh pr list failed: " + err.Error()})
		return
	}
	var entries []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		UpdatedAt   string `json:"updatedAt"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"prs": []any{}, "skipped": "gh pr list parse: " + err.Error()})
		return
	}
	editPrefix := "wiki/edit-" + safeRoot + "-"
	legacyPrefix := "wiki/" + safeRoot + "-"
	matches := make([]map[string]any, 0)
	for _, e := range entries {
		if !strings.HasPrefix(e.HeadRefName, editPrefix) && !strings.HasPrefix(e.HeadRefName, legacyPrefix) {
			continue
		}
		matches = append(matches, map[string]any{
			"number":      e.Number,
			"url":         e.URL,
			"title":       e.Title,
			"branch":      e.HeadRefName,
			"updated_at":  e.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"prs": matches})
}
