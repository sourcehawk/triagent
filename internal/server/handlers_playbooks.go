package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// playbookListItem is the per-row shape returned by GET /api/playbooks.
// `source` distinguishes the upstream library ("plugin" — overridable),
// launcher-bundled metas ("system" — locked, see Locked field),
// user-only ("user"), user-overriding-a-plugin-id ("override"), broken
// file ("broken"), or operator-disabled ("disabled") so the editor can
// render the right badge and offer the right actions.
//
// Each user file is the single source-of-truth for its id; there are
// no sibling versions to track — git history replaces the versioned-file
// layout. The detail endpoint (GET /api/playbooks/{id}) augments the
// response with an inline commits[] slice.
//   - Disabled: true when a tombstone (active=false) has suppressed the
//     embedded version.
type playbookListItem struct {
	ID          string `json:"id"`
	Symptom     string `json:"symptom,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // "plugin" | "system" | "user" | "override" | "broken" | "disabled"
	// Locked is true for entries the launcher ships bundled (system
	// metas: master investigation entrypoint + workflow metas). The
	// editor should disable save / delete / type-change / push-PR
	// when locked — these files are owned by the launcher binary, not
	// the operator. Always false for plugin / user / override entries.
	Locked    bool   `json:"locked,omitempty"`
	NodeCount int    `json:"nodeCount"`
	YAML      string `json:"yaml"`
	Disabled  bool   `json:"disabled,omitempty"`
	// Type mirrors the strategies-side classifier: "investigation"
	// (default) vs "general" (meta/workflow). Empty = inferred as
	// the default — front-end normalises before rendering.
	Type string `json:"type,omitempty"`
	// SyncState is the authoritative answer to "is this playbook in
	// sync with upstream?". Computed by playbookSyncState — the same
	// resolver the editor's preview endpoint uses, so the list and
	// the editor never disagree about drift.
	SyncState SyncState `json:"syncState"`
	// UpstreamYAML carries the raw upstream-clone YAML for this id
	// when one exists. The editor uses it to gate the push-PR button
	// against the operator's *draft* (not just the saved active
	// version), so unsaved edits that would create a real PR diff
	// unlock the button. Empty when there's no upstream counterpart.
	UpstreamYAML string `json:"upstreamYAML,omitempty"`
	// SchemaVersion is the playbook contract version this YAML
	// targets (see strategies.CurrentSchemaVersion). Empty / 0 means
	// the field wasn't set; the loader treats that as 1 for
	// back-compat — the JSON returns 1 in that case so the frontend
	// renders a concrete number.
	SchemaVersion int `json:"schemaVersion"`
	// SchemaMismatch is true when SchemaVersion differs from the
	// runtime's current version. The frontend uses it to render a
	// warning + (Phase 2) a migrate button.
	SchemaMismatch bool `json:"schemaMismatch,omitempty"`
}

// handleListPlaybooks merges the cached embedded set with whatever's
// on disk under the user playbooks dir and returns the union, sorted by
// id. User entries with a system id win and get marked "override"; user
// entries with a fresh id are "user"; embedded-only entries are "system".
//
// GET /api/playbooks
func (a *apiHandlers) handleListPlaybooks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items := a.collectPlaybooks()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"playbooks": items})
}

// handleGetPlaybook returns one playbook by id, with the same source
// resolution as the list. The detail response augments the list shape
// with a commits[] field containing the 50 most recent merged commits
// across both the user and upstream repos. 404 when the id is unknown
// to both system and user sets.
//
// GET /api/playbooks/{id}
func (a *apiHandlers) handleGetPlaybook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	for _, p := range a.collectPlaybooks() {
		if p.ID != id {
			continue
		}
		// Augment with recent commits across both repos. Best
		// effort: empty list if either repo is missing.
		typeName := p.Type
		if typeName == "" {
			typeName = "investigation"
		}
		relPath := filepath.Join(typeName, id+".yaml")
		local, _ := gitLogForPlaybook(r.Context(), a.opts.UserPlaybooksDir, relPath, "local", "", 50)
		upstream, _ := gitLogForPlaybook(r.Context(), a.opts.PluginPlaybooksDir, relPath, "upstream", "", 50)
		commits := mergePlaybookCommits(local, upstream, 50)
		out := struct {
			playbookListItem
			Commits []playbookCommit `json:"commits"`
		}{p, commits}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeError(w, http.StatusNotFound, "playbook not found")
}

// handlePutPlaybook validates the YAML body via `triagent-mcp validate-playbook`
// and writes <UserPlaybooksDir>/<id>.yaml. The id in the URL must match
// the YAML's own id field; we reject mismatches to avoid confusing
// rename semantics.
//
// POST /api/playbooks/{id}  body: {"yaml": "...", "type": "investigation"}
//
// Type is required: the launcher writes <userdir>/<type>/<id>.yaml
// and the field tells the loader which type slot the file belongs to.
// The frontend already knows the type from the loaded list/get
// payload (PlaybookListItem.Type / PlaybookListItem.YAML's source)
// so it sends it back unchanged on save.
func (a *apiHandlers) handlePutPlaybook(w http.ResponseWriter, r *http.Request) {
	if a.opts.UserPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "user playbooks dir is not configured")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	var body struct {
		YAML    string `json:"yaml"`
		Type    string `json:"type"`
		Active  *bool  `json:"active,omitempty"`
		Message string `json:"message,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.YAML) == "" {
		writeError(w, http.StatusBadRequest, "yaml is required")
		return
	}
	if strings.TrimSpace(body.Type) == "" {
		writeError(w, http.StatusBadRequest, "type is required (the playbook's type slot — investigation, general, …)")
		return
	}
	// Default to active=true for back-compat with callers (proposal
	// promotion, scripts) that don't carry intent. The editor's save
	// button is the only path that flips active=false — it always
	// sends the field explicitly.
	activate := true
	if body.Active != nil {
		activate = *body.Active
	}
	// Defence-in-depth against the editor letting a locked-id save
	// slip through. The strategies loader silently drops user files
	// targeting a locked id at runtime, so a successful 204 here
	// followed by "the file disappeared on next reload" is a
	// confusing UX. Reject up front with a clear message instead.
	if a.isLockedID(id) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("playbook id %q is locked (ships with the launcher); user-dir overrides are ignored. Edit it by changing the bundled file in system/ and rebuilding.", id))
		return
	}

	errs, err := writeUserPlaybook(r.Context(), a.opts.UserPlaybooksDir, a.opts.MCPBinaryPath, body.Type, id, body.YAML, activate, body.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"errors": errs,
		})
		return
	}
	// Type-move cleanup: if the operator changed the playbook's type
	// slot, files for this id in the old slot become orphans (the
	// loader's "same id can't span types" invariant would surface
	// them as broken on next load). Remove them now so the operator
	// doesn't have to clean up by hand. Failures here don't fail the
	// save — the new file is already on disk and the loader will
	// surface any leftovers.
	if _, cleanupErr := removeIDFromOtherTypes(a.opts.UserPlaybooksDir, id, body.Type); cleanupErr != nil {
		fmt.Fprintf(os.Stderr, "investigate: cleanup other-type files for %s: %v\n", id, cleanupErr)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePlaybook removes the user copy of <id>. If a system
// playbook with the same id exists the strategies MCP falls back to it
// on next session start; the editor's GET will then show the system
// version with source="system".
//
// DELETE /api/playbooks/{id}
func (a *apiHandlers) handleDeletePlaybook(w http.ResponseWriter, r *http.Request) {
	if a.opts.UserPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "user playbooks dir is not configured")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	if a.isLockedID(id) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("playbook id %q is locked (ships with the launcher); cannot be deleted from the user dir.", id))
		return
	}
	if err := deleteUserPlaybook(a.opts.UserPlaybooksDir, id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "no user playbook with that id")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePushPlaybookPR runs the gh + git dance to commit the supplied
