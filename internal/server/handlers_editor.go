package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sort"

	"github.com/sourcehawk/triagent/internal/editor"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/prompts"
)

// editorSessionRequest is the new {subject, sources} shape. Pre-1.0
// codebase, so the legacy {playbook_id, version} body is rejected — the
// frontend was migrated in lockstep.
type editorSessionRequest struct {
	Subject *subjectPayload `json:"subject"`
	Sources *sourcesPayload `json:"sources,omitempty"`
}

type subjectPayload struct {
	Kind     string             `json:"kind"` // "playbook" | "wiki"
	Playbook *playbookSubjectIn `json:"playbook,omitempty"`
	Wiki     *wikiSubjectIn     `json:"wiki,omitempty"`
}

type playbookSubjectIn struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// IsNew + YAML are set for the "+ new playbook" flow, where the
	// playbook hasn't been saved to disk yet. When IsNew is true the
	// resolver skips the file-system lookup and seeds the agent with
	// the inline YAML (the operator's in-progress draft) instead of
	// failing with "playbook not found".
	IsNew bool   `json:"isNew,omitempty"`
	YAML  string `json:"yaml,omitempty"`
	Type  string `json:"type,omitempty"`
}

type wikiSubjectIn struct {
	Kind string `json:"kind"` // "entry" | "entity"
	ID   string `json:"id"`   // "inc-12345-broker-ooms" or "service/zeebe-broker"
}

type sourcesPayload struct {
	InvestigationID  string `json:"investigationID,omitempty"`
	IncidentioURL    string `json:"incidentioURL,omitempty"`
	SlackChannelID   string `json:"slackChannelID,omitempty"`
	SlackChannelName string `json:"slackChannelName,omitempty"`
	SlackSinceUnix   int64  `json:"slackSinceUnix,omitempty"`
}

