package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/wiki"
	"gopkg.in/yaml.v3"
)

// ── GET /api/wiki/stats ───────────────────────────────────────────────────────

func (a *apiHandlers) handleWikiStats(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		wikiWriteNotConfigured(w)
		return
	}
	vaultPath := a.opts.WikiPath

	paths, err := wiki.ListEntries(vaultPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entityCounts := map[string]int{"service": 0, "error": 0, "symptom": 0, "component": 0}
	for _, t := range []string{"service", "error", "symptom", "component"} {
		refs, err := wiki.ListEntities(vaultPath, t)
		if err == nil {
			entityCounts[t] = len(refs)
		}
	}

	entryCount := 0
	lastPromotedAt := ""
	for _, p := range paths {
		raw, err := wiki.ReadNote(vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, _, err := wiki.SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm wiki.Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			continue
		}
		entryCount++
		if fm.Date > lastPromotedAt {
			lastPromotedAt = fm.Date
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entry_count":          entryCount,
		"entity_count_by_type": entityCounts,
		"last_promoted_at":     lastPromotedAt,
	})
}

// ── GET /api/wiki/entries ─────────────────────────────────────────────────────

func (a *apiHandlers) handleWikiListEntries(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		wikiWriteNotConfigured(w)
		return
	}
	vaultPath := a.opts.WikiPath

	limit := 20
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if n, err := strconv.Atoi(oStr); err == nil && n >= 0 {
			offset = n
		}
	}

	queryLower := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filters := wikiFiltersFromQuery(r)
	// unsynced=true narrows the hit set to entries whose local file
	// diverges from origin/HEAD (or isn't on it yet). Mirrors the
	// "not in upstream" predicate the list / row icon already use:
	// anything not in unsyncedSet renders as synced, so the same
	// classification drives both rendering and filtering.
	onlyUnsynced := r.URL.Query().Get("unsynced") == "true"

	paths, err := wiki.ListEntries(vaultPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type hit struct {
		ID          string           `json:"id"`
		Path        string           `json:"path"`
		Title       string           `json:"title"`
		Snippet     string           `json:"snippet,omitempty"`
		Frontmatter wiki.Frontmatter `json:"frontmatter"`
		Score       int              `json:"score"`
		// SyncState is the resolver's drift answer for this file.
		// IncidentRow renders the cloud-up icon when status is
		// "local-drift"; tooltip pulls from the resolver's reason
		// string so list / detail / editor agree on the wording.
		SyncState SyncState `json:"syncState"`
	}

	// One-shot diff against origin/HEAD so each hit can flag whether
	// its file diverges from upstream. Failure (no remote / never
	// fetched / detached HEAD) returns an empty set — no unsynced
	// markers, which is the safe default.
	unsyncedSet := unsyncedWikiFiles(r.Context(), vaultPath)

	var hits []hit
	for _, p := range paths {
		raw, err := wiki.ReadNote(vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, body, err := wiki.SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm wiki.Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			continue
		}
		if !wikiMatchesFilters(fm, filters) {
			continue
		}
		if onlyUnsynced && !unsyncedSet[p] {
			continue
		}
		score := 0
		snippet := ""
		if queryLower != "" {
			titleHits := strings.Count(strings.ToLower(fm.Title), queryLower)
			bodyHits := strings.Count(strings.ToLower(string(body)), queryLower)
			score = 5*titleHits + bodyHits
			if score == 0 {
				continue
			}
			snippet = wikiMakeSnippet(string(body), queryLower)
		}
		hits = append(hits, hit{
			ID:          fm.ID,
			Path:        p,
			Title:       fm.Title,
			Snippet:     snippet,
			Frontmatter: fm,
			Score:       score,
			SyncState:   wikiSyncStateFor(unsyncedSet[p]),
		})
	}

	// Sort: higher score first; ties by date desc (newer first).
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Frontmatter.Date > hits[j].Frontmatter.Date
	})

	// Total before slicing — drives the frontend's pagination UI.
	total := len(hits)

	// Slice [offset, offset+limit). Bounds-safe.
	if offset >= total {
		hits = nil
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		hits = hits[offset:end]
	}
	if hits == nil {
		hits = []hit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":   hits,
		"total":  total,
		"offset": offset,
		"limit":  limit,
	})
}

