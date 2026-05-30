package server

import (
	"os"
	"path/filepath"
	"time"
)

// SyncStatus is the high-level "is this resource on origin/HEAD as well
// as locally?" answer. Designed to be uniform across resource kinds
// (playbooks today; sessions and wiki next) so frontend rendering can
// switch on this value without per-kind branching.
type SyncStatus string

const (
	// SyncStatusSynced — the local copy matches upstream after
	// canonicalisation. The kind-specific resolver decides what
	// "matches" means (e.g. playbookBodiesMatch's strip rules).
	SyncStatusSynced SyncStatus = "synced"

	// SyncStatusLocalDrift — both sides exist but the local copy has
	// edits that aren't yet on origin. Includes operator tombstones
	// (active=false on a still-active upstream entry).
	SyncStatusLocalDrift SyncStatus = "local-drift"

	// SyncStatusLocalOnly — local exists, upstream doesn't. A freshly
	// authored resource the operator hasn't pushed yet.
	SyncStatusLocalOnly SyncStatus = "local-only"

	// SyncStatusUpstreamOnly — upstream exists, local doesn't. A peer
	// authored it; this launcher hasn't created a local override.
	// Surfaced so the UI can offer an "import" affordance rather than
	// hiding entries the operator could be using.
	SyncStatusUpstreamOnly SyncStatus = "upstream-only"

	// SyncStatusClosed — only meaningful for resources that flow
	// through a PR (sessions). The PR closed without merging, so the
	// resource never landed on main. Kept distinct from "local-drift"
	// so the UI can prompt re-push instead of "you have local edits".
	SyncStatusClosed SyncStatus = "closed"

	// SyncStatusUnknown — couldn't determine. Plugin clone missing,
	// upstream dir not configured, the resolver hit an unexpected
	// state. Treated as "don't show a sync indicator" by the UI; the
	// alternative of showing a stale "synced" badge is worse.
	SyncStatusUnknown SyncStatus = "unknown"
)

// SyncState is the full payload the API returns. Reason is human-readable
// (used for tooltip text); the structured fields drive rendering decisions.
//
// PR is populated for resources that flow through a pull request
// (sessions today; future: anything else that gets its own PR thread).
// Playbooks don't currently track PR state per-id — their PR creation
// is fire-and-forget — so the field stays nil for them. Optional and
// omitempty so per-kind callers don't have to think about it.
type SyncState struct {
	Status SyncStatus    `json:"status"`
	Reason string        `json:"reason,omitempty"`
	PR     *SyncStatePR  `json:"pr,omitempty"`
}

// SyncStatePR is the structured PR sidecar embedded in SyncState. The
// frontend renders state-coloured pills (open/merged/closed) and a
// relative-time tooltip from these fields, so they're shipped pre-stringified
// (RFC3339) rather than as Go time.Time — keeps the wire format
// timezone-explicit and lets the frontend do its own relative-time math.
type SyncStatePR struct {
	URL      string `json:"url"`
	State    string `json:"state"`              // "open" | "merged" | "closed"
	MergedAt string `json:"mergedAt,omitempty"`
	ClosedAt string `json:"closedAt,omitempty"`
}

// playbookSyncStateOpts is the handler-facing input shape. Separating
// it from positional args makes it cheap to add fields (e.g. base SHA
// for diff-against-a-specific-commit) without rippling through callers.
type playbookSyncStateOpts struct {
	ID string

	// TypeName is the directory the file lives under (investigation,
	// general, …). When empty, the resolver looks it up from the
	// user-side group.
	TypeName string

	// OverrideYAML lets a caller (the editor's preview endpoint) ask
	// "would *this* body be a no-op push?" without saving first. Empty
	// means "use the saved user-dir file".
	OverrideYAML string
}

