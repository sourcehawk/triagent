package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// marshalWikiFrontmatter marshals a wikiFrontmatter struct back to YAML text
// (without trailing newline). Used by the resolve handler to rewrite the file.
func marshalWikiFrontmatter(fm wikiFrontmatter) (string, error) {
	out, err := yaml.Marshal(&fm)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// wikiProposalListItem is the per-row payload emitted by handleListWikiProposals.
// Kept small so the wiki sidenav can render lots of proposals cheaply.
type wikiProposalListItem struct {
	ProposalID string `json:"proposal_id"`
	Slug       string `json:"slug"`
	Title      string `json:"title,omitempty"`
	Status     string `json:"status,omitempty"`   // frontmatter status (resolved/open/wontfix)
	Severity   string `json:"severity,omitempty"` // frontmatter severity (sev1/sev2/sev3)
	IsNew      bool   `json:"is_new"`             // false if a vault entry with this slug already exists
	ModifiedAt string `json:"modified_at"`        // RFC3339 — file mtime so the sidenav can sort newest-first
}

// handleListWikiProposals enumerates pending wiki proposals on disk. The wiki
// sidenav uses this so an operator who tabbed out of the editor can still
// see (and re-open) a proposal that hasn't been approved or declined yet.
//
// A "pending" proposal is one whose draft markdown exists in the proposals
// dir and has no sibling resolution marker. Entity stub siblings (matching
// __entity__) are skipped — they're not proposals themselves.
//
// GET /api/wiki-proposals
func (a *apiHandlers) handleListWikiProposals(w http.ResponseWriter, r *http.Request) {
	proposals := resolveWikiProposalsPath(a.opts.WikiProposalsPath)
	entries, err := os.ReadDir(proposals)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"proposals": []wikiProposalListItem{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]wikiProposalListItem, 0, len(entries))
	const sep = "__"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		// Filename shape: <prop-id>__<slug>.md. Entity stubs sit alongside
		// at <prop-id>__entity__<type>__<name>.md — skip those; the sidenav
		// only surfaces top-level proposals.
		stem := strings.TrimSuffix(name, ".md")
		idx := strings.Index(stem, sep)
		if idx < 0 {
			continue
		}
		proposalID := stem[:idx]
		if !wikiProposalIDPattern.MatchString(proposalID) {
			continue
		}
		rest := stem[idx+len(sep):]
		if strings.HasPrefix(rest, "entity"+sep) {
			continue
		}

		// Already resolved? Skip — the sidenav is for pending proposals.
		if _, ok, _ := readWikiProposalResolution(proposals, proposalID); ok {
			continue
		}

		full := filepath.Join(proposals, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		item := wikiProposalListItem{
			ProposalID: proposalID,
			Slug:       rest,
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		}
		// Parse frontmatter for title/status/severity. Best-effort — a
		// malformed draft still gets surfaced so the operator can decline it.
		if raw, err := os.ReadFile(full); err == nil {
			if fmBytes, _, err := wikiSplitFrontmatter(raw); err == nil {
				var fm wikiFrontmatter
				if err := yaml.Unmarshal(fmBytes, &fm); err == nil {
					item.Title = fm.Title
					item.Status = fm.Status
					item.Severity = fm.Severity
				}
			}
		}
		// is_new: true if the vault has no entry for this slug yet.
		entryPath := filepath.Join(a.opts.WikiPath, "entries", rest+".md")
		if _, err := os.Stat(entryPath); errors.Is(err, os.ErrNotExist) {
			item.IsNew = true
		}
		out = append(out, item)
	}

	// Newest first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedAt > out[j].ModifiedAt
	})
	writeJSON(w, http.StatusOK, map[string]any{"proposals": out})
}