// ── GET /api/wiki/entries/{slug} ─────────────────────────────────────────────

func (a *apiHandlers) handleWikiGetEntry(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		wikiWriteNotConfigured(w)
		return
	}
	vaultPath := a.opts.WikiPath
	slug := r.PathValue("slug")
	if err := wikiValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	note, err := wiki.ReadEntry(vaultPath, slug)
	if err != nil {
		// File-on-disk is missing — this is the normal state for a freshly-
		// created backfill: the modal made an editor session, the agent
		// hasn't drafted yet, and pushWikiToVault hasn't materialised the
		// file. Return a synthetic empty entry so the editor mounts cleanly
		// at /wiki/entries/?slug=<slug> without a 404 in the network tab
		// and without the frontend needing a session-aware recovery path.
		// Other failures (parse error on an existing-but-malformed file)
		// keep the 404 with the underlying message.
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusOK, map[string]any{
				"slug": slug,
				"path": filepath.Join("entries", slug+".md"),
				"frontmatter": wiki.Frontmatter{
					SchemaVersion: 1,
					ID:            slug,
					Date:          time.Now().UTC().Format("2006-01-02"),
					Status:        "open",
					Services:      []string{},
					Errors:        []string{},
					Symptoms:      []string{},
				},
				"markdown":        "",
				"syncState":       wikiSyncStateFor(false),
				"exists_upstream": false,
				"is_stub":         true,
			})
			return
		}
		writeError(w, http.StatusNotFound, "entry not found: "+err.Error())
		return
	}

	syncState := wikiSyncStateFor(unsyncedWikiFiles(r.Context(), vaultPath)[note.Path])
	// existsUpstream drives the wiki detail view's delete confirmation:
	// true → "this will open a PR upstream to delete the wiki"
	// false → "this will delete the wiki from local storage"
	existsUpstream := wikiFileOnOrigin(r.Context(), vaultPath, note.Path)

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":            note.Frontmatter.ID,
		"path":            note.Path,
		"frontmatter":     note.Frontmatter,
		"markdown":        note.Body,
		"syncState":       syncState,
		"exists_upstream": existsUpstream,
	})
}

// unsyncedWikiFiles returns the set of vault-relative paths whose
// content differs between origin/HEAD and the local HEAD. Used to
// flag entries (and entity stubs) whose local commits haven't been
// pushed yet — the cloud-with-up-arrow icon next to the slug mirrors
// the playbook list's pattern.
//
// Returns an empty map on any failure (no remote, never fetched,
// detached HEAD, etc.) — a missing diff signal is preferable to a
// false alarm. The wiki-upstream sync header surfaces the underlying
// problem if there is one.
func unsyncedWikiFiles(ctx context.Context, vaultPath string) map[string]bool {
	out := make(map[string]bool)
	if vaultPath == "" {
		return out
	}
	// `git diff --name-only origin/HEAD HEAD` returns paths that differ
	// in either direction. We don't restrict to a subdir — both
	// entries/ and entities/ paths flow through here so the same set
	// can be queried from multiple call sites.
	res, err := runGitCapture(ctx, vaultPath, "diff", "--name-only", "origin/HEAD", "HEAD")
	if err != nil {
		// Some repos don't expose origin/HEAD as a symbolic ref; try
		// origin/main as a fallback before giving up.
		res, err = runGitCapture(ctx, vaultPath, "diff", "--name-only", "origin/main", "HEAD")
		if err != nil {
			return out
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(res), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out[line] = true
		}
	}
	return out
}

// ── GET /api/wiki/correlated-sessions ────────────────────────────────────────
//
// Returns the set of investigation session ids referenced by any wiki
// entry's frontmatter.links.investigation URL. The wiki home page uses
// this to filter the local investigations list down to the ones that
// don't yet have a wiki entry — those become candidates for the
// "create wiki" entry-point.

// investigationSessionFromURL extracts the `id=<uuid>` query parameter from a
// `links.investigation` URL. Returns "" when the URL is empty / malformed /
// missing the id.
var investigationSessionFromURL = regexp.MustCompile(`[?&]id=([0-9a-fA-F-]{36})`)

