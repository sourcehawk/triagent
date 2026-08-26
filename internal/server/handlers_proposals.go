package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/strategies"
	"gopkg.in/yaml.v3"
)

// playbookProposalListItem is the per-row payload emitted by
// handleListPlaybookProposals. Kept small so the playbook sidenav can
// render lots of proposals cheaply. Mirrors wikiProposalListItem in
// spirit — slug → playbook_id, plus the type subdirectory the draft
// was filed under.
type playbookProposalListItem struct {
	ProposalID  string `json:"proposal_id"`
	PlaybookID  string `json:"playbook_id"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	IsNew       bool   `json:"is_new"`
	ModifiedAt  string `json:"modified_at"`
}

// handleListPlaybookProposals enumerates pending playbook proposals on
// disk. The playbooks sidenav uses this so an operator who tabbed out of
// the editor can still see (and re-open) a proposal that hasn't been
// approved or declined yet.
//
// A "pending" proposal is one whose draft yaml exists in the proposals
// dir and has no sibling resolution marker. Drafts live at
// <UserPlaybooksDir>/proposals/<type>/<playbook_id>__<proposal_id>.yaml
// — we walk type subdirs and skip the .resolved/ ledger.
//
// GET /api/playbook-proposals
func (a *apiHandlers) handleListPlaybookProposals(w http.ResponseWriter, r *http.Request) {
	if a.opts.UserPlaybooksDir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"proposals": []playbookProposalListItem{}})
		return
	}
	proposalsRoot := filepath.Join(a.opts.UserPlaybooksDir, "proposals")
	typeDirs, err := os.ReadDir(proposalsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"proposals": []playbookProposalListItem{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Snapshot the user-playbook id set once so the per-row is_new check
	// is a map lookup rather than another walk of the user dir.
	userIDs := map[string]struct{}{}
	if groups, err := loadUserPlaybookGroups(a.opts.UserPlaybooksDir); err == nil {
		for id := range groups {
			userIDs[id] = struct{}{}
		}
	}

	out := make([]playbookProposalListItem, 0)
	const sep = "__"
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		typeName := td.Name()
		// Skip the resolved ledger and any other dotfile dir.
		if !typePattern.MatchString(typeName) {
			continue
		}
		typeDir := filepath.Join(proposalsRoot, typeName)
		entries, err := os.ReadDir(typeDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			stem := strings.TrimSuffix(e.Name(), ".yaml")
			idx := strings.LastIndex(stem, sep)
			if idx < 0 {
				continue
			}
			playbookID := stem[:idx]
			proposalID := stem[idx+len(sep):]
			if !playbookIDPattern.MatchString(playbookID) || !proposalIDPattern.MatchString(proposalID) {
				continue
			}

			// Already resolved? Skip — the sidenav is for pending proposals.
			if _, ok, _ := readProposalResolution(a.opts.UserPlaybooksDir, proposalID); ok {
				continue
			}

			full := filepath.Join(typeDir, e.Name())
			info, err := e.Info()
			if err != nil {
				continue
			}
			item := playbookProposalListItem{
				ProposalID: proposalID,
				PlaybookID: playbookID,
				Type:       typeName,
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			}
			// Best-effort: pull a short description out of the YAML so the
			// sidenav row has a human label. A malformed draft still gets
			// surfaced so the operator can decline it.
			if raw, err := os.ReadFile(full); err == nil {
				var head struct {
					Description string `yaml:"description"`
					Symptom     string `yaml:"symptom"`
				}
				if err := yaml.Unmarshal(raw, &head); err == nil {
					if head.Description != "" {
						item.Description = head.Description
					} else if head.Symptom != "" {
						item.Description = head.Symptom
					}
				}
			}
			if _, ok := userIDs[playbookID]; !ok {
				item.IsNew = true
			}
			out = append(out, item)
		}
	}

	// Newest first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedAt > out[j].ModifiedAt
	})
	writeJSON(w, http.StatusOK, map[string]any{"proposals": out})
}

// handleGetProposal returns the draft for a proposal id along with
// the currently-loaded base YAML (if any) and the target version.
// Used by the chat UI's diff card on a tab refresh — the inline payload
// in the playbook_proposal_draft tool result is the primary source, this
// endpoint is the recovery path.
//
// GET /api/playbook-proposals/{id}
func (a *apiHandlers) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.opts.UserPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "user playbooks dir is not configured")
		return
	}
	proposalID := r.PathValue("id")
	if !proposalIDPattern.MatchString(proposalID) {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	playbookID, body, err := readDraftFromDir(a.opts.UserPlaybooksDir, proposalID)
	if err != nil {
		// Draft missing — consult the resolution ledger so chat
		// surfaces re-mounting after approve/decline render the
		// outcome instead of a stale Approve/Decline pair.
		if res, ok, rerr := readProposalResolution(a.opts.UserPlaybooksDir, proposalID); rerr == nil && ok {
			payload := map[string]any{
				"proposal_id": proposalID,
				"status":      res.Outcome,
				"at":          res.At,
			}
			if res.Outcome == "approved" {
				payload["id"] = res.ID
				payload["type"] = res.Type
				payload["version"] = res.Version
				payload["activated"] = res.Activated
			}
			writeJSON(w, http.StatusOK, payload)
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Determine base YAML from the currently-loaded playbook set so
	// the diff renders against what's actually live (plugin, system,
	// or user override).
	baseYAML, err := loadBaseForID(a.opts, playbookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proposal_id": proposalID,
		"playbook_id": playbookID,
		"base_yaml":   baseYAML,
		"new_yaml":    body,
		"status":      "pending",
	})
}

// approvePlaybookByID is the shared body of the approve action: validate id,
// run the promote subprocess, write the resolution marker, and respond JSON.
// Both the browser-facing handler and the auto/approve-proposal handler call it.
func (a *apiHandlers) approvePlaybookByID(w http.ResponseWriter, r *http.Request, proposalID string) {
	if a.opts.UserPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "user playbooks dir is not configured")
		return
	}
	if !proposalIDPattern.MatchString(proposalID) {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	id, typeName, version, activated, validationErrs, err := promoteProposalViaSubprocess(r.Context(), a.opts.MCPBinaryPath, a.opts.UserPlaybooksDir, proposalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(validationErrs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"errors": validationErrs,
		})
		return
	}
	// Persist a tiny outcome marker so the chat-side card knows the
	// proposal was approved when it re-mounts on a page reload. Not
	// fatal if the write fails — the operator already saw success.
	_ = writeProposalResolution(a.opts.UserPlaybooksDir, proposalID, proposalResolution{
		Outcome:   "approved",
		ID:        id,
		Type:      typeName,
		Version:   version,
		Activated: activated,
		At:        time.Now().UTC().Format(time.RFC3339),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"id":        id,
		"type":      typeName,
		"version":   version,
		"activated": activated,
	})
}

// handleApproveProposal promotes a draft to a versioned file and bumps
// the active pointer. Returns {ok, id, version, activated} on success.
//
// POST /api/playbook-proposals/{id}/approve
// Body is ignored — approve always activates. Decline is the only
// alternative.
func (a *apiHandlers) handleApproveProposal(w http.ResponseWriter, r *http.Request) {
	a.approvePlaybookByID(w, r, r.PathValue("id"))
}

// handleDeclineProposal drops a draft without promoting it. The
// frontend optionally posts { note } so the operator's pushback is
// persisted in the .resolved marker for list_proposals to surface to
// the next dispatched sub-agent. The note is independent of the chat
// transcript — the marker is the durable record the strategies MCP
// reads on subsequent walks; the chat transcript is the master agent's
// in-conversation context.
//
// POST /api/playbook-proposals/{id}/decline
func (a *apiHandlers) handleDeclineProposal(w http.ResponseWriter, r *http.Request) {
	if a.opts.UserPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "user playbooks dir is not configured")
		return
	}
	proposalID := r.PathValue("id")
	if !proposalIDPattern.MatchString(proposalID) {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return
	}
	// Optional body { note }. Parse leniently — an empty body or invalid
	// JSON is fine (legacy callers send nothing); just decline without a
	// note. Don't surface decode errors as 4xx; the decline itself is
	// the important effect.
	var body struct {
		Note string `json:"note,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if err := deleteProposalViaSubprocess(r.Context(), a.opts.MCPBinaryPath, a.opts.UserPlaybooksDir, proposalID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Persist the outcome marker so a re-mounted chat card shows
	// "declined" instead of action buttons. Best-effort.
	_ = writeProposalResolution(a.opts.UserPlaybooksDir, proposalID, proposalResolution{
		Outcome: "declined",
		At:      time.Now().UTC().Format(time.RFC3339),
		Note:    strings.TrimSpace(body.Note),
	})
	w.WriteHeader(http.StatusNoContent)
}