// playbookSyncState is the disk-loading entry point for the editor's
// HTTP preview endpoint. It reads:
//   - upstream YAML from the plugin clone (a.opts.PluginPlaybooksDir)
//   - user YAML from the user dir (a.opts.UserPlaybooksDir), unless
//     OverrideYAML is supplied (editor preview path)
//
// and delegates to playbookSyncStateFor for the actual classification.
// The list endpoint (collectPlaybooks Pass 2) calls
// playbookSyncStateFor directly with the inputs it has already
// loaded — avoids a second walk of the user dir per id and a second
// read of the upstream file.
//
// Duplicating the classification logic anywhere else is the bug class
// we keep hitting (FE/BE strip-list drift); every "is this drifted?"
// path must end up at playbookSyncStateFor. Resist the urge.
//
// Locked / system-tier ids short-circuit to SyncStatusSynced — they
// ship with the binary, the notion of "drifted from upstream" doesn't
// apply. NOTE: locked detection requires a populated metaCache; if the
// in-process catalog load (loadMeta) failed at startup, isLockedID
// returns false even for system ids and they fall through to the
// regular compare path. The
// list endpoint hides the inconsistency (a nil cache produces zero
// Pass-1 entries), but a direct sync-state call for a locked id in
// that degraded state will return SyncStatusUnknown — covered by
// TestPlaybookSyncState_LockedNeedsMetaCache.
func (a *apiHandlers) playbookSyncState(o playbookSyncStateOpts) SyncState {
	if o.ID == "" {
		return SyncState{Status: SyncStatusUnknown, Reason: "missing id"}
	}
	if a.isLockedID(o.ID) {
		return SyncState{Status: SyncStatusSynced, Reason: "ships with the launcher"}
	}

	// Resolve user-side YAML and (if not provided) type from the user
	// dir. Active is only meaningful when comparing the saved file —
	// an OverrideYAML preview is comparing the body the editor would
	// push, regardless of the on-disk active flag.
	var userYAML string
	userActive := true
	typeName := o.TypeName
	isOverride := o.OverrideYAML != ""
	if isOverride {
		userYAML = o.OverrideYAML
	} else {
		groups, _ := loadUserPlaybookGroups(a.opts.UserPlaybooksDir)
		if g, ok := groups[o.ID]; ok {
			userYAML = g.YAML
			userActive = g.Active
			if typeName == "" {
				typeName = g.Type
			}
		}
	}

	// Read upstream side. Missing plugin dir or missing file is fine —
	// playbookSyncStateFor maps an empty upstream to local-only /
	// unknown as appropriate.
	upstreamYAML := readUpstreamPlaybookYAML(a.opts.PluginPlaybooksDir, typeName, o.ID)

	return playbookSyncStateFor(userYAML, userActive, upstreamYAML, isOverride)
}

// playbookSyncStateFor is the pure classification function: given the
// already-loaded user/upstream YAML for an id, return the SyncState.
// Split out so collectPlaybooks Pass 2 can call it directly with the
// inputs it has already loaded (avoiding the inner re-walk that the
// handler-facing playbookSyncState performs). Treat as the single
// source of truth for the classification rules — every code path that
// asks "is this drifted?" must end up here.
//
// isOverride controls the active-tombstone branch: an editor preview
// (override yaml supplied) compares the body the operator would push
// regardless of the on-disk active flag, so the tombstone-as-drift
// rule only applies when classifying a saved file.
func playbookSyncStateFor(userYAML string, userActive bool, upstreamYAML string, isOverride bool) SyncState {
	switch {
	case userYAML == "" && upstreamYAML == "":
		return SyncState{Status: SyncStatusUnknown, Reason: "no record locally or upstream"}
	case userYAML == "" && upstreamYAML != "":
		return SyncState{Status: SyncStatusUpstreamOnly, Reason: "upstream-only — no local override"}
	case userYAML != "" && upstreamYAML == "":
		return SyncState{Status: SyncStatusLocalOnly, Reason: "not yet on upstream"}
	case !isOverride && !userActive:
		// Operator disabled the upstream entry locally. The "off"
		// choice is real drift — push as a tombstone if they want it
		// reflected on origin.
		return SyncState{Status: SyncStatusLocalDrift, Reason: "disabled locally"}
	case playbookBodiesMatch(userYAML, upstreamYAML):
		return SyncState{Status: SyncStatusSynced, Reason: "matches upstream"}
	default:
		return SyncState{Status: SyncStatusLocalDrift, Reason: "local edits not on upstream"}
	}
}

// sessionSyncStateInputs is the structured input shape for
// sessionSyncStateFor. Lifting these out keeps the classifier signature
// stable as we add fields (e.g. branch ahead/behind for stage 3).
type sessionSyncStateInputs struct {
	// HasLocal — does this launcher know about an Investigation for
	// this id/slug? False means "upstream-only entry from someone
	// else's machine; the operator hasn't adopted it locally".
	HasLocal bool

	// Pushed reflects Investigation.PushedAt != nil. After
	// ReconcileUpstreamPushed runs, this is also true for sessions
	// whose slug exists in the upstream clone (i.e. peer-pushed and
	// we synced it down). Treated as the unified "is this on
	// upstream?" signal.
	Pushed bool

	// PushURL is the PR URL set by the launcher's own push flow.
	// Empty when the session was reconciled from an upstream
	// session.md without a known PR (e.g. peer push). The classifier
	// suppresses the PR sidecar in that case rather than emitting an
	// empty pill.
	PushURL string

	// PRState mirrors RefreshPRStates' result: "open"/"merged"/"closed"
	// or "" when unknown. Drives the synced/closed split — a closed
	// PR is its own status because the session.md never landed on
	// main, so the operator should re-push, not just see drift.
	PRState string

	PRMergedAt *time.Time
	PRClosedAt *time.Time
}

