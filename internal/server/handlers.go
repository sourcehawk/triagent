package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/connections"
	"github.com/sourcehawk/triagent/internal/editor"
	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/internal/promforward"
	"github.com/sourcehawk/triagent/internal/repos"
	"github.com/sourcehawk/triagent/internal/sessions"
	"github.com/sourcehawk/triagent/internal/watches"
)

// apiHandlers carries the dependencies the JSON handlers need without
// passing them through every closure. Held by Server; one per launch.
type apiHandlers struct {
	opts           Options
	manager        *Manager
	editorMgr      *editor.Manager
	connections    *connections.Manager
	prof           *profile.Profile // investigation profile (prompt content + playbook IDs)
	telemetryURL   string           // loopback URL triagent-mcp posts tool-events to
	telemetryToken string           // shared secret triagent-mcp sends as Bearer
	metaCache      *metaCache
	capabilities   Capabilities // optional dev-tool probes done at startup
	mcpHealth      *mcpHealth   // per-session MCP call stats for the health bar

	// sessionDrafter is the seam used by handlePushSessionPR. Production
	// builds wire it to a subprocess invocation of triagent-mcp --kind sessions;
	// tests replace it with a stub.
	sessionDrafter sessionDrafter

	// architectureWorker runs triagent-mcp generate-architecture-summary in the
	// background for auto-gen on add and manual refresh from the repo page.
	// Single-flight per <owner>/<name>; nil disables the auto-gen path
	// (handleAddRepo skips the enqueue and the refresh endpoint returns 503).
	architectureWorker *repos.ArchitectureWorker

	// generationContext returns the long-lived context for background
	// generation goroutines. Production wires it to manager.ParentContext()
	// so a client disconnect does not kill mid-flight generation. Tests
	// leave this nil; the handler then falls back to context.Background().
	generationContext func() context.Context

	// slackAPIBase / incidentioAPIBase let tests stub the upstream
	// services. Empty values use production endpoints.
	slackAPIBase      string
	incidentioAPIBase string

	// preflightFn is the function used to run preflight. Tests can replace
	// this with a stub that skips kubernetes I/O. When nil, preflight.Run
	// is used.
	preflightFn func(preflight.Options) (*preflight.Result, error)

	// sessionFn builds the live claude session after rehydrate resolves
	// the new external state. Tests inject a stub so the test process
	// does not need a real `claude` binary on PATH. When nil, the
	// rehydrate path falls back to sessions.New (which calls
	// exec.LookPath("claude") and so requires the CLI in production).
	sessionFn func(sessions.Options, string) (investigationSession, error)

	// codefixGh runs gh from the codefix discard handler (closes PR,
	// deletes branch). Tests inject stubs.
	codefixGh codefixGhRunner

	// prStateCache is populated by the PR-state poller (Task 13). Until
	// that lands the map is nil and lookupCodefixPRState returns "".
	prStateCache map[string]PRInfo

	// codefixRepos is the set of {owner/name} the poller fans out
	// across (Task 13). Until that lands the set is nil and the poller
	// only polls the sessions repo.
	codefixRepos map[string]struct{}

	// autoBackendFactory is the test seam used by the auto-mode preflight
	// branch when wiring AutoOptions for Manager.EnableAuto. Production
	// leaves this nil; Manager.EnableAuto then falls back to
	// defaultAutoBackendFactory (which spawns a real claude.Session).
	// Tests inject a fake to skip the subprocess.
	autoBackendFactory func(AutoOptions) (autoBackendish, error)

	// watches is the signal-watches Manager. Nil when opts.UserWatchesPath is empty.
	watches *watches.Manager

	// ingestToken is the per-launch bearer the ingestion-agent MCP sends.
	// Empty disables the loopback (handlers reject all requests).
	ingestToken string

	// promForwarder manages per-investigation Prometheus port-forwards.
	// Nil when prom isn't configured for the active profile.
	promForwarder promForwarderAPI
}

// resolvedLinkedRepos returns the merged list at preflight time:
// profile linked_repos + user_repos.yaml, deduped (profile wins).
func (a *apiHandlers) resolvedLinkedRepos() ([]repos.LinkedRepo, error) {
	userList, err := repos.LoadUserRepos(a.opts.UserReposPath)
	if err != nil {
		return nil, fmt.Errorf("load user repos: %w", err)
	}
	return repos.Resolve(repos.ProfileDefaults(a.opts.Profile), userList), nil
}