// handleCreateOrResumeEditorSession is the single entry point for the
// drawer. Body shape:
//
//	{"subject": {"kind":"playbook","playbook":{"id":"…","version":"…"}}}
//	{"subject": {"kind":"wiki","wiki":{"kind":"entry","id":"inc-12345-broker-ooms"}},
//	 "sources": {"incidentioURL":"…","slackChannelID":"…", …}}
//
// If a live session for this subject's Key() already exists, return it
// as-is — that's the "drawer collapsed and reopened" path. Otherwise
// build the per-session MCP config (which conditionally includes the
// slack / incidentio servers when their tokens are connected and the
// sources are non-empty), register a fresh editor.Session, and Start it.
//
// POST /api/editor-sessions
func (a *apiHandlers) handleCreateOrResumeEditorSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	var body editorSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	subject, err := a.resolveSubject(body.Subject)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if existing := a.editorMgr.Existing(subject); existing != nil {
		writeJSON(w, http.StatusOK, existing.Snapshot())
		return
	}

	// Resolve sources before allocating any disk so a bad URL or a
	// missing token surfaces as 400 cheaply.
	sources, err := a.resolveSources(body.Sources)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Wiki entry backfill: when the operator didn't paste an explicit
	// incident.io URL but the wiki id encodes one (the standard
	// `INC-<n>-<slug>` shape), derive the ref so the triagent-incidentio MCP
	// gets wired and the agent can pull incident context. Mirrors the
	// homepage modal's "pull from incident.io" toggle without requiring
	// the operator to paste the URL by hand for every backfill.
	if sources.IncidentioRef == "" {
		if wiki, ok := subject.(editor.WikiSubject); ok && wiki.Kind == "entry" {
			if ref := editor.DeriveIncidentioRefFromSlug(wiki.ID); ref != "" {
				// Only auto-wire when a token is already linked —
				// otherwise the existing HasIncidentio() branch below
				// would fail the session for operators who haven't
				// connected incident.io, which would regress sessions
				// that work today without it.
				if tok, _ := a.connections.GetIncidentioToken(); tok != "" {
					sources.IncidentioRef = ref
				}
			}
		}
	}

	id, err := editorNewID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := filepath.Join(a.editorMgr.Root(), "editor-"+id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create session dir: %v", err))
		return
	}

	linked, err := a.resolvedLinkedRepos()
	if err != nil {
		_ = os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve linked repos: %v", err))
		return
	}

	mcpInputs := preflight.EditorMCPInputs{
		Dir:                dir,
		MCPBin:             a.opts.MCPBinaryPath,
		UserPlaybooksDir:   a.opts.UserPlaybooksDir,
		PluginPlaybooksDir: a.opts.PluginPlaybooksDir,
		SystemPlaybooksDir: a.opts.SystemPlaybooksDir,
		LinkedRepos:        linked,
		GitCacheDir:        a.opts.GitCacheDir,
		TelemetryURL:       a.telemetryURL,
		TraceID:            id,
		TelemetryToken:     a.telemetryToken,
	}
	// Wiki MCP only matters for wiki-subject sessions; the playbook
	// editor doesn't reference wiki tools so we keep its mcp.json
	// minimal. For wiki subjects, wire in the launcher's wiki paths
	// (resolveWikiProposalsPath defaults the proposals dir when
	// unset).
	if _, ok := subject.(editor.WikiSubject); ok {
		mcpInputs.WikiPath = a.opts.WikiPath
		mcpInputs.WikiProposalsPath = resolveWikiProposalsPath(a.opts.WikiProposalsPath)
	}
	// Slack / incidentio MCPs are wired whenever the corresponding token
	// is linked. Channel id and incident reference are agent-supplied per
	// tool call; the operator's session-attached scope (Sources) is
	// surfaced to the agent through the prompt narrative, not through MCP
	// boot env. A session that attached a scope but doesn't have the token
	// linked still fails fast — drafting against a configured source
	// without API access would be misleading.
	if sources.HasSlack() {
		tok, err := a.connections.GetSlackToken()
		if err != nil || tok == "" {
			_ = os.RemoveAll(dir)
			writeError(w, http.StatusBadRequest, "slack not connected — link a token in the Connections panel first")
			return
		}
		mcpInputs.SlackToken = tok
	} else if tok, _ := a.connections.GetSlackToken(); tok != "" {
		// Token-only sessions (no operator-attached channel) still wire
		// the MCP so the agent can resolve and read any channel the
		// token grants access to.
		mcpInputs.SlackToken = tok
	}
	if sources.HasIncidentio() {
		tok, err := a.connections.GetIncidentioToken()
		if err != nil || tok == "" {
			_ = os.RemoveAll(dir)
			writeError(w, http.StatusBadRequest, "incidentio not connected — link an API key in the Connections panel first")
			return
		}
		mcpInputs.IncidentioToken = tok
	} else if tok, _ := a.connections.GetIncidentioToken(); tok != "" {
		mcpInputs.IncidentioToken = tok
	}
	mcpPath, err := preflight.WriteEditorMCPConfig(mcpInputs)
	if err != nil {
		_ = os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write mcp config: %v", err))
		return
	}

	sess := &editor.Session{
		ID:                     id,
		Subject:                subject,
		Sources:                sources,
		MCPConfigPath:          mcpPath,
		LinkedRepos:            linked,
		ToolCatalog:            a.editorToolCatalog(),
		SlackMCPAvailable:      mcpInputs.SlackToken != "",
		IncidentioMCPAvailable: mcpInputs.IncidentioToken != "",
		Profile:                a.prof,
		SessionDir:             dir,
		CreatedAt:              time.Now().UTC(),
	}
	if _, err := a.editorMgr.Register(sess); err != nil {
		_ = os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Bridge this session's events into the launcher's multiplex
	// stream. The closure captures sess, so reads sess.ID at call
	// time — Register has stamped the ID by now.
	sess.Forward = func(ev editor.Event) {
		a.manager.PublishStream(streamEnvelopeFromEditor(ev, sess.ID))
	}
	if err := sess.Start(); err != nil {
		// Don't unregister — the publishError path inside Start
		// surfaces the failure to any subscriber the drawer attaches
		// afterwards. Returning a 200 with the DTO lets the frontend
		// mount the SSE stream and pick up the error event.
		writeJSON(w, http.StatusOK, sess.Snapshot())
		return
	}
	writeJSON(w, http.StatusOK, sess.Snapshot())
}

