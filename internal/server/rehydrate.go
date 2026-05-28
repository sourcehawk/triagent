package server

import (
	"fmt"

	"github.com/sourcehawk/triagent/internal/preflight"
	"github.com/sourcehawk/triagent/internal/sessions"
)

// rehydrate runs preflight against the current external state (launcher
// repo list, connection tokens) and rebuilds the live claude/MCP wiring
// with the persisted ClaudeSessionID injected so the next call uses
// --resume <id>.
//
// External-state fields — LinkedRepos, SlackMCPEnabled,
// IncidentioMCPEnabled — are re-derived from current launcher /
// connections state rather than read off the persisted Investigation,
// so config drift since the session was created is honoured.
//
// Returns nil on success. On error, the corresponding rehydrate_state
// envelopes are already published; the caller should map the error to
// the right HTTP status and return.
//
// Concurrency: the lock is held only across the small state mutations.
// Slow IO (preflight, port-forward) runs with the lock released. The
// rehydrating flag (set under the lock) prevents parallel calls.
func (a *apiHandlers) rehydrate(inv *Investigation) error {
	inv.mu.Lock()
	if inv.rehydrating {
		inv.mu.Unlock()
		return ErrRehydrating
	}
	if inv.archived {
		inv.mu.Unlock()
		return ErrArchived
	}
	if !inv.needsRehydrate {
		inv.mu.Unlock()
		return nil // already rehydrated; nothing to do
	}
	inv.rehydrating = true
	ctx := inv.ctx
	inv.mu.Unlock()

	inv.publishRehydrateState(RehydrateStatePayload{Phase: "started"})

	// Helper to bail out symmetrically.
	finishFailure := func(err error, degraded []string) error {
		inv.mu.Lock()
		inv.rehydrating = false
		inv.mu.Unlock()
		inv.publishRehydrateState(RehydrateStatePayload{
			Phase:    "failed",
			Error:    err.Error(),
			Degraded: degraded,
		})
		return err
	}

	// Re-derive external-state from the launcher and connections manager
	// instead of trusting the persisted Investigation. The session's prom
	// config is persisted in metadata.json (PromTarget + PromDisabled) and
	// read back via loadInvestigation, so we pass it through from inv.
	linked, err := a.resolvedLinkedRepos()
	if err != nil {
		return finishFailure(fmt.Errorf("resolve linked repos: %w", err), nil)
	}
	var slackTok, ioTok string
	if a.connections != nil {
		slackTok, _ = a.connections.GetSlackToken()
		ioTok, _ = a.connections.GetIncidentioToken()
	}

	inv.mu.Lock()
	opts := preflight.Options{
		Provider:           a.opts.Provider,
		Ctx:                ctx,
		Namespace:          inv.Namespace,
		SessionDir:         inv.SessionDir,
		MCPBinaryPath:      a.opts.MCPBinaryPath,
		DocsServerName:     a.opts.DocsServerName,
		LinkedRepos:        linked,
		GitCacheDir:        a.opts.GitCacheDir,
		UserPlaybooksDir:   a.opts.UserPlaybooksDir,
		PluginPlaybooksDir: a.opts.PluginPlaybooksDir,
		SystemPlaybooksDir: a.opts.SystemPlaybooksDir,
		WikiPath:           a.opts.WikiPath,
		WikiProposalsPath:  resolveWikiProposalsPath(a.opts.WikiProposalsPath),
		SlackToken:         slackTok,
		IncidentioToken:    ioTok,
		KubeconfigPath:     inv.KubeconfigPath,
		// Telemetry is a launcher-wide concern; reuse the live values.
		TelemetryURL:   a.telemetryURL,
		TraceID:        inv.ID,
		TelemetryToken: a.telemetryToken,
		Profile:        inv.Profile,
		// Prom config is persisted in metadata.json and restored via
		// loadInvestigation — pass it through so the rehydrated MCP config
		// includes the correct prom server entry.
		PromTarget:   inv.PromTarget,
		PromDisabled: inv.PromDisabled,
	}
	inv.mu.Unlock()

	res, err := a.runPreflight(opts)
	var degraded []string
	if err != nil {
		return finishFailure(err, nil)
	}

	// Build a fresh sessions.Session and inject the persisted id so
	// Resume calls --resume <id> rather than starting a new turn.
	slackEnabled := slackTok != ""
	ioEnabled := ioTok != ""

	inv.mu.Lock()
	inv.MCPConfigPath = res.MCPConfigPath
	inv.DocsPrefix = res.DocsPrefix
	if res.KubeconfigPath != "" {
		inv.KubeconfigPath = res.KubeconfigPath
	}
	inv.LinkedRepos = linked
	inv.SlackMCPEnabled = slackEnabled
	inv.IncidentioMCPEnabled = ioEnabled
	sessOpts := sessions.Options{
		Namespace:              inv.Namespace,
		UserNotes:              inv.Notes,
		IncidentURL:            inv.IncidentURL,
		SlackChannelURL:        inv.SlackChannelURL,
		SlackChannelID:         inv.SlackChannelID,
		SlackChannelName:       inv.SlackChannelName,
		MCPConfigPath:          inv.MCPConfigPath,
		SlackMCPAvailable:      slackEnabled,
		IncidentioMCPAvailable: ioEnabled,
		LinkedRepos:            linked,
		LaunchCwd:              inv.LaunchCwd,
		KubeconfigPath:         inv.KubeconfigPath,
		Profile:                inv.Profile,
	}
	priorID := inv.ClaudeSessionID
	inv.mu.Unlock()

	sess, err := a.buildSession(sessOpts, priorID)
	if err != nil {
		return finishFailure(fmt.Errorf("build session: %w", err), degraded)
	}

	inv.mu.Lock()
	inv.session = sess
	inv.started = true
	inv.needsRehydrate = false
	inv.rehydrating = false
	inv.mu.Unlock()

	if err := a.manager.persistMetadata(inv); err != nil {
		// Persistence is best-effort; log but don't roll back the
		// in-memory state — the operator's prompt should still go
		// through.
		_ = err
	}
	inv.publishRehydrateState(RehydrateStatePayload{Phase: "succeeded", Degraded: degraded})
	return nil
}

// runPreflight calls a.preflightFn when set (test seam) or
// preflight.Run otherwise.
func (a *apiHandlers) runPreflight(opts preflight.Options) (*preflight.Result, error) {
	if a.preflightFn != nil {
		return a.preflightFn(opts)
	}
	return preflight.Run(opts)
}

// buildSession calls a.sessionFn when set (test seam) or builds a real
// sessions.Session and restores priorID on it otherwise. Centralising
// the restore keeps the production and test paths producing
// session-id-equivalent investigationSessions.
func (a *apiHandlers) buildSession(opts sessions.Options, priorID string) (investigationSession, error) {
	if a.sessionFn != nil {
		return a.sessionFn(opts, priorID)
	}
	sess, err := sessions.New(opts)
	if err != nil {
		return nil, err
	}
	sess.RestoreSessionID(priorID)
	return sess, nil
}