// sessionSyncStateFor classifies a session's relationship to upstream.
// Pure: every input is explicit. Mirrors playbookSyncStateFor's role —
// the single oracle every "is this session synced?" caller must end up
// at, regardless of which view it's serving.
//
// Status decisions:
//   - !HasLocal             → upstream-only (peer-authored; not yet
//                             adopted on this machine)
//   - PRState == "closed"   → closed (PR closed without merging; UI
//                             prompts re-push — distinct from drift)
//   - HasLocal && !Pushed   → local-only (operator authored; never
//                             pushed)
//   - otherwise             → synced (on upstream as PR-open / merged
//                             / reconciled-from-clone)
//
// PR sidecar is set whenever PushURL is known. ReconcileUpstreamPushed
// flips Pushed without a PushURL (it doesn't have the PR reference)
// — those entries report "synced" without a PR pill, which is honest:
// the session.md is on origin/HEAD, the PR thread is just unknown to
// this launcher.
func sessionSyncStateFor(in sessionSyncStateInputs) SyncState {
	if !in.HasLocal {
		return SyncState{
			Status: SyncStatusUpstreamOnly,
			Reason: "upstream session not yet adopted locally",
		}
	}
	if in.PRState == PRStateClosed {
		return SyncState{
			Status: SyncStatusClosed,
			Reason: "PR closed without merging — push again to share",
			PR:     sessionPRSidecar(in),
		}
	}
	if !in.Pushed {
		return SyncState{
			Status: SyncStatusLocalOnly,
			Reason: "not yet pushed to upstream",
		}
	}
	return SyncState{
		Status: SyncStatusSynced,
		Reason: "on upstream",
		PR:     sessionPRSidecar(in),
	}
}

// sessionPRSidecar builds the optional PR pill payload from the
// classifier's inputs. Returns nil when there's no PR URL — a session
// reconciled from an upstream session.md without a PR reference (peer
// push) reports synced but doesn't synthesise an empty PR object.
func sessionPRSidecar(in sessionSyncStateInputs) *SyncStatePR {
	if in.PushURL == "" {
		return nil
	}
	pr := &SyncStatePR{URL: in.PushURL, State: in.PRState}
	if in.PRMergedAt != nil {
		pr.MergedAt = in.PRMergedAt.UTC().Format(time.RFC3339)
	}
	if in.PRClosedAt != nil {
		pr.ClosedAt = in.PRClosedAt.UTC().Format(time.RFC3339)
	}
	return pr
}

// wikiSyncStateFor is the binary classifier for wiki resources. Wiki
// drift is path-based: `git diff --name-only origin/HEAD HEAD` already
// answers "do my local commits include changes to this file that
// aren't on origin yet?". The classifier collapses to two states —
// any presence in the diff (added or modified) is local-drift; absence
// is synced. Distinguishing local-only from local-drift would require
// an extra git call per file and yields the same UX answer ("push to
// share"); not worth the I/O.
//
// Caller passes `pathInDiff` from a single batched
// unsyncedWikiFiles() lookup, so each list response runs git once and
// classifies N files in memory.
func wikiSyncStateFor(pathInDiff bool) SyncState {
	if pathInDiff {
		return SyncState{
			Status: SyncStatusLocalDrift,
			Reason: "local commits not yet on upstream",
		}
	}
	return SyncState{
		Status: SyncStatusSynced,
		Reason: "matches upstream",
	}
}

// readUpstreamPlaybookYAML reads the upstream copy of <pluginDir>/<type>/<id>.yaml
// or returns "" when missing/unconfigured. Callers must have already
// validated typeName + id for path safety; the resolver's HTTP
// entrypoints do this via playbookIDPattern / typePattern. The list
// endpoint feeds typeName from loadUserPlaybookGroups (directory walk
// with typePattern + playbookIDPattern filters), so it's safe by
// construction.
func readUpstreamPlaybookYAML(pluginDir, typeName, id string) string {
	if pluginDir == "" || typeName == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(pluginDir, typeName, id+".yaml"))
	if err != nil {
		return ""
	}
	return string(data)
}