// resolveSubject builds an editor.Subject from the wire payload,
// loading whatever side-data the prompt builder needs (playbook YAML
// for playbook subjects, existing markdown for wiki subjects).
func (a *apiHandlers) resolveSubject(in *subjectPayload) (editor.Subject, error) {
	if in == nil {
		return nil, errors.New("subject is required")
	}
	switch in.Kind {
	case "playbook":
		if in.Playbook == nil {
			return nil, errors.New("subject.playbook is required when kind=playbook")
		}
		if !playbookIDPattern.MatchString(in.Playbook.ID) {
			return nil, errors.New("invalid playbook id")
		}
		if in.Playbook.Version == "" {
			return nil, errors.New("playbook version is required")
		}
		// "+ new playbook" flow: nothing on disk yet, so trust the
		// inline YAML the editor drawer attached to the subject. The
		// agent uses this as its starting context — usually the
		// operator's pre-filled symptom + the empty-template scaffold.
		// We deliberately don't fall back to disk here; if IsNew is
		// set, the operator is asking for AI-driven creation, not
		// resumption of a saved playbook that happens to share the id.
		if in.Playbook.IsNew {
			return editor.PlaybookSubject{
				ID:      in.Playbook.ID,
				Version: in.Playbook.Version,
				Type:    in.Playbook.Type,
				YAML:    in.Playbook.YAML,
			}, nil
		}
		yamlBody, typeName, ok := a.resolvePlaybookYAML(in.Playbook.ID, in.Playbook.Version)
		if !ok {
			return nil, fmt.Errorf("playbook %s@%s not found", in.Playbook.ID, in.Playbook.Version)
		}
		return editor.PlaybookSubject{
			ID:      in.Playbook.ID,
			Version: in.Playbook.Version,
			Type:    typeName,
			YAML:    yamlBody,
		}, nil
	case "wiki":
		if in.Wiki == nil {
			return nil, errors.New("subject.wiki is required when kind=wiki")
		}
		if in.Wiki.Kind != "entry" && in.Wiki.Kind != "entity" {
			return nil, fmt.Errorf("invalid wiki kind %q (want entry|entity)", in.Wiki.Kind)
		}
		if strings.TrimSpace(in.Wiki.ID) == "" {
			return nil, errors.New("wiki id is required")
		}
		md := a.loadExistingWikiMarkdown(in.Wiki.Kind, in.Wiki.ID)
		return editor.WikiSubject{
			Kind:             in.Wiki.Kind,
			ID:               in.Wiki.ID,
			ExistingMarkdown: md,
		}, nil
	default:
		return nil, fmt.Errorf("unknown subject kind %q (want playbook|wiki)", in.Kind)
	}
}

// resolveSources turns the wire shape into editor.Sources, parsing the
// incident.io URL into its reference and looking up the investigation
// session-dir for any attached investigation id. Returns an empty
// Sources when nil.
func (a *apiHandlers) resolveSources(in *sourcesPayload) (editor.Sources, error) {
	if in == nil {
		return editor.Sources{}, nil
	}
	out := editor.Sources{
		SlackChannelID:   strings.TrimSpace(in.SlackChannelID),
		SlackChannelName: strings.TrimSpace(in.SlackChannelName),
		SlackSinceUnix:   in.SlackSinceUnix,
	}
	if invID := strings.TrimSpace(in.InvestigationID); invID != "" {
		inv := a.manager.Get(invID)
		if inv == nil {
			return editor.Sources{}, fmt.Errorf("investigation %q not found", invID)
		}
		out.InvestigationID = invID
		out.InvestigationDir = inv.SessionDir
	}
	if u := strings.TrimSpace(in.IncidentioURL); u != "" {
		ref, err := editor.ParseIncidentioURL(u)
		if err != nil {
			return editor.Sources{}, fmt.Errorf("incidentioURL: %w", err)
		}
		out.IncidentioURL = u
		out.IncidentioRef = ref
	}
	return out, nil
}