// proposalIDPattern mirrors the strategies-side guard. Defined here
// (instead of imported) because the launcher is a separate Go module
// and we want defence-in-depth at the URL boundary regardless of what
// the triagent-mcp subprocess accepts.
var proposalIDPattern = playbookIDPattern

// readDraftFromDir reads the draft yaml directly. Skips the triagent-mcp
// subprocess for read-only inspection — the proposal file format is
// stable enough that the launcher can decode it without help. Falls
// back to the subprocess for promotion (which needs the validator).
//
// Drafts are filed by the strategies MCP under a per-type subdir
// (<dir>/proposals/<type>/<playbook_id>__<proposal_id>.yaml), so we
// walk every subdirectory of proposals/ rather than scanning the
// flat root. Mirrors strategies' LoadProposalDraft.
func readDraftFromDir(dir, proposalID string) (playbookID, body string, err error) {
	proposalsRoot := filepath.Join(dir, "proposals")
	typeDirs, err := os.ReadDir(proposalsRoot)
	if err != nil {
		return "", "", err
	}
	suffix := "__" + proposalID + ".yaml"
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		typeDir := filepath.Join(proposalsRoot, td.Name())
		entries, err := os.ReadDir(typeDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(typeDir, e.Name()))
			if err != nil {
				return "", "", err
			}
			id := strings.TrimSuffix(e.Name(), suffix)
			return id, string(data), nil
		}
	}
	return "", "", os.ErrNotExist
}