func (a *apiHandlers) handleWikiCorrelatedSessions(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		wikiWriteNotConfigured(w)
		return
	}
	vaultPath := a.opts.WikiPath

	paths, err := wiki.ListEntries(vaultPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	seen := make(map[string]bool)
	for _, p := range paths {
		raw, err := wiki.ReadNote(vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, _, err := wiki.SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm wiki.Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			continue
		}
		inv := strings.TrimSpace(fm.Links.Investigation)
		if inv == "" {
			continue
		}
		m := investigationSessionFromURL.FindStringSubmatch(inv)
		if m == nil {
			continue
		}
		seen[m[1]] = true
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	writeJSON(w, http.StatusOK, map[string]any{"session_ids": out})
}

// ── GET /api/wiki/entities ────────────────────────────────────────────────────

func (a *apiHandlers) handleWikiListEntities(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		wikiWriteNotConfigured(w)
		return
	}
	vaultPath := a.opts.WikiPath
	typeFilter := r.URL.Query().Get("type")

	refs, err := wiki.ListEntities(vaultPath, typeFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Compute incident counts in one pass.
	counts := make(map[string]int)
	paths, _ := wiki.ListEntries(vaultPath)
	for _, p := range paths {
		raw, err := wiki.ReadNote(vaultPath, p)
		if err != nil {
			continue
		}
		fmBytes, _, err := wiki.SplitFrontmatter(raw)
		if err != nil {
			continue
		}
		var fm wiki.Frontmatter
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			continue
		}
		for _, n := range fm.Services {
			counts["service:"+n]++
		}
		for _, n := range fm.Errors {
			counts["error:"+n]++
		}
		for _, n := range fm.Symptoms {
			counts["symptom:"+n]++
		}
	}

	type entityWithCount struct {
		wiki.EntityRef
		EntryCount int `json:"entry_count"`
	}

	entities := make([]entityWithCount, 0, len(refs))
	for _, r := range refs {
		entities = append(entities, entityWithCount{
			EntityRef:  r,
			EntryCount: counts[r.Type+":"+r.Name],
		})
	}

	// Stable order: type then name.
	sort.SliceStable(entities, func(i, j int) bool {
		if entities[i].Type != entities[j].Type {
			return entities[i].Type < entities[j].Type
		}
		return entities[i].Name < entities[j].Name
	})

	writeJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

// ── GET /api/wiki/entities/{type}/{name} ─────────────────────────────────────

var entityNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (a *apiHandlers) handleWikiGetEntity(w http.ResponseWriter, r *http.Request) {
	if !a.capabilities.Wiki.Valid {
		wikiWriteNotConfigured(w)
		return
	}
	vaultPath := a.opts.WikiPath

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

	dir := wiki.TypeToDir(entityType)
	relPath := filepath.Join("entities", dir, entityName+".md")
	raw, err := wiki.ReadNote(vaultPath, relPath)
	if err != nil {
		// Same synthetic-stub treatment as entries: a missing file is the
		// "fresh entity, agent will draft" state; the editor renders an
		// empty form without a 404 in the network tab. Backlinks stay
		// empty because no other entries reference this entity yet.
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusOK, map[string]any{
				"type": entityType,
				"name": entityName,
				"path": relPath,
				"frontmatter": wiki.EntityFrontmatter{
					SchemaVersion: 1,
					Type:          entityType,
					Name:          entityName,
					Created:       time.Now().UTC().Format("2006-01-02"),
				},
				"markdown":  "",
				"backlinks": []any{},
				"syncState": wikiSyncStateFor(false),
				"is_stub":   true,
			})
			return
		}
		writeError(w, http.StatusNotFound, "entity not found: "+err.Error())
		return
	}

	fmBytes, bodyBytes, err := wiki.SplitFrontmatter(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse entity: "+err.Error())
		return
	}
	var ef wiki.EntityFrontmatter
	if err := yaml.Unmarshal(fmBytes, &ef); err != nil {
		writeError(w, http.StatusInternalServerError, "parse entity frontmatter: "+err.Error())
		return
	}

	// Compute backlinks: incidents that reference this entity in frontmatter.
	type backlink struct {
		ID       string `json:"id"`
		Path     string `json:"path"`
		Title    string `json:"title"`
		Date     string `json:"date"`
		Severity string `json:"severity"`
		Status   string `json:"status"`
	}
	var backlinks []backlink

	paths, _ := wiki.ListEntries(vaultPath)
	for _, p := range paths {
		incRaw, err := wiki.ReadNote(vaultPath, p)
		if err != nil {
			continue
		}
		incFMBytes, _, err := wiki.SplitFrontmatter(incRaw)
		if err != nil {
			continue
		}
		var fm wiki.Frontmatter
		if err := yaml.Unmarshal(incFMBytes, &fm); err != nil {
			continue
		}
		referenced := false
		switch entityType {
		case "service":
			referenced = wikiContains(fm.Services, entityName)
		case "error":
			referenced = wikiContains(fm.Errors, entityName)
		case "symptom":
			referenced = wikiContains(fm.Symptoms, entityName)
		case "component":
			// components aren't directly in frontmatter; skip for now
		}
		if referenced {
			backlinks = append(backlinks, backlink{
				ID:       fm.ID,
				Path:     p,
				Title:    fm.Title,
				Date:     fm.Date,
				Severity: fm.Severity,
				Status:   fm.Status,
			})
		}
	}
	if backlinks == nil {
		backlinks = []backlink{}
	}

	syncState := wikiSyncStateFor(unsyncedWikiFiles(r.Context(), vaultPath)[relPath])

	writeJSON(w, http.StatusOK, map[string]any{
		"name":        entityName,
		"type":        entityType,
		"path":        relPath,
		"frontmatter": ef,
		"markdown":    string(bodyBytes),
		"backlinks":   backlinks,
		"syncState":   syncState,
	})
}