// YAML to the operator's playbooks repo checkout on a fresh branch and open
// a PR. Returns the PR URL on success; structured validation errors as
// 400 (same shape as PUT); subprocess failures as 500 with the wrapped
// stderr so the UI can surface what gh / git actually said.
//
// POST /api/playbooks/{id}/push-pr  body: pushPlaybookPRRequest
func (a *apiHandlers) handlePushPlaybookPR(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "playbooks", a.opts.PlaybooksRepo) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	if a.isLockedID(id) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("playbook id %q is locked (ships with the launcher); not pushable to the upstream playbooks repo. Edit the bundled file in system/ and ship it via the launcher's release flow.", id))
		return
	}
	var body pushPlaybookPRRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res, errs, err := pushPlaybookPR(
		r.Context(),
		a.capabilities,
		a.opts.PluginPlaybooksCloneRoot,
		a.opts.PluginPlaybooksDir,
		a.opts.PlaybooksRepo,
		a.opts.MCPBinaryPath,
		id,
		body,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"errors": errs,
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handlePlaybookSyncState answers "is this playbook in sync with
// upstream?" — used by the editor to gate the push-PR button live as
// the operator types. Body is optional:
//
//	body: { yaml?: string, type?: string }
//
// When yaml is present the resolver compares it against upstream
// (the editor's "would my unsaved draft create a real diff?" question).
// When yaml is empty (or no body) the resolver falls back to the
// saved user-dir file — same answer as the list endpoint, useful for
// refreshing the editor badge after a save.
//
// POST /api/playbooks/{id}/sync-state
func (a *apiHandlers) handlePlaybookSyncState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !playbookIDPattern.MatchString(id) {
		// Validate before passing to filepath.Join inside the
		// resolver. The auth token already gates this surface to the
		// local operator, but defence-in-depth: an unvalidated id /
		// type would let a crafted request probe arbitrary YAML
		// files via the synced/local-drift response.
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid playbook id %q (allowed: alphanumeric, _-, up to 64 chars)", id))
		return
	}
	var body struct {
		YAML string `json:"yaml,omitempty"`
		Type string `json:"type,omitempty"`
	}
	// Tolerate empty body — caller wants the saved-file answer. We
	// allow the call without a Content-Length header (chunked
	// transfers report ContentLength == -1) by treating io.EOF as
	// "no body sent" rather than malformed JSON.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Type != "" && !typePattern.MatchString(body.Type) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid type %q (allowed: alphanumeric, _-, up to 64 chars)", body.Type))
		return
	}
	state := a.playbookSyncState(playbookSyncStateOpts{
		ID:           id,
		TypeName:     body.Type,
		OverrideYAML: body.YAML,
	})
	writeJSON(w, http.StatusOK, state)
}