func (a *apiHandlers) register(mux *http.ServeMux) {
	mux.HandleFunc("/api/clusters", a.handleClusters)
	mux.HandleFunc("/api/login", a.handleLogin)
	mux.HandleFunc("/api/preflight", a.handlePreflight)
	mux.HandleFunc("/api/investigations", a.handleListInvestigations)
	mux.HandleFunc("GET /api/investigations/{id}", a.handleGetInvestigation)
	mux.HandleFunc("DELETE /api/investigations/{id}", a.handleDeleteInvestigation)
	mux.HandleFunc("GET /api/investigations/{id}/export", a.handleExportInvestigation)
	mux.HandleFunc("POST /api/investigations/import", a.handleImportInvestigation)
	mux.HandleFunc("POST /api/investigations/{id}/start", a.handleStartSession)
	mux.HandleFunc("GET /api/stream", a.handleStream)
	mux.HandleFunc("POST /api/stream/close", a.handleCloseStream)
	mux.HandleFunc("GET /api/investigations/{id}/transcript", a.handleInvestigationTranscript)
	mux.HandleFunc("POST /api/investigations/{id}/messages", a.handleMessage)
	mux.HandleFunc("POST /api/investigations/{id}/interrupt", a.handleInterrupt)
	mux.HandleFunc("POST /api/internal/tool-events", a.handleToolEvent)
	mux.HandleFunc("GET /api/repos", a.handleListRepos)
	mux.HandleFunc("POST /api/repos", a.handleAddRepo)
	mux.HandleFunc("DELETE /api/repos/{owner}/{name}", a.handleDeleteRepo)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/summary", a.handleGetRepoSummary)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/summary/status", a.handleGetRepoSummaryStatus)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/summary/edits", a.handleGetRepoSummaryEdits)
	mux.HandleFunc("PUT /api/repos/{owner}/{name}/summary", a.handleUpdateRepoSummary)
	mux.HandleFunc("POST /api/repos/{owner}/{name}/summary/refresh", a.handleRefreshRepoSummary)
	mux.HandleFunc("GET /api/playbooks", a.handleListPlaybooks)
	mux.HandleFunc("GET /api/playbooks/{id}", a.handleGetPlaybook)
	mux.HandleFunc("POST /api/playbooks/{id}", a.handlePutPlaybook)
	mux.HandleFunc("DELETE /api/playbooks/{id}", a.handleDeletePlaybook)
	mux.HandleFunc("POST /api/playbooks/{id}/push-pr", a.handlePushPlaybookPR)
	mux.HandleFunc("POST /api/playbooks/{id}/sync-state", a.handlePlaybookSyncState)
	mux.HandleFunc("GET /api/playbooks-upstream", a.handlePlaybooksUpstreamStatus)
	mux.HandleFunc("POST /api/playbooks-upstream/sync", a.handlePlaybooksUpstreamSync)
	mux.HandleFunc("GET /api/playbook-types", a.handleListPlaybookTypes)
	mux.HandleFunc("POST /api/playbook-types", a.handleCreatePlaybookType)
	mux.HandleFunc("DELETE /api/playbook-types/{name}", a.handleDeletePlaybookType)
	mux.HandleFunc("POST /api/playbook-types/{name}/propose-removal", a.handleProposePlaybookTypeRemoval)
	mux.HandleFunc("GET /api/playbooks/{id}/commits", a.handleListPlaybookCommits)
	mux.HandleFunc("GET /api/playbooks/{id}/commits/{sha}", a.handleGetPlaybookCommit)
	mux.HandleFunc("GET /api/playbooks/{id}/related", a.handleRelatedPlaybooks)
	mux.HandleFunc("GET /api/playbooks/{id}/related-wiki", a.handleRelatedWikiEntries)
	mux.HandleFunc("GET /api/playbook-proposals", a.handleListPlaybookProposals)
	mux.HandleFunc("GET /api/playbook-proposals/{id}", a.handleGetProposal)
	mux.HandleFunc("POST /api/playbook-proposals/{id}/approve", a.handleApproveProposal)
	mux.HandleFunc("POST /api/playbook-proposals/{id}/decline", a.handleDeclineProposal)
	mux.HandleFunc("GET /api/wiki-proposals", a.handleListWikiProposals)
	mux.HandleFunc("GET /api/wiki-proposals/{id}", a.handleGetWikiProposal)
	mux.HandleFunc("POST /api/wiki-proposals/{id}/approve", a.handleApproveWikiProposal)
	mux.HandleFunc("POST /api/wiki-proposals/{id}/decline", a.handleDeclineWikiProposal)
	mux.HandleFunc("GET /api/codefix-proposals", a.handleListCodefixProposals)
	mux.HandleFunc("GET /api/codefix-proposals/{id}", a.handleGetCodefixProposal)
	mux.HandleFunc("POST /api/codefix-proposals/{id}/discard", a.handleDiscardCodefixProposal)
	mux.HandleFunc("POST /api/wiki/entries/{slug}/push-pr", a.handlePushWikiPR)
	mux.HandleFunc("POST /api/wiki/entries/{slug}/delete-pr", a.handleDeleteWikiEntryPR)
	mux.HandleFunc("POST /api/wiki/entries/{slug}/resolve", a.handleResolveWikiEntry)
	mux.HandleFunc("DELETE /api/wiki/entries/{slug}", a.handleDeleteWikiEntry)
	mux.HandleFunc("PUT /api/wiki/entries/{slug}", a.handleUpdateWikiEntry)
	mux.HandleFunc("PUT /api/wiki/entities/{type}/{name}", a.handleUpdateWikiEntity)
	mux.HandleFunc("POST /api/wiki/push-pr-bundle", a.handlePushWikiPRBundle)
	mux.HandleFunc("GET /api/wiki/open-prs", a.handleListOpenWikiPRs)
	mux.HandleFunc("GET /api/wiki-upstream", a.handleWikiUpstreamStatus)
	mux.HandleFunc("POST /api/wiki-upstream/sync", a.handleWikiUpstreamSync)
	mux.HandleFunc("GET /api/sessions-upstream", a.handleSessionsUpstreamStatus)
	mux.HandleFunc("POST /api/sessions-upstream/sync", a.handleSessionsUpstreamSync)
	mux.HandleFunc("POST /api/sessions-upstream/refresh-pr-states", a.handleSessionsUpstreamRefreshPRStates)
	mux.HandleFunc("GET /api/sessions-upstream/sessions", a.handleSessionsUpstreamList)
	mux.HandleFunc("GET /api/sessions-upstream/sessions/{slug}", a.handleSessionsUpstreamGet)
	mux.HandleFunc("GET /api/sessions-upstream/sessions/{slug}/bundle", a.handleSessionsUpstreamBundle)
	mux.HandleFunc("POST /api/investigations/{id}/archive", a.handleArchiveInvestigation)
	mux.HandleFunc("POST /api/investigations/{id}/label", a.handleRenameInvestigation)
	mux.HandleFunc("POST /api/internal/investigations/{id}/label", a.handleSetLabelFromMCP)
	mux.HandleFunc("POST /api/internal/prom/{id}/endpoint", a.handlePromEndpoint)
	mux.HandleFunc("POST /api/internal/investigations/{id}/auto/send-message", a.handleAutoSendMessage)
	mux.HandleFunc("POST /api/internal/investigations/{id}/auto/finish", a.handleAutoFinish)
	mux.HandleFunc("POST /api/internal/investigations/{id}/auto/request-takeover", a.handleAutoRequestTakeover)
	mux.HandleFunc("POST /api/internal/investigations/{id}/auto/approve-proposal", a.handleAutoApproveProposal)
	mux.HandleFunc("POST /api/investigations/{id}/auto/takeover", a.handleAutoTakeover)
	mux.HandleFunc("POST /api/investigations/{id}/auto/resume", a.handleAutoResume)
	mux.HandleFunc("POST /api/investigations/{id}/push-pr", a.handlePushSessionPR)
	mux.HandleFunc("GET /api/wiki/stats", a.handleWikiStats)
	mux.HandleFunc("GET /api/wiki/entries", a.handleWikiListEntries)
	mux.HandleFunc("GET /api/wiki/entries/{slug}", a.handleWikiGetEntry)
	mux.HandleFunc("GET /api/wiki/correlated-sessions", a.handleWikiCorrelatedSessions)
	mux.HandleFunc("GET /api/wiki/entities", a.handleWikiListEntities)
	mux.HandleFunc("GET /api/wiki/entities/{type}/{name}", a.handleWikiGetEntity)
	mux.HandleFunc("GET /api/tools", a.handleListTools)
	mux.HandleFunc("GET /api/capabilities", a.handleCapabilities)
	mux.HandleFunc("GET /api/profile/inputs", a.handleProfileInputs)
	mux.HandleFunc("GET /api/profile/prom-defaults", a.handleProfilePromDefaults)
	mux.HandleFunc("POST /api/editor-sessions", a.handleCreateOrResumeEditorSession)
	mux.HandleFunc("GET /api/editor-sessions", a.handleFindEditorSession)
	mux.HandleFunc("GET /api/editor-sessions/{id}", a.handleGetEditorSession)
	mux.HandleFunc("DELETE /api/editor-sessions/{id}", a.handleDeleteEditorSession)
	mux.HandleFunc("POST /api/editor-sessions/{id}/messages", a.handleEditorMessage)
	mux.HandleFunc("POST /api/editor-sessions/{id}/rebind", a.handleRebindEditorSession)
	mux.HandleFunc("GET /api/editor-sessions/{id}/transcript", a.handleEditorTranscript)
	mux.HandleFunc("GET /api/investigations/{id}/mcp-health", a.handleMCPHealth)
	mux.HandleFunc("GET /api/investigations/{id}/mcp-probe", a.handleMCPProbe)
	mux.HandleFunc("GET /api/connections", a.handleGetConnections)
	mux.HandleFunc("PUT /api/connections/slack", a.handlePutSlackToken)
	mux.HandleFunc("PUT /api/connections/incidentio", a.handlePutIncidentioToken)
	mux.HandleFunc("DELETE /api/connections/{kind}", a.handleDeleteConnection)
	mux.HandleFunc("GET /api/slack/channels", a.handleListSlackChannels)

	mux.HandleFunc("GET /api/watches", a.handleListWatches)
	mux.HandleFunc("POST /api/watches", a.handleCreateWatch)
	mux.HandleFunc("PATCH /api/watches/{id}", a.handlePatchWatch)
	mux.HandleFunc("DELETE /api/watches/{id}", a.handleDeleteWatch)
	mux.HandleFunc("POST /api/watches/{id}/poll-now", a.handlePollNowWatch)
	mux.HandleFunc("GET /api/watches/{id}/items", a.handleListWatchItems)
	mux.HandleFunc("GET /api/watches/{id}/signals", a.handleListWatchSignals)
	mux.HandleFunc("POST /api/watches/{id}/clear", a.handleClearWatch)
	mux.HandleFunc("DELETE /api/watches/{id}/logs", a.handleDeleteWatchLogs)

	mux.HandleFunc("GET /api/watches/{id}/ingest-runs", a.handleListIngestRuns)
	mux.HandleFunc("GET /api/watches/{id}/ingest-runs/{runID}", a.handleGetIngestRun)

	mux.HandleFunc("GET /api/watches/{id}/queue", a.handleGetWatchQueue)
	mux.HandleFunc("POST /api/watches/{id}/queue/{sid}/cancel", a.handleCancelQueued)

	mux.HandleFunc("POST /api/watches/{id}/signals/{sid}/start", a.handleManualStart)
	mux.HandleFunc("POST /api/watches/{id}/signals/{sid}/retry", a.handleRetrySignal)

	mux.HandleFunc("POST /api/watches/{id}/ingest/history", a.handleIngestHistory)
	mux.HandleFunc("POST /api/watches/{id}/ingest/start-investigation", a.handleIngestStart)
	mux.HandleFunc("POST /api/watches/{id}/ingest/report-unclear", a.handleIngestUnclear)
	mux.HandleFunc("POST /api/watches/{id}/ingest/dismiss-items", a.handleIngestDismiss)
}