// loadExistingWikiMarkdown returns the body of the wiki entry the
// session is editing, or "" when the file doesn't exist (a new entry).
// Used by the agent so it revises rather than redrafts.
func (a *apiHandlers) loadExistingWikiMarkdown(kind, id string) string {
	if a.opts.WikiPath == "" {
		return ""
	}
	var rel string
	switch kind {
	case "entry":
		rel = filepath.Join("entries", id+".md")
	case "entity":
		rel = filepath.Join("entities", id+".md")
	default:
		return ""
	}
	body, err := os.ReadFile(filepath.Join(a.opts.WikiPath, rel))
	if err != nil {
		return ""
	}
	return string(body)
}

// handleFindEditorSession looks up the live session for a subject by
// kind+key. Used by the drawer on mount to decide between resume and
// create. Returns 404 if none.
//
// GET /api/editor-sessions?subject_kind=...&subject_key=...
func (a *apiHandlers) handleFindEditorSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	q := r.URL.Query()
	kind := q.Get("subject_kind")
	key := q.Get("subject_key")
	if kind == "" || key == "" {
		writeError(w, http.StatusBadRequest, "subject_kind and subject_key are required")
		return
	}
	sess := a.editorMgr.ExistingByKey(key)
	if sess == nil || sess.Subject == nil || sess.Subject.SubjectKind() != kind {
		writeError(w, http.StatusNotFound, "no live editor session for this subject")
		return
	}
	writeJSON(w, http.StatusOK, sess.Snapshot())
}

// handleGetEditorSession returns the DTO for a session id. Mirrors the
// investigation Get endpoint.
//
// GET /api/editor-sessions/{id}
func (a *apiHandlers) handleGetEditorSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	sess := a.editorMgr.Get(r.PathValue("id"))
	if sess == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}
	writeJSON(w, http.StatusOK, sess.Snapshot())
}