// handleListTools returns the cached tool catalog the editor's
// suggested-call dropdown surfaces.
//
// GET /api/tools
func (a *apiHandlers) handleListTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	meta := a.metaCache.get()
	if meta == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tools": []MetaTool{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": meta.Tools})
}

// isLockedID reports whether the given id is bundled by the launcher
// (system tier). Used to gate write paths (save, delete, set-active,
// push-PR) so they fail-loud rather than silently dropping the user's
// edit on the next loader pass.
func (a *apiHandlers) isLockedID(id string) bool {
	meta := a.metaCache.get()
	if meta == nil {
		return false
	}
	mp, ok := meta.Playbooks[id]
	return ok && mp.Locked
}

// collectPlaybooks merges the embedded set (from cached dump-meta) with
// the user playbook dir and returns the resolved list. Errors reading
// the user dir are swallowed — the editor still shows the embedded
// set in that case. Each entry carries the playbook's active state,
// type slot, and unsynced/upstream provenance so the list-card UI
// can render badges + the cloud-up "drift from upstream" hint.
func (a *apiHandlers) collectPlaybooks() []playbookListItem {
	out := make(map[string]playbookListItem)

	// Pass 1: seed with playbooks the metaCache knows about — both
	// the upstream library (source="plugin", overridable) and the
	// launcher-bundled metas (source="system", locked). The loader
	// parses each YAML and uses its schema_version (defaulting to 1
	// when absent). Plugin entries with active=false are surfaced as
	// Disabled=true so the StatusPill renders the disabled state
	// without the source badge having to encode it. System entries
	// can't be disabled (they ship with the binary, the operator's
	// user dir can't tombstone them).
	if meta := a.metaCache.get(); meta != nil {
		for id, mp := range meta.Playbooks {
			summary := summarisePlaybookYAML(mp.YAML)
			disabled := summary.active != nil && !*summary.active
			// Type is the directory the file lives in upstream
			// (mp.Type, set by dump-meta from the parent dir name).
			// The YAML's `type:` field was removed when we switched
			// to directory-as-type, so summary.pbType is no longer
			// populated for the new layout.
			pbType := mp.Type
			if pbType == "" {
				pbType = summary.pbType // back-compat for any stale upstream still using the YAML field
			}
			schemaV := summary.schemaVersion
			if schemaV == 0 {
				schemaV = 1 // unset = back-compat default; matches strategies.EffectiveSchemaVersion
			}
			source := mp.Source
			if source == "" {
				source = "plugin" // back-compat for caches predating the source field
			}
			// Plugin-tier YAML IS the upstream YAML — system-bundled
			// metas have no upstream counterpart, so leave UpstreamYAML
			// empty for them. The push-PR gate uses this to decide
			// whether the operator's draft would actually diverge.
			upstreamYAML := ""
			if source == "plugin" {
				upstreamYAML = mp.YAML
			}
			// Pass-1 entries — locked (system) or plugin (upstream-only,
			// no override) — are synced by definition. The locked
			// case ships with the launcher; the plugin case is the
			// operator using upstream as-is, no drift to surface.
			// Pass 2 will overwrite this entry (calling the resolver)
			// for any id that has a user-side file.
			syncReason := "ships with the launcher"
			if !mp.Locked {
				syncReason = "using upstream as-is"
			}
			out[id] = playbookListItem{
				ID:             id,
				Symptom:        summary.symptom,
				Description:    summary.description,
				Source:         source,
				Locked:         mp.Locked,
				NodeCount:      summary.nodeCount,
				YAML:           mp.YAML,
				Disabled:       disabled,
				Type:           pbType,
				UpstreamYAML:   upstreamYAML,
				SchemaVersion:  schemaV,
				SchemaMismatch: a.schemaMismatch(schemaV),
				SyncState:      SyncState{Status: SyncStatusSynced, Reason: syncReason},
			}
		}
	}

	// Pass 2: layer user files on top. Each user file is the single
	// source-of-truth for its id; there are no sibling versions to
	// track — git history is the record of changes.
	//
	// Locked ids (system metas) are explicitly skipped: the strategies
	// loader silently drops user-dir files targeting them, so a stray
	// user file shouldn't appear in the list as an override either.
	// Operators who try to override a system meta should see only the
	// locked entry, with no UI affordance suggesting the override
	// took effect.
	groups, err := loadUserPlaybookGroups(a.opts.UserPlaybooksDir)
	if err == nil {
		for id, g := range groups {
			if id == "" || g.Type == "" {
				continue
			}
			if existing, ok := out[id]; ok && existing.Locked {
				continue
			}
			summary := summarisePlaybookYAML(g.YAML)
			source := "user"
			if _, embedded := out[id]; embedded {
				source = "override"
			}
			// Same as Pass 1: directory IS the type. g.Type is set
			// by loadUserPlaybookGroups from the parent dir name.
			pbType := g.Type
			if pbType == "" {
				pbType = summary.pbType
			}
			// Read upstream YAML once: it's needed both for the
			// editor's diff display (UpstreamYAML field below) and
			// as input to the drift classifier. Calling the
			// disk-loading playbookSyncState here would walk the
			// user dir a second time (we already have `g`) and
			// re-read this same upstream file — playbookSyncStateFor
			// is the pure classifier that takes what we've loaded.
			upstreamYAML := readUpstreamPlaybookYAML(a.opts.PluginPlaybooksDir, pbType, id)
			schemaV := summary.schemaVersion
			if schemaV == 0 {
				schemaV = 1
			}
			// Single oracle for "is this drifted?". The editor's
			// preview endpoint feeds the same classifier via
			// playbookSyncState's override path — list and editor
			// never disagree.
			syncState := playbookSyncStateFor(g.YAML, g.Active, upstreamYAML, false)
			out[id] = playbookListItem{
				ID:             id,
				Symptom:        summary.symptom,
				Description:    summary.description,
				Source:         source,
				NodeCount:      summary.nodeCount,
				YAML:           g.YAML,
				Disabled:       !g.Active,
				Type:           pbType,
				SyncState:      syncState,
				UpstreamYAML:   upstreamYAML,
				SchemaVersion:  schemaV,
				SchemaMismatch: a.schemaMismatch(schemaV),
			}
		}
	}

	items := make([]playbookListItem, 0, len(out))
	for _, v := range out {
		items = append(items, v)
	}
	return items
}