// loadBaseForID returns the canonical YAML of the playbook the running
// strategies MCP would resolve for id: the same plugin → user → system
// merge the MCP loads at startup (user overrides plugin; the locked
// system tier wins over both), re-rendered through the shared
// serialiser so the proposal diff surfaces semantic deltas rather than
// on-disk formatting. Returns "" for a brand-new id, which the diff card
// renders as a new playbook; a set that fails to load is an error, not an
// empty base, so an update is never mislabelled as new.
func loadBaseForID(opts Options, id string) (string, error) {
	books, err := strategies.LoadPlaybooksFrom(opts.PluginPlaybooksDir, opts.SystemPlaybooksDir, opts.UserPlaybooksDir)
	if err != nil {
		return "", fmt.Errorf("load playbook set: %w", err)
	}
	pb, ok := books[id]
	if !ok {
		return "", nil
	}
	rendered, err := strategies.RenderPlaybookYAML(pb)
	if err != nil {
		return "", fmt.Errorf("render base playbook %s: %w", id, err)
	}
	return rendered, nil
}

// proposalResolution is the outcome marker we persist when a playbook
// proposal is approved or declined. Stored at
// <UserPlaybooksDir>/proposals/.resolved/<proposalID>.json so chat
// surfaces re-mounting after the proposal is gone (e.g. on page
// reload) can render the actual outcome instead of stale
// Approve/Decline buttons that would 404 on click.
type proposalResolution struct {
	Outcome   string `json:"outcome"` // "approved" or "declined"
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Version   string `json:"version,omitempty"`
	Activated bool   `json:"activated,omitempty"`
	At        string `json:"at"`
	// Note carries an optional operator-supplied comment (typically the
	// pushback that accompanies a decline). Surfaced by list_proposals
	// so dispatched sub-agents reading current proposal state can adjust
	// without re-submitting the same shape that was just declined.
	Note string `json:"note,omitempty"`
}

// proposalResolutionPath returns the on-disk location of the marker
// for a given proposal id.
func proposalResolutionPath(dir, proposalID string) string {
	return filepath.Join(dir, "proposals", ".resolved", proposalID+".json")
}

// writeProposalResolution persists the marker. Creates the .resolved
// dir on demand. Best-effort callers may ignore the error — the
// resolution itself already succeeded by the time we get here.
func writeProposalResolution(dir, proposalID string, r proposalResolution) error {
	p := proposalResolutionPath(dir, proposalID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// readProposalResolution returns (marker, true, nil) when the marker
// exists, (zero, false, nil) when it doesn't, and (zero, false, err)
// for read/parse errors so callers can distinguish "no record" from
// "record unreadable".
func readProposalResolution(dir, proposalID string) (proposalResolution, bool, error) {
	data, err := os.ReadFile(proposalResolutionPath(dir, proposalID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return proposalResolution{}, false, nil
		}
		return proposalResolution{}, false, err
	}
	var r proposalResolution
	if err := json.Unmarshal(data, &r); err != nil {
		return proposalResolution{}, false, err
	}
	return r, true, nil
}