// handleDeleteEditorSession is the explicit X-button close path: tear
// down claude, drop the per-session dir, and forget the session.
//
// DELETE /api/editor-sessions/{id}
func (a *apiHandlers) handleDeleteEditorSession(w http.ResponseWriter, r *http.Request) {
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	id := r.PathValue("id")
	if !a.editorMgr.Remove(id, true) {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleEditorMessage submits a follow-up prompt to the editor session.
// Same shape as the investigation /messages endpoint.
//
// POST /api/editor-sessions/{id}/messages  body: {"text": "..."}
func (a *apiHandlers) handleEditorMessage(w http.ResponseWriter, r *http.Request) {
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	sess := a.editorMgr.Get(r.PathValue("id"))
	if sess == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if err := sess.SendFollowUp(body.Text); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleInterruptEditorSession cancels the editor session's in-flight
// turn. Same contract as the investigation /interrupt endpoint.
//
// POST /api/editor-sessions/{id}/interrupt
// 202 — interrupt requested; drain will emit the breadcrumb + end events.
// 404 — unknown session.
// 409 — no turn currently in flight.
func (a *apiHandlers) handleInterruptEditorSession(w http.ResponseWriter, r *http.Request) {
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	sess := a.editorMgr.Get(r.PathValue("id"))
	if sess == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}
	if err := sess.Interrupt(); err != nil {
		if errors.Is(err, editor.ErrNotStreaming) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// resolvePlaybookYAML returns the YAML + type for one id.
// The version parameter is kept for API compatibility but is no longer
// used to select among sibling files — each id has exactly one file on
// disk; git history is the record of prior versions. The user dir takes
// precedence over the embedded (plugin/system) version, matching the
// runtime loader's resolution order.
func (a *apiHandlers) resolvePlaybookYAML(id, version string) (yamlBody, typeName string, ok bool) {
	if a.opts.UserPlaybooksDir != "" {
		groups, err := loadUserPlaybookGroups(a.opts.UserPlaybooksDir)
		if err == nil {
			if g, present := groups[id]; present {
				return g.YAML, g.Type, true
			}
		}
	}
	// Fall back to the embedded (plugin/system) version from the cache.
	if meta := a.metaCache.get(); meta != nil {
		if mp, present := meta.Playbooks[id]; present {
			return mp.YAML, mp.Type, true
		}
	}
	return "", "", false
}

// editorToolCatalog projects the launcher's metaCache into the
// catalog shape the editor prompt expects. Sorted by (server, name)
// so the agent sees a deterministic listing across launches and
// the prompt-cache layer downstream actually hits. Empty result is
// safe — the prompt section just gets skipped.
func (a *apiHandlers) editorToolCatalog() []prompts.ToolCatalogEntry {
	meta := a.metaCache.get()
	if meta == nil || len(meta.Tools) == 0 {
		return nil
	}
	out := make([]prompts.ToolCatalogEntry, 0, len(meta.Tools))
	for _, t := range meta.Tools {
		ins := make([]prompts.ToolCatalogInput, 0, len(t.Inputs))
		for _, in := range t.Inputs {
			ins = append(ins, prompts.ToolCatalogInput{
				Name:     in.Name,
				Required: in.Required,
			})
		}
		out = append(out, prompts.ToolCatalogEntry{
			Server:      t.Server,
			Name:        t.Name,
			Description: t.Description,
			Inputs:      ins,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// editorNewID is a thin wrapper over editor's id minter; declared here
// because the editor package keeps newID unexported. The handlers need
// one up-front to compose the session dir before Register, so we
// expose our own.
func editorNewID() (string, error) {
	return NewID()
}

// handleRebindEditorSession retargets an existing editor session at a
// new Subject. Used by the playbook editor's "+ new playbook" → accept
// flow: when the operator approves a proposal that the agent drafted
// against `__new`, the session must follow the editor onto the saved
// id without tearing down the conversation.
//
// POST /api/editor-sessions/{id}/rebind
// Body: same shape as POST /api/editor-sessions's "subject" field, e.g.
//
//	{"kind":"playbook","playbook":{"id":"my_id","version":"HEAD"}}
//
// Notably, the IsNew/YAML side-data on the playbook payload is ignored:
// rebind never re-resolves disk content. The new Subject's ID + version
// are what move; the agent's already-baked prompt context isn't
// regenerated.
func (a *apiHandlers) handleRebindEditorSession(w http.ResponseWriter, r *http.Request) {
	if a.editorMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "editor manager not initialised")
		return
	}
	id := r.PathValue("id")
	var body subjectPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	subject, err := a.rebindResolveSubject(&body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, err := a.editorMgr.Rebind(id, subject)
	if err != nil {
		switch {
		case errors.Is(err, editor.ErrSessionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, editor.ErrSessionExists):
			writeError(w, http.StatusConflict, err.Error())
		default:
			// Reaching here means Rebind returned an error shape
			// we don't recognise. Today the only such case is the
			// nil-subject guard, which can't fire because
			// rebindResolveSubject already validated the input —
			// so this is genuinely a server bug if it happens.
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, sess.Snapshot())
}

// rebindResolveSubject is a thin variant of resolveSubject that skips
// disk loads. Rebind only needs the (kind, id, version) tuple; the
// already-running session keeps its baked prompt context, so re-reading
// YAML or wiki markdown would be wasted I/O — and would mean a deleted
// playbook can't be retargeted to a fresh id.
func (a *apiHandlers) rebindResolveSubject(in *subjectPayload) (editor.Subject, error) {
	if in == nil {
		return nil, errors.New("subject is required")
	}
	switch in.Kind {
	case "playbook":
		if in.Playbook == nil {
			return nil, errors.New("subject.playbook is required when kind=playbook")
		}
		if !playbookIDPattern.MatchString(in.Playbook.ID) {
			return nil, errors.New("invalid playbook id")
		}
		if in.Playbook.Version == "" {
			return nil, errors.New("playbook version is required")
		}
		return editor.PlaybookSubject{
			ID:      in.Playbook.ID,
			Version: in.Playbook.Version,
			Type:    in.Playbook.Type,
		}, nil
	case "wiki":
		if in.Wiki == nil {
			return nil, errors.New("subject.wiki is required when kind=wiki")
		}
		if in.Wiki.Kind != "entry" && in.Wiki.Kind != "entity" {
			return nil, fmt.Errorf("invalid wiki kind %q (want entry|entity)", in.Wiki.Kind)
		}
		if strings.TrimSpace(in.Wiki.ID) == "" {
			return nil, errors.New("wiki id is required")
		}
		return editor.WikiSubject{Kind: in.Wiki.Kind, ID: in.Wiki.ID}, nil
	default:
		return nil, fmt.Errorf("unknown subject kind %q (want playbook|wiki)", in.Kind)
	}
}