// handleGetWikiProposal returns the draft for a wiki proposal id.
// Used by the chat UI to recover the draft when the inline tool result
// is no longer in scope.
//
// GET /api/wiki-proposals/{id}
func (a *apiHandlers) handleGetWikiProposal(w http.ResponseWriter, r *http.Request) {
	proposals := resolveWikiProposalsPath(a.opts.WikiProposalsPath)
	id := r.PathValue("id")
	if !wikiProposalIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	slug, body, err := readWikiDraft(proposals, id)
	if err != nil {
		// Draft missing — surface the resolution outcome if we have
		// one so the chat-side card stops offering Approve/Decline.
		if res, ok, rerr := readWikiProposalResolution(proposals, id); rerr == nil && ok {
			payload := map[string]any{
				"proposal_id": id,
				"status":      res.Outcome,
				"at":          res.At,
			}
			if res.Outcome == "approved" {
				payload["slug"] = res.Slug
				payload["path"] = res.Path
				payload["commit"] = res.Commit
				if len(res.StubsCreated) > 0 {
					payload["stubs_created"] = res.StubsCreated
				}
			}
			writeJSON(w, http.StatusOK, payload)
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Derive is_new + base_md from the vault so a sidenav deep-link
	// (no tool-result context to fall back on) can render the diff view
	// and headline chips with the same information the chat-side card
	// had originally.
	isNew := true
	var baseMD string
	if a.opts.WikiPath != "" {
		entryPath := filepath.Join(a.opts.WikiPath, "entries", slug+".md")
		if raw, err := os.ReadFile(entryPath); err == nil {
			isNew = false
			baseMD = string(raw)
		}
	}
	out := map[string]any{
		"proposal_id": id,
		"slug":        slug,
		"new_md":      body,
		"is_new":      isNew,
		"status":      "pending",
	}
	if baseMD != "" {
		out["base_md"] = baseMD
	}
	if stubs := readWikiNewEntityStubsRaw(proposals, id); len(stubs) > 0 {
		out["new_entities"] = stubs
	}
	writeJSON(w, http.StatusOK, out)
}

// wikiNewEntityStubRaw mirrors mcp/internal/wiki.NewEntityStub for the launcher's
// GET response. Carries the raw stub markdown so the proposal card's raw view
// can render every sibling file with separators, matching what the agent's
// tool result already includes.
type wikiNewEntityStubRaw struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	RawMD       string `json:"raw_md"`
}

// readWikiNewEntityStubsRaw scans the proposals dir for entity stub siblings
// and returns each one's full markdown. Errors and malformed stubs are
// silently skipped — the existing readWikiNewEntityStubs already surfaces
// parse failures via the approve flow; this helper is purely for display.
func readWikiNewEntityStubsRaw(proposalsDir, proposalID string) []wikiNewEntityStubRaw {
	entries, err := os.ReadDir(proposalsDir)
	if err != nil {
		return nil
	}
	prefix := proposalID + "__entity__"
	var out []wikiNewEntityStubRaw
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(e.Name(), prefix), ".md")
		sep := strings.Index(inner, "__")
		if sep < 0 {
			continue
		}
		typ := inner[:sep]
		name := inner[sep+2:]
		if typ == "" || name == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proposalsDir, e.Name()))
		if err != nil {
			continue
		}
		desc, _ := parseEntityStubDescription(raw)
		out = append(out, wikiNewEntityStubRaw{
			Type:        typ,
			Name:        name,
			Description: desc,
			RawMD:       string(raw),
		})
	}
	return out
}