// handleCapabilities returns the cached capability probes from server
// startup. The frontend uses this to disable feature buttons whose
// underlying tool isn't ready (e.g. "Push as PR" when gh is missing).
//
// GET /api/capabilities
func (a *apiHandlers) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, a.capabilities)
}

// handleClusters returns the list of clusters the provider can see.
// GET /api/clusters
func (a *apiHandlers) handleClusters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.opts.Provider == nil {
		writeError(w, http.StatusServiceUnavailable, "kubernetes provider not wired")
		return
	}
	clusters, err := a.opts.Provider.ListClusters(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("list clusters: %v", err))
		return
	}
	out := make([]clusterDTO, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, clusterDTO{Name: c.Name, ID: c.ID, Labels: c.Labels})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": out})
}

// handleLogin runs the provider's Login for the named cluster. tsh prompts
// (SSO/2FA) are written to the controlling terminal where the operator
// invoked `triagent --web`; the browser request blocks until login
// finishes. Already-logged-in clusters complete instantly.
//
// POST /api/login  body: {"cluster": "<name>"}
func (a *apiHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.opts.Provider == nil {
		writeError(w, http.StatusServiceUnavailable, "kubernetes provider not wired")
		return
	}
	var body struct {
		Cluster string `json:"cluster"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster is required")
		return
	}
	res, err := a.opts.Provider.Login(r.Context(), body.Cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("login %q: %v", body.Cluster, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contextName": res.ContextName})
}

// handlePreflight runs preflight.Run for a chosen context + cluster, writes
// the per-session MCP config, optionally starts the prom port-forward, and
// registers the result as a live Investigation. Phase 4 will use the
// returned id to attach a streaming claude session.
//
// POST /api/preflight  body: PreflightRequest
func (a *apiHandlers) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body preflightRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate inputs against the loaded profile when available.
	// When prof is nil (no profile wired) validation is skipped.
	if a.prof != nil {
		if err := profile.ValidateInputValues(a.prof.InvestigationInputs, body.Inputs); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Extract typed values from the generic inputs map.
	// Canonical input IDs (same for any profile that
	// follows the same schema): cluster_id, incident_url, slack_channel,
	// notes. Unknown profiles may omit any of these; safe to default to "".
	clusterIDCtx := body.Inputs["cluster_id"]
	slackCtx := body.Inputs["slack_channel"]
	incidentCtx := body.Inputs["incident_url"]
	notesCtx := body.Inputs["notes"]

	clusterID, _ := clusterIDCtx["value"].(string)
	incidentURL := strings.TrimSpace(func() string {
		v, _ := incidentCtx["value"].(string)
		return v
	}())
	slackChannelID := strings.TrimSpace(func() string {
		v, _ := slackCtx["id"].(string)
		return v
	}())
	slackChannelName := strings.TrimSpace(func() string {
		v, _ := slackCtx["name"].(string)
		return v
	}())
	slackChannelURL := strings.TrimSpace(func() string {
		v, _ := slackCtx["url"].(string)
		return v
	}())
	notes, _ := notesCtx["value"].(string)
	clusterID = strings.TrimSpace(clusterID)

	// Backfill slackChannelURL from the picker form (channel id + persisted
	// workspace URL) so wiki tools downstream get a valid slack_link.
	if slackChannelID != "" && slackChannelURL == "" {
		workspaceURL, _ := a.connections.GetSlackWorkspaceURL()
		slackChannelURL = SlackChannelURL(workspaceURL, slackChannelID)
	}

	sessionDir, err := makeSessionDir(a.opts.SessionsRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("session dir: %v", err))
		return
	}

	// Pre-allocate the investigation id so we can bake it into the per-
	// session MCP config as a trace id; triagent-mcp's telemetry POSTs tag
	// every event with this id so we know which investigation to
	// publish to.
	investigationID, err := NewID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate id: %v", err))
		return
	}

	linked, err := a.resolvedLinkedRepos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve linked repos: %v", err))
		return
	}

	// Slack / incidentio MCPs are wired whenever the corresponding token
	// is linked. Channel id and incident reference are conveyed to the
	// agent through the system prompt and threaded into per-tool-call
	// args; they do NOT flow through the MCP boot env any more.
	slackMCPToken, _ := a.connections.GetSlackToken()
	ioMCPToken, _ := a.connections.GetIncidentioToken()

	// Resolve per-investigation prom target by overlaying body.Prom
	// onto the profile's defaults. Each non-zero field in the override
	// wins; the rest inherit from the profile.
	promTarget, promDisabled := resolvePromTarget(a.prof, body.Prom)

	runPreflight := a.preflightFn
	if runPreflight == nil {
		runPreflight = preflight.Run
	}
	res, err := runPreflight(preflight.Options{
		Provider:           a.opts.Provider,
		Ctx:                r.Context(),
		Namespace:          "",
		SessionDir:         sessionDir,
		MCPBinaryPath:      a.opts.MCPBinaryPath,
		DocsServerName:     a.opts.DocsServerName,
		LinkedRepos:        linked,
		GitCacheDir:        a.opts.GitCacheDir,
		UserPlaybooksDir:   a.opts.UserPlaybooksDir,
		PluginPlaybooksDir: a.opts.PluginPlaybooksDir,
		SystemPlaybooksDir: a.opts.SystemPlaybooksDir,
		WikiPath:           a.opts.WikiPath,
		WikiProposalsPath:  resolveWikiProposalsPath(a.opts.WikiProposalsPath),
		SlackToken:         slackMCPToken,
		IncidentioToken:    ioMCPToken,
		TelemetryURL:       a.telemetryURL,
		TraceID:            investigationID,
		TelemetryToken:     a.telemetryToken,
		Profile:            a.prof,
		PromTarget:         promTarget,
		PromDisabled:       promDisabled,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Pre-seed <sessionDir>/active-context from the operator's
	// cluster_id pick so the k8s MCP's startup hydrate finds it. Best-
	// effort: a missing provider, an unresolved cluster, or a write
	// error all leave the file absent and the agent falls back to
	// calling switch_context itself.
	activeContext, err := seedActiveContext(r.Context(), a.opts.Provider, sessionDir, res.KubeconfigPath, clusterID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: seed active-context for %s: %v\n", investigationID, err)
	}

	inv := &Investigation{
		ID:                   investigationID,
		IncidentURL:          incidentURL,
		SlackChannelURL:      slackChannelURL,
		SlackChannelID:       slackChannelID,
		SlackChannelName:     slackChannelName,
		Notes:                notes,
		MCPConfigPath:        res.MCPConfigPath,
		KubeconfigPath:       res.KubeconfigPath,
		DocsPrefix:           res.DocsPrefix,
		SessionDir:           sessionDir,
		SlackMCPEnabled:      slackMCPToken != "",
		IncidentioMCPEnabled: ioMCPToken != "",
		LinkedRepos:          linked,
		Profile:              a.prof,
		PromTarget:           promTarget,
		PromDisabled:         promDisabled,
		ActiveContext:        activeContext,
	}
	inv.Author = resolveGitAuthor()
	if _, err := a.manager.Register(inv); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Surface the operator's notes as the first transcript item so the
	// chat reads top-down ("here's what I was investigating, here's what
	// claude did about it"). Empty notes are skipped — chat just starts
	// with claude's first reply. The envelope persists like any other
	// event so archived sessions replay it on reload.
	if n := strings.TrimSpace(notes); n != "" {
		inv.publish(EventEnvelope{Kind: envKindUser, Text: n})
	}

	// Auto-mode opt-in: spawn the operator agent in a goroutine so the
	// HTTP response doesn't block on the multi-second claude warm-up.
	// A failure to write op-mcp.json is logged and silently degrades
	// the investigation to manual mode — the user-visible preflight
	// still succeeded.
	if body.Auto {
		opPath, err := a.writeOperatorMCPConfig(inv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "investigate: auto mode disabled for %s (cannot write op-mcp.json): %v\n", inv.ID, err)
		} else {
			opts := AutoOptions{
				OperatorMCPConfigPath: opPath,
				OperatorCwd:           filepath.Join(inv.SessionDir, "auto"),
				Briefing:              autoBriefing(inv, clusterID),
				Env: []string{
					"TRIAGENT_MCP_TELEMETRY_URL=" + a.telemetryURL,
					"TRIAGENT_MCP_TELEMETRY_TOKEN=" + a.telemetryToken,
					"TRIAGENT_MCP_TRACE_ID=" + inv.ID,
				},
				BackendFactory: a.autoBackendFactory,
				Profile:        a.prof,
			}
			if a.manager.trackBackground() {
				go func() {
					defer a.manager.bgDone()
					if err := a.manager.EnableAuto(inv, opts); err != nil {
						fmt.Fprintf(os.Stderr, "investigate: EnableAuto %s: %v\n", inv.ID, err)
					}
				}()
			}
		}
	}

	writeJSON(w, http.StatusOK, inv.Snapshot())
}

// handleListInvestigations returns the active investigations for the
// sidebar/list view in the UI. GET /api/investigations
func (a *apiHandlers) handleListInvestigations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"investigations": a.manager.List()})
}

// handleGetInvestigation returns a single investigation's metadata. Used
// by the SPA to refresh state for a deep-linked tab without listing the
// whole sidebar.
//
// GET /api/investigations/{id}
func (a *apiHandlers) handleGetInvestigation(w http.ResponseWriter, r *http.Request) {
	inv := a.manager.Get(r.PathValue("id"))
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	writeJSON(w, http.StatusOK, inv.Snapshot())
}

// handleDeleteInvestigation removes an investigation from memory and
// deletes its on-disk session directory. Idempotent. The SPA hits this
// from the trash icon in the sidebar.
//
// DELETE /api/investigations/{id}
func (a *apiHandlers) handleDeleteInvestigation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id) // capture before Remove so we can fire the terminal hook
	if !a.manager.Remove(id, true) {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	a.mcpHealth.drop(id)
	a.manager.fireTerminal(inv) // M6.4: release spawner slot on delete
	w.WriteHeader(http.StatusNoContent)
}

// handleExportInvestigation downloads a redacted share bundle (metadata +
// transcript) for an investigation. The bundle drops local-only details
// (MCP config path, kubeconfig context, linked-repo paths, on-disk session
// dir) so a teammate can import it on their own launcher without leaking
// the exporter's filesystem layout.
//
// GET /api/investigations/{id}/export
func (a *apiHandlers) handleExportInvestigation(w http.ResponseWriter, r *http.Request) {
	inv := a.manager.Get(r.PathValue("id"))
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	dto := inv.Snapshot()
	if dto.SessionDir == "" {
		writeError(w, http.StatusBadRequest, "investigation has no on-disk session to export")
		return
	}
	bundle, err := buildShareBundle(dto.SessionDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("build bundle: %v", err))
		return
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("encode bundle: %v", err))
		return
	}
	filename := exportFilename(dto)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// exportFilename composes a friendly download name from a DTO. Falls back
// to the id if context/namespace are missing for any reason.
func exportFilename(dto InvestigationDTO) string {
	short := dto.ID
	if len(short) > 8 {
		short = short[:8]
	}
	parts := make([]string, 0, 2)
	if dto.Namespace != "" {
		parts = append(parts, sanitiseForFilename(dto.Namespace))
	}
	parts = append(parts, short)
	return strings.Join(parts, "-") + ".triagent.json"
}

// sanitiseForFilename strips characters that would be awkward in a download
// filename across operating systems. Keeps alphanumerics, dashes, dots,
// underscores; everything else collapses to a dash.
func sanitiseForFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// handleImportInvestigation accepts a share bundle (the JSON file produced
// by handleExportInvestigation) and lands it as a fresh archived
// investigation in the receiver's sidebar. Accepts either a raw JSON body
// (Content-Type: application/json) or a multipart upload with the bundle
// in a "file" form field — gives the SPA the freedom to use whichever fits
// the import affordance (paste/json vs file picker/drag-drop).
//
// POST /api/investigations/import
func (a *apiHandlers) handleImportInvestigation(w http.ResponseWriter, r *http.Request) {
	body, err := readImportBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer func() { _ = body.Close() }()

	bundle, err := decodeShareBundle(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Idempotent re-import: if we already have this investigation locally
	// (matched by source id, which the import flow preserves so that the
	// imported slug matches upstream), return the existing snapshot
	// instead of materialising a duplicate dir. The frontend's upstream
	// "replay full transcript" path already short-circuits on slug match,
	// so this mostly catches drag-drop re-imports — but it stops them
	// from leaking orphan session dirs on disk.
	if id := bundle.Source.InvestigationID; id != "" {
		if existing := a.manager.Get(id); existing != nil {
			writeJSON(w, http.StatusOK, existing.Snapshot())
			return
		}
	}

	dir, _, err := materializeImport(a.opts.SessionsRoot, bundle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("materialise import: %v", err))
		return
	}

	inv, err := a.manager.AdoptFromDir(dir)
	if err != nil {
		// The on-disk dir is now an orphan we can't surface in the UI;
		// leave it for the operator to clean up rather than rm-rf'ing it
		// from a failure path.
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("adopt: %v", err))
		return
	}
	// If the imported session's slug already exists in the upstream
	// sessions clone (i.e. this is an upstream pull rather than a
	// peer-share of an unpushed session), backfill PushedAt so the
	// sidebar checkmark shows immediately. Without this the launcher
	// would only flip the indicator on the next startup sweep.
	// Best-effort: a missing/unreadable clone is fine; the next startup
	// reconcile picks it up.
	if a.opts.SessionsPath != "" {
		a.manager.ReconcileUpstreamPushed(a.opts.SessionsPath)
	}
	writeJSON(w, http.StatusOK, inv.Snapshot())
}

// readImportBody returns a reader over the bundle JSON regardless of
// whether the request used multipart or a raw body. Caller must Close().
// The 64 MiB cap matches what we'd accept as a single events.jsonl on
// disk; bundles much larger than that are likely malformed.
func readImportBody(r *http.Request) (io.ReadCloser, error) {
	const maxBytes = 64 << 20
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			return nil, fmt.Errorf("parse multipart: %w", err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf("multipart missing 'file' field: %w", err)
		}
		return f, nil
	}
	return io.NopCloser(http.MaxBytesReader(nil, r.Body, maxBytes)), nil
}

// handleMCPHealth returns the live per-MCP-server call stats for an active
// investigation. The frontend polls this every few seconds to colour the
// active-mcp chips by health. Empty result is normal at session start before
// the agent has called any MCP tools.
//
// GET /api/investigations/{id}/mcp-health
func (a *apiHandlers) handleMCPHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.manager.Get(id) == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"servers": a.mcpHealth.snapshot(id),
	})
}

// handleMCPProbe runs lightweight readiness checks against the per-session
// MCP servers' dependencies and returns one result per registered alias.
// Independent of the passive telemetry tracking — answers "are these
// servers' dependencies actually reachable right now?" rather than "has
// the agent talked to them recently?". Used by the frontend chip
// classification to distinguish idle-but-healthy from broken.
//
// GET /api/investigations/{id}/mcp-probe
func (a *apiHandlers) handleMCPProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	results := runMCPProbes(r.Context(), inv, a.opts)
	writeJSON(w, http.StatusOK, map[string]any{"servers": results})
}

// handleToolEvent receives tool-call telemetry from triagent-mcp. The launcher
// is the source of truth for tool activity in the activity panel; we
// trust this endpoint, not parsed claude stdout, because triagent-mcp wraps
// every tool handler and reports the actual call boundaries.
//
// POST /api/internal/tool-events  body: toolEventRequest
// Auth: Bearer launch token (matches the cookie token).
func (a *apiHandlers) handleToolEvent(w http.ResponseWriter, r *http.Request) {
	if a.telemetryToken == "" {
		writeError(w, http.StatusServiceUnavailable, "telemetry not configured")
		return
	}
	if !checkBearer(r, a.telemetryToken) {
		writeError(w, http.StatusUnauthorized, "invalid telemetry token")
		return
	}
	var body toolEventRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Trace id is whatever the launcher (us) put in env at preflight or
	// editor-session start. We mint two id namespaces (investigation,
	// editor) and they don't collide; check the investigation manager
	// first since that's the hot path, then fall through to the editor
	// manager.
	if inv := a.manager.Get(body.TraceID); inv != nil {
		switch body.Phase {
		case "start":
			a.mcpHealth.recordStart(body.TraceID, body.ToolName)
			inv.publish(EventEnvelope{
				Kind:         envKindToolUse,
				ToolID:       body.ToolID,
				ToolName:     body.ToolName,
				ToolInput:    body.Input,
				ParentToolID: body.ParentToolID,
			})
		case "end":
			a.mcpHealth.recordEnd(body.TraceID, body.ToolName, body.ErrorText)
			text := body.Result
			if body.ErrorText != "" {
				text = body.ErrorText
			}
			inv.publish(EventEnvelope{
				Kind:         envKindToolResult,
				ToolID:       body.ToolID,
				Text:         text,
				ParentToolID: body.ParentToolID,
			})
		case "status":
			inv.publish(EventEnvelope{
				Kind:         envKindToolStatus,
				ParentToolID: body.ParentToolID,
				Text:         body.Result,
			})
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown phase %q", body.Phase))
			return
		}
		// On a successful draft_pr or create_github_issue end-event,
		// persist a CodefixProposalPayload to disk keyed by the
		// proposal_id the MCP minted. The repos page's activity panel
		// reads these. Best-effort: failures are logged but never
		// block the agent.
		if body.Phase == "end" && body.ErrorText == "" {
			if isDraftPRToolName(body.ToolName) {
				a.persistCodefixProposal(body.TraceID, body.Result)
			} else if isCreateGithubIssueToolName(body.ToolName) {
				a.persistCodefixIssue(body.TraceID, body.Result)
			}
		}

		w.WriteHeader(http.StatusAccepted)
		return
	}
	if a.editorMgr != nil {
		if sess := a.editorMgr.Get(body.TraceID); sess != nil {
			switch body.Phase {
			case "start":
				sess.PublishToolEvent(editor.Event{
					Kind:         editor.KindToolUse,
					ToolID:       body.ToolID,
					ToolName:     body.ToolName,
					ToolInput:    body.Input,
					ParentToolID: body.ParentToolID,
				})
			case "end":
				text := body.Result
				if body.ErrorText != "" {
					text = body.ErrorText
				}
				sess.PublishToolEvent(editor.Event{
					Kind:         editor.KindToolResult,
					ToolID:       body.ToolID,
					Text:         text,
					ParentToolID: body.ParentToolID,
				})
			case "status":
				sess.PublishToolEvent(editor.Event{
					Kind:         editor.KindToolStatus,
					ParentToolID: body.ParentToolID,
					Text:         body.Result,
				})
			default:
				writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown phase %q", body.Phase))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	writeError(w, http.StatusNotFound, "trace id not found in any active session")
}

type toolEventRequest struct {
	Phase        string         `json:"phase"`
	TraceID      string         `json:"traceId"`
	ToolID       string         `json:"toolId"`
	ToolName     string         `json:"toolName,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	Result       string         `json:"result,omitempty"`
	ErrorText    string         `json:"errorText,omitempty"`
	ParentToolID string         `json:"parentToolId,omitempty"`
}

// checkBearer compares the request's Authorization header against the
// expected token. Constant-time comparison since the token is small and
// shared across triagent-mcp processes for the same launch.
func checkBearer(r *http.Request, expected string) bool {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := h[len(prefix):]
	if len(got) != len(expected) {
		return false
	}
	var diff byte
	for i := 0; i < len(got); i++ {
		diff |= got[i] ^ expected[i]
	}
	return diff == 0
}

// handleStartSession kicks off the claude session for an investigation.
// 202 Accepted on success — the actual streaming runs in a goroutine and
// is consumed via /events. POST /api/investigations/{id}/start
func (a *apiHandlers) handleStartSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	if err := inv.Start(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleMessage submits a follow-up prompt to an in-progress session.
// 202 Accepted; new events will arrive on the SSE stream. The user's
// prompt is also published as a synthetic "user" envelope so the
// transcript reflects what was asked.
//
// POST /api/investigations/{id}/messages  body: {"text": "..."}
func (a *apiHandlers) handleMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
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

	// Rehydrate first if this investigation came back from disk and
	// hasn't been re-attached in this process. The rehydrate path
	// publishes its own rehydrate_state envelopes; we just translate
	// the error to the right HTTP status.
	inv.mu.Lock()
	needs := inv.needsRehydrate
	inv.mu.Unlock()
	if needs {
		if err := a.rehydrate(inv); err != nil {
			switch {
			case errors.Is(err, ErrRehydrating):
				writeError(w, http.StatusConflict, err.Error())
			case errors.Is(err, ErrArchived):
				writeError(w, http.StatusConflict, err.Error())
			default:
				writeError(w, http.StatusServiceUnavailable, "rehydrate failed: "+err.Error())
			}
			return
		}
	}

	if err := inv.SendFollowUp("human", body.Text); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleInterrupt cancels the in-flight claude turn for this
// investigation. The frontend swaps the composer's Send button for a
// Stop button while streaming; clicking it posts here.
//
// POST /api/investigations/{id}/interrupt
// 202 — interrupt requested; drain will emit the breadcrumb + end envelopes.
// 404 — unknown investigation.
// 409 — no turn currently in flight.
func (a *apiHandlers) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	if err := inv.Interrupt(); err != nil {
		if errors.Is(err, ErrNotStreaming) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ── DTOs / helpers ──────────────────────────────────────────────────────────

type clusterDTO struct {
	Name   string            `json:"name"`
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels,omitempty"`
}

type preflightRequest struct {
	// Inputs is a map keyed by InvestigationInput.ID (as defined in the
	// profile) to a field map for that input. For text/url/textarea/cluster_id
	// inputs the field map contains {"value": "<string>"}. For slack_channel
	// inputs it contains {"id": "...", "name": "...", "url": "..."}.
	// Validated against the profile's InvestigationInputs schema before use.
	Inputs map[string]map[string]any `json:"inputs"`
	// Prom carries per-investigation Prometheus overrides. Each non-zero
	// field overlays the profile's defaults; fields left at zero inherit
	// the profile value. Disabled=true skips the prom MCP entirely.
	Prom *promOverrideBody `json:"prom,omitempty"`
	// Auto, when true, spawns the auto-mode operator agent (separate
	// claude session) after the investigation is registered. The
	// preflight HTTP response does NOT block on the operator's first
	// turn — EnableAuto runs in a goroutine. Operator-side failures are
	// logged to stderr and the investigation continues in manual mode.
	Auto bool `json:"auto,omitempty"`
}

// promOverrideBody is the per-investigation Prometheus override received
// from the browser form. Each field is optional; zero values inherit the
// active profile's defaults.prometheus. Disabled=true skips the prom MCP
// entirely for this investigation, regardless of profile defaults.
type promOverrideBody struct {
	Service   string `json:"service,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Port      int    `json:"port,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
}

// resolvePromTarget applies per-investigation prom overrides on top of
// profile defaults using field-level overlay semantics:
//   - If override.Disabled is true → return nil, true (skip prom MCP).
//   - Otherwise, start from the profile defaults and overwrite each
//     field where the override is non-zero.
//   - The result is only returned non-nil when all three fields
//     (service, namespace, port>0) are populated — a partial target
//     would attach the prom MCP only for resolveTarget to fail later
//     (e.g. "get service /name: no namespace specified"). Returning
//     nil instead leaves the agent in the no-prom branch with a clean
//     "catalog empty" reply rather than a service-lookup error.
//
// Returns (target, disabled) where target is nil when no complete
// target is configured.
func resolvePromTarget(prof *profile.Profile, override *promOverrideBody) (*promforward.Target, bool) {
	if override != nil && override.Disabled {
		return nil, true
	}
	// Start from profile defaults (may be zero-value if no profile).
	var target promforward.Target
	if prof != nil {
		target.Service = prof.Defaults.Prometheus.Service
		target.Namespace = prof.Defaults.Prometheus.Namespace
		target.Port = prof.Defaults.Prometheus.Port
	}
	// Field-level overlay: non-zero values in the override win.
	if override != nil {
		if override.Service != "" {
			target.Service = override.Service
		}
		if override.Namespace != "" {
			target.Namespace = override.Namespace
		}
		if override.Port > 0 {
			target.Port = override.Port
		}
	}
	if target.Service == "" || target.Namespace == "" || target.Port <= 0 {
		return nil, false
	}
	return &target, false
}

// handleProfilePromDefaults returns the active profile's prometheus defaults
// so the frontend can pre-populate the per-investigation prom override form.
//
// GET /api/profile/prom-defaults
func (a *apiHandlers) handleProfilePromDefaults(w http.ResponseWriter, r *http.Request) {
	if a.prof == nil {
		writeError(w, http.StatusServiceUnavailable, "no profile configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":   a.prof.Defaults.Prometheus.Service,
		"namespace": a.prof.Defaults.Prometheus.Namespace,
		"port":      a.prof.Defaults.Prometheus.Port,
	})
}

// handleListRepos returns the resolved defaults (embedded or file-backed)
// + the user-managed file for the picker UI. The UI uses these to show
// "default (locked)" vs "user (deletable)" distinctions.
//
// GET /api/repos
func (a *apiHandlers) handleListRepos(w http.ResponseWriter, r *http.Request) {
	user, err := repos.LoadUserRepos(a.opts.UserReposPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load user repos: %v", err))
		return
	}
	defs := repos.ProfileDefaults(a.opts.Profile)
	if defs == nil {
		defs = []repos.LinkedRepo{}
	}
	if user == nil {
		user = []repos.LinkedRepo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"defaults": defs, "user": user})
}

// handleAddRepo persists a new entry to the user-repos file. Rejects entries
// that duplicate a default. Empty UserReposPath disables this endpoint.
//
// POST /api/repos  body: {"owner": "...", "name": "...", "alias": "...", "description": "..."}
//
// description is REQUIRED (>= repos.MinDescriptionLength chars after trim).
// It's rendered into the investigation prompt's Linked repositories section
// and serves as fallback orientation when no architecture summary has been
// generated for this repo yet, so a missing or one-letter description
// degrades the agent's first-stop context.
func (a *apiHandlers) handleAddRepo(w http.ResponseWriter, r *http.Request) {
	if a.opts.UserReposPath == "" {
		writeError(w, http.StatusServiceUnavailable, "user repos file not configured")
		return
	}
	var body struct {
		Owner       string `json:"owner"`
		Name        string `json:"name"`
		Alias       string `json:"alias,omitempty"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Owner == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}
	for _, d := range repos.ProfileDefaults(a.opts.Profile) {
		if d.Owner == body.Owner && d.Name == body.Name {
			writeError(w, http.StatusConflict, fmt.Sprintf("%s/%s is a default repo", body.Owner, body.Name))
			return
		}
	}
	repo := repos.LinkedRepo{
		Owner:       body.Owner,
		Name:        body.Name,
		Alias:       body.Alias,
		Description: body.Description,
	}
	// AddUserRepo enforces the description-required rule (and structural
	// repo validation). Surface its message verbatim so the operator sees
	// the same "min N chars" hint regardless of whether they hit the API
	// directly or via the manage-repos UI.
	if err := repos.AddUserRepo(a.opts.UserReposPath, repo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Kick off auto-gen of the architecture summary in the background. Don't
	// block the HTTP response on it — the worker spawns its own goroutine
	// using a long-lived context (manager.ParentContext via the
	// generationContext seam), so client disconnect / window close doesn't
	// cancel mid-flight.
	if a.architectureWorker != nil {
		ctx := context.Background()
		if a.generationContext != nil {
			ctx = a.generationContext()
		}
		a.architectureWorker.GenerateAsync(ctx, repo.Owner, repo.Name, repos.GenerateRequest{
			Kind:        "freeform",
			Description: repo.Description,
		})
	}
	// Wrap the LinkedRepo in a small envelope that carries
	// summaryStatus when auto-gen has been kicked off. Frontend reads
	// summaryStatus to know whether to show a "generating…" inline hint
	// after the modal closes — without it, an auto-add would land
	// silently and the operator wouldn't know to wait for the toast.
	type addRepoResponse struct {
		repos.LinkedRepo
		SummaryStatus string `json:"summaryStatus,omitempty"`
	}
	status := ""
	if a.architectureWorker != nil {
		status = "pending"
	}
	writeJSON(w, http.StatusCreated, addRepoResponse{
		LinkedRepo:    repo,
		SummaryStatus: status,
	})
}

// handleDeleteRepo removes an entry from the user-repos file. Defaults are
// not deletable (409). Missing entries return 404.
//
// DELETE /api/repos/{owner}/{name}
func (a *apiHandlers) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	if a.opts.UserReposPath == "" {
		writeError(w, http.StatusServiceUnavailable, "user repos file not configured")
		return
	}
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	for _, d := range repos.ProfileDefaults(a.opts.Profile) {
		if d.Owner == owner && d.Name == name {
			writeError(w, http.StatusConflict, fmt.Sprintf("%s/%s is a default repo and cannot be deleted", owner, name))
			return
		}
	}
	if err := repos.RemoveUserRepo(a.opts.UserReposPath, owner, name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// makeSessionDir creates a writable, per-investigation directory under the
// sessions root. Mirrors cmd/start.go's makeSessionDir but takes the root
// from server Options so the web flow matches the TUI flow.
func makeSessionDir(root string) (string, error) {
	dir := filepath.Join(root, "session-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// handleSetLabelFromMCP receives label updates from the triagent-meta MCP.
// Bearer-auth via the same telemetry token triagent-mcp uses for tool-event
// posts. The agent reaches here through Manager.SetLabel, which
// validates + persists + broadcasts.
//
// POST /api/internal/investigations/{id}/label
// Body: { "label": string }
// Auth: Bearer telemetry token.
func (a *apiHandlers) handleSetLabelFromMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.telemetryToken == "" {
		writeError(w, http.StatusServiceUnavailable, "telemetry not configured")
		return
	}
	if !checkBearer(r, a.telemetryToken) {
		writeError(w, http.StatusUnauthorized, "invalid telemetry token")
		return
	}
	a.applyLabelUpdate(w, r)
}

// handleRenameInvestigation is the public, cookie-auth'd rename surface
// for the operator's pencil-icon UI. Same payload + manager call as the
// internal endpoint.
//
// POST /api/investigations/{id}/label
// Body: { "label": string }
// Auth: launcher cookie session (handled by the existing middleware).
func (a *apiHandlers) handleRenameInvestigation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.applyLabelUpdate(w, r)
}

// applyLabelUpdate reads {label} from the body, calls Manager.SetLabel,
// and returns the updated snapshot on success. Shared by both auth
// surfaces so they can't disagree on validation or persistence
// semantics.
func (a *apiHandlers) applyLabelUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := a.manager.SetLabel(id, body.Label); err != nil {
		// Validation errors → 400; not-found → 404.
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	writeJSON(w, http.StatusOK, inv.Snapshot())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}