// ── helpers ──────────────────────────────────────────────────────────────────

// wikiWriteNotConfigured writes a consistent 503 for unconfigured wiki vault.
func wikiWriteNotConfigured(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	// We can't call writeJSON here because it also calls WriteHeader, so write
	// the JSON body directly after the header is already set.
	_, _ = w.Write([]byte(`{"ok":false,"error":"wiki vault not configured"}`))
}

type wikiSearchFilters struct {
	Services []string
	Errors   []string
	Symptoms []string
	Severity []string
	Status   []string
}

func wikiFiltersFromQuery(r *http.Request) wikiSearchFilters {
	splitCSV := func(s string) []string {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		out := parts[:0]
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return wikiSearchFilters{
		Services: splitCSV(r.URL.Query().Get("services")),
		Errors:   splitCSV(r.URL.Query().Get("errors")),
		Symptoms: splitCSV(r.URL.Query().Get("symptoms")),
		Severity: splitCSV(r.URL.Query().Get("severity")),
		Status:   splitCSV(r.URL.Query().Get("status")),
	}
}

func wikiMatchesFilters(fm wiki.Frontmatter, f wikiSearchFilters) bool {
	if len(f.Services) > 0 && !wikiAnyOverlap(fm.Services, f.Services) {
		return false
	}
	if len(f.Errors) > 0 && !wikiAnyOverlap(fm.Errors, f.Errors) {
		return false
	}
	if len(f.Symptoms) > 0 && !wikiAnyOverlap(fm.Symptoms, f.Symptoms) {
		return false
	}
	if len(f.Severity) > 0 && !wikiContains(f.Severity, fm.Severity) {
		return false
	}
	if len(f.Status) > 0 && !wikiContains(f.Status, fm.Status) {
		return false
	}
	return true
}

func wikiAnyOverlap(a, b []string) bool {
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if set[y] {
			return true
		}
	}
	return false
}

func wikiContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func wikiMakeSnippet(body, queryLower string) string {
	lower := strings.ToLower(body)
	idx := strings.Index(lower, queryLower)
	if idx < 0 {
		return ""
	}
	start := idx - 60
	if start < 0 {
		start = 0
	}
	end := idx + len(queryLower) + 60
	if end > len(body) {
		end = len(body)
	}
	out := body[start:end]
	out = strings.ReplaceAll(out, "\n", " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(body) {
		out = out + "…"
	}
	return out
}