// approveWikiByID is the shared body of the wiki approve action: validate id,
// push the draft to the local vault, write the resolution marker, and respond
// JSON. Both the browser-facing handler and the auto/approve-proposal handler
// call it.
func (a *apiHandlers) approveWikiByID(w http.ResponseWriter, r *http.Request, proposalID string) {
	if !wikiProposalIDPattern.MatchString(proposalID) {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	proposals := resolveWikiProposalsPath(a.opts.WikiProposalsPath)
	res, validationErrs, err := pushWikiToVault(
		r.Context(),
		a.capabilities,
		a.opts.WikiCloneRoot,
		a.opts.WikiPath,
		a.opts.MCPBinaryPath,
		proposals,
		pushWikiPRRequest{ProposalID: proposalID},
	)
	if err != nil {
		// Capability failures map to 503; everything else 500.
		if !a.capabilities.Wiki.Valid {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(validationErrs) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": validationErrs})
		return
	}
	// Persist a tiny outcome marker so a re-mounted chat card knows
	// the proposal was approved. Best-effort.
	_ = writeWikiProposalResolution(proposals, proposalID, wikiProposalResolution{
		Outcome:      "approved",
		Slug:         res.Slug,
		Path:         res.Path,
		Commit:       res.Commit,
		StubsCreated: res.StubsCreated,
		At:           time.Now().UTC().Format(time.RFC3339),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"slug":          res.Slug,
		"path":          res.Path,
		"commit":        res.Commit,
		"stubs_created": res.StubsCreated,
	})
}

// handleApproveWikiProposal promotes a wiki draft to a local vault commit.
// Returns {ok, slug, path, commit, stubs_created} on success.
// No PR is opened — that is a separate explicit operator action from the
// wiki entry detail page.
//
// POST /api/wiki-proposals/{id}/approve
func (a *apiHandlers) handleApproveWikiProposal(w http.ResponseWriter, r *http.Request) {
	a.approveWikiByID(w, r, r.PathValue("id"))
}

// handleDeclineWikiProposal drops a wiki draft without promoting it.
// The frontend optionally posts { note } so the operator's pushback is
// persisted in the .resolved marker for future dispatched sub-agents
// to read. The chat-message follow-up is independent — the marker is
// the durable record, chat is the master agent's in-conversation
// context.
//
// POST /api/wiki-proposals/{id}/decline
func (a *apiHandlers) handleDeclineWikiProposal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !wikiProposalIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	// Optional body { note }. Lenient parse — legacy callers send
	// nothing and that's fine. Don't surface decode errors; the decline
	// itself is the load-bearing effect.
	var body struct {
		Note string `json:"note,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	proposals := resolveWikiProposalsPath(a.opts.WikiProposalsPath)
	if err := declineWikiViaSubprocess(r.Context(), a.opts.MCPBinaryPath, proposals, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Persist the outcome marker so a re-mounted chat card shows
	// "declined" instead of action buttons. Best-effort.
	_ = writeWikiProposalResolution(proposals, id, wikiProposalResolution{
		Outcome: "declined",
		At:      time.Now().UTC().Format(time.RFC3339),
		Note:    strings.TrimSpace(body.Note),
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleResolveWikiEntry sets status=resolved on an entry file in the
// local vault. Idempotent: already-resolved entries return ok without
// re-committing.
//
// POST /api/wiki/entries/{slug}/resolve
func (a *apiHandlers) handleResolveWikiEntry(w http.ResponseWriter, r *http.Request) {
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

	entryRel := "entries/" + slug + ".md"
	entryAbs := a.opts.WikiPath + "/" + entryRel
	entryBytes, err := os.ReadFile(entryAbs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "entry "+slug+" not found in vault")
			return
		}
		writeError(w, http.StatusInternalServerError, "read entry: "+err.Error())
		return
	}

	fmBytes, body, err := wikiSplitFrontmatter(entryBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "entry missing valid frontmatter: "+err.Error())
		return
	}
	var fm wikiFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		writeError(w, http.StatusBadRequest, "frontmatter parse: "+err.Error())
		return
	}

	// Already resolved — no-op.
	if fm.Status == "resolved" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": "", "already_resolved": true})
		return
	}

	// Refuse on dirty tree.
	if dirty, err := gitWorkingTreeDirty(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if dirty {
		writeError(w, http.StatusConflict, "wiki working tree is dirty — commit or stash before resolving")
		return
	}

	fm.Status = "resolved"
	fmOut, err := marshalWikiFrontmatter(fm)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal frontmatter: "+err.Error())
		return
	}
	newContent := "---\n" + fmOut + "---\n" + string(body)
	if err := os.WriteFile(entryAbs, []byte(newContent), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "write entry: "+err.Error())
		return
	}

	if out, err := runGit(r.Context(), a.opts.WikiPath, "add", entryRel); err != nil {
		writeError(w, http.StatusInternalServerError, "git add: "+err.Error()+" ("+out+")")
		return
	}
	commitMsg := "wiki: resolve " + slug
	if out, err := runGit(r.Context(), a.opts.WikiPath, "commit", "-m", commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, "git commit: "+err.Error()+" ("+out+")")
		return
	}
	sha, _ := runGit(r.Context(), a.opts.WikiPath, "rev-parse", "--short", "HEAD")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": strings.TrimSpace(sha)})
}

// handleDeleteWikiEntry removes an entry file from the local vault and
// commits the deletion.
//
// DELETE /api/wiki/entries/{slug}
func (a *apiHandlers) handleDeleteWikiEntry(w http.ResponseWriter, r *http.Request) {
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

	entryRel := "entries/" + slug + ".md"
	entryAbs := a.opts.WikiPath + "/" + entryRel
	if _, err := os.Stat(entryAbs); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "entry "+slug+" not found in vault")
			return
		}
		writeError(w, http.StatusInternalServerError, "stat entry: "+err.Error())
		return
	}

	// Refuse on dirty tree.
	if dirty, err := gitWorkingTreeDirty(r.Context(), a.opts.WikiPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if dirty {
		writeError(w, http.StatusConflict, "wiki working tree is dirty — commit or stash before deleting")
		return
	}

	if err := os.Remove(entryAbs); err != nil {
		writeError(w, http.StatusInternalServerError, "remove entry: "+err.Error())
		return
	}

	if out, err := runGit(r.Context(), a.opts.WikiPath, "add", entryRel); err != nil {
		writeError(w, http.StatusInternalServerError, "git add: "+err.Error()+" ("+out+")")
		return
	}
	commitMsg := "wiki: delete " + slug
	if out, err := runGit(r.Context(), a.opts.WikiPath, "commit", "-m", commitMsg); err != nil {
		writeError(w, http.StatusInternalServerError, "git commit: "+err.Error()+" ("+out+")")
		return
	}
	sha, _ := runGit(r.Context(), a.opts.WikiPath, "rev-parse", "--short", "HEAD")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": strings.TrimSpace(sha)})
}

// handleDeleteWikiEntryPR opens an upstream PR that removes the
// entry file, then deletes the file locally. Used when the operator
// chooses to delete an entry that exists upstream — once the local
// file is gone, the launcher can't easily push the deletion separately,
// so the PR has to happen in the same atomic flow.
//
// POST /api/wiki/entries/{slug}/delete-pr
// Body: {"branch": "...", "title": "...", "body": "...", "base": "..."}
// All fields optional.
func (a *apiHandlers) handleDeleteWikiEntryPR(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "wiki", a.opts.WikiRepo) {
		return
	}
	slug := r.PathValue("slug")
	if err := wikiValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body pushWikiPRRequest
	if r.Body != http.NoBody {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	body.Slug = slug

	res, validationErrs, err := pushWikiDeletePR(
		r.Context(),
		a.capabilities,
		a.opts.WikiCloneRoot,
		a.opts.WikiPath,
		a.opts.WikiRepo,
		body,
	)
	if err != nil {
		if !a.capabilities.GH.Authenticated || !a.capabilities.Wiki.Valid {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(validationErrs) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": validationErrs})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"url":    res.URL,
		"branch": res.Branch,
		"base":   res.Base,
		"slug":   res.Slug,
	})
}

// handlePushWikiPR creates a PR in the upstream wiki repo for an
// already-committed entry in the local vault. Triggered explicitly
// by the operator from the wiki entry detail page.
//
// POST /api/wiki/entries/{slug}/push-pr
// Body: {"branch": "...", "title": "...", "body": "...", "base": "..."}
// All fields are optional; the launcher derives sensible defaults.
func (a *apiHandlers) handlePushWikiPR(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "wiki", a.opts.WikiRepo) {
		return
	}
	slug := r.PathValue("slug")
	if err := wikiValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body pushWikiPRRequest
	if r.Body != http.NoBody {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	body.Slug = slug

	res, validationErrs, err := pushWikiPR(
		r.Context(),
		a.capabilities,
		a.opts.WikiCloneRoot,
		a.opts.WikiPath,
		a.opts.WikiRepo,
		body,
	)
	if err != nil {
		if !a.capabilities.GH.Authenticated || !a.capabilities.Wiki.Valid {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(validationErrs) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": validationErrs})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"url":    res.URL,
		"branch": res.Branch,
		"base":   res.Base,
		"slug":   res.Slug,
	})
}

// wikiProposalResolution is the outcome marker we persist when a wiki
// proposal is approved or declined. Stored at
// <proposalsDir>/.resolved/<proposalID>.json so the chat-side card
// can render the actual outcome on re-mount instead of stale
// Approve/Decline buttons.
type wikiProposalResolution struct {
	Outcome      string   `json:"outcome"` // "approved" or "declined"
	Slug         string   `json:"slug,omitempty"`
	Path         string   `json:"path,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	StubsCreated []string `json:"stubs_created,omitempty"`
	At           string   `json:"at"`
	// Note carries an optional operator-supplied comment (typically the
	// pushback that accompanies a decline). Symmetric to
	// proposalResolution.Note on the playbook side; surfaced by the
	// strategies MCP's list_proposals tool on a subsequent dispatch.
	Note string `json:"note,omitempty"`
}

func wikiProposalResolutionPath(proposalsDir, proposalID string) string {
	return filepath.Join(proposalsDir, ".resolved", proposalID+".json")
}

func writeWikiProposalResolution(proposalsDir, proposalID string, r wikiProposalResolution) error {
	p := wikiProposalResolutionPath(proposalsDir, proposalID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func readWikiProposalResolution(proposalsDir, proposalID string) (wikiProposalResolution, bool, error) {
	data, err := os.ReadFile(wikiProposalResolutionPath(proposalsDir, proposalID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return wikiProposalResolution{}, false, nil
		}
		return wikiProposalResolution{}, false, err
	}
	var r wikiProposalResolution
	if err := json.Unmarshal(data, &r); err != nil {
		return wikiProposalResolution{}, false, err
	}
	return r, true, nil
}