// schemaMismatch returns true when the supplied schema_version
// differs from the triagent-mcp version this launcher's metaCache was
// loaded against. Falls back to "no mismatch" when the cache wasn't
// populated (dump-meta failed at startup) — better silent than to
// flag everything as mismatched in that degraded state.
func (a *apiHandlers) schemaMismatch(playbookVersion int) bool {
	meta := a.metaCache.get()
	if meta == nil || meta.CurrentSchemaVersion == 0 {
		return false
	}
	return playbookVersion != meta.CurrentSchemaVersion
}

// summarisePlaybookYAML pulls just the fields the list view needs without
// running full validation. Malformed user files are silently skipped by
// loadUserPlaybookGroups — the operator won't see them in the list.
// TODO: surface a warning for malformed user files so operators can find
// and fix them without needing to inspect the filesystem directly.
// Active is *bool to distinguish unset (default true) from explicit
// false; embedded YAMLs that ship with active=false load as
// system-disabled.
func summarisePlaybookYAML(body string) (out struct {
	id, symptom, description, pbType string
	nodeCount                        int
	active                           *bool
	schemaVersion                    int
}) {
	var head struct {
		ID            string         `yaml:"id"`
		Symptom       string         `yaml:"symptom"`
		Description   string         `yaml:"description"`
		Active        *bool          `yaml:"active"`
		Type          string         `yaml:"type"`
		SchemaVersion int            `yaml:"schema_version"`
		Nodes         map[string]any `yaml:"nodes"`
	}
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return out
	}
	out.id = head.ID
	out.symptom = head.Symptom
	out.description = head.Description
	out.nodeCount = len(head.Nodes)
	out.active = head.Active
	out.pbType = head.Type
	out.schemaVersion = head.SchemaVersion
	return out
}

