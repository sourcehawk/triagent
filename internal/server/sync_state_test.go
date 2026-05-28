package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// playbookSyncState is the single source of truth for "is this user
// playbook drifted from upstream?". The list endpoint, the editor's
// push-PR preview, and any future audit tool should all flow through
// this — see refactor doc / CHANGELOG. Each branch below pins one of
// the SyncStatus values to a concrete scenario so behaviour can't
// silently change.
//
// Scenario fixtures use a minimal investigation/<id>.yaml shape;
// playbookBodiesMatch handles the YAML normalisation (active /
// version / type stripping) so these tests don't have to.

func newSyncStateAPI(t *testing.T) *apiHandlers {
	t.Helper()
	return &apiHandlers{
		opts: Options{
			UserPlaybooksDir:   t.TempDir(),
			PluginPlaybooksDir: t.TempDir(),
		},
		manager:   NewManager(context.Background(), t.TempDir()),
		metaCache: &metaCache{},
	}
}

func writeYAML(t *testing.T, dir, typeName, id, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, typeName), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, typeName, id+".yaml"), []byte(body), 0o644))
}

const fixturePlaybookYAML = `id: foo
symptom: x
entrypoint: n
nodes:
  n: {description: d}
`

func TestPlaybookSyncState_SyncedWhenUserMatchesUpstream(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)
	// Upstream copy carries the post-push stamps the user side strips —
	// active and (in the broken-before-our-fix world) type. Both flow
	// through playbookBodiesMatch's strip list, so canonically equal.
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo",
		"active: true\n"+fixturePlaybookYAML+"type: investigation\n")

	state := a.playbookSyncState(playbookSyncStateOpts{ID: "foo"})
	assert.Equal(t, SyncStatusSynced, state.Status, "user file matches upstream after canonicalisation")
}

func TestPlaybookSyncState_LocalDriftWhenContentDiffers(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo",
		`id: foo
symptom: x
entrypoint: n
nodes:
  n: {description: edited locally}
`)
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)

	state := a.playbookSyncState(playbookSyncStateOpts{ID: "foo"})
	assert.Equal(t, SyncStatusLocalDrift, state.Status, "user content differs from upstream")
}

func TestPlaybookSyncState_LocalOnlyForFreshlyAuthored(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)
	// No upstream copy at all — operator just authored this.

	state := a.playbookSyncState(playbookSyncStateOpts{ID: "foo"})
	assert.Equal(t, SyncStatusLocalOnly, state.Status, "no upstream counterpart yet")
}

func TestPlaybookSyncState_UpstreamOnlyWhenUserHasntOverridden(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)
	// No user file — operator hasn't created a local override.

	state := a.playbookSyncState(playbookSyncStateOpts{ID: "foo", TypeName: "investigation"})
	assert.Equal(t, SyncStatusUpstreamOnly, state.Status, "upstream-only entries are reported, not silently synced")
}

func TestPlaybookSyncState_LocalDriftWhenDisabledTombstone(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	// Disabled user override of an upstream playbook — the operator's
	// "off" choice isn't reflected on origin/HEAD, so it's a real
	// drift the badge should surface.
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo",
		"active: false\n"+fixturePlaybookYAML)
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)

	state := a.playbookSyncState(playbookSyncStateOpts{ID: "foo"})
	assert.Equal(t, SyncStatusLocalDrift, state.Status, "disabled tombstone counts as drift")
}

func TestPlaybookSyncState_OverrideYAMLLetsEditorPreviewUnsavedDraft(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	// Saved user file matches upstream, but the operator has typed
	// something new in the editor. The override yaml is what we need
	// to compare against upstream — simulating the editor's "would my
	// draft create a real PR diff?" question.
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)

	override := `id: foo
symptom: x
entrypoint: n
nodes:
  n: {description: brand new prose}
`
	state := a.playbookSyncState(playbookSyncStateOpts{
		ID:           "foo",
		TypeName:     "investigation",
		OverrideYAML: override,
	})
	assert.Equal(t, SyncStatusLocalDrift, state.Status, "override yaml drives the comparison, not the saved file")
}

func TestPlaybookSyncState_UnknownWhenNeitherSideHasIt(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)

	state := a.playbookSyncState(playbookSyncStateOpts{ID: "ghost", TypeName: "investigation"})
	assert.Equal(t, SyncStatusUnknown, state.Status, "no record locally or upstream")
}

// Locked (system-tier) ids short-circuit to "synced" — they ship
// with the launcher binary and have no upstream counterpart that
// drift could be defined against. The shortcut requires a populated
// metaCache; with metaCache empty (e.g. dump-meta failed at startup)
// the resolver falls through to the regular compare path. Pinning
// both branches so a future change can't silently break either.
func TestPlaybookSyncState_LockedRequiresMetaCache(t *testing.T) {
	t.Parallel()

	t.Run("locked id with populated cache returns Synced", func(t *testing.T) {
		t.Parallel()
		a := newSyncStateAPI(t)
		a.metaCache.set(&Meta{
			Playbooks: map[string]MetaPlaybook{
				"sys-master": {Source: "system", Locked: true, YAML: fixturePlaybookYAML},
			},
		})
		state := a.playbookSyncState(playbookSyncStateOpts{ID: "sys-master"})
		assert.Equal(t, SyncStatusSynced, state.Status, "locked = synced regardless of any user/upstream file")
		assert.Contains(t, state.Reason, "ships with the launcher")
	})

	t.Run("locked id with nil cache returns Unknown — degraded state", func(t *testing.T) {
		t.Parallel()
		// Same locked id, but no metaCache.set call — simulates the
		// dump-meta-failed-at-startup degraded state. isLockedID
		// returns false, the regular compare path runs, finds no
		// user file, no upstream file, lands on Unknown.
		a := newSyncStateAPI(t)
		state := a.playbookSyncState(playbookSyncStateOpts{ID: "sys-master"})
		assert.Equal(t, SyncStatusUnknown, state.Status, "without metaCache there is no way to know an id is locked")
	})
}

// Empty id rejects with Unknown — the resolver is defensive against
// callers that bypass the HTTP handler's id validation.
func TestPlaybookSyncState_RejectsEmptyID(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	state := a.playbookSyncState(playbookSyncStateOpts{ID: ""})
	assert.Equal(t, SyncStatusUnknown, state.Status)
	assert.Equal(t, "missing id", state.Reason)
}

// HTTP layer for the editor's "would my draft be a no-op push?"
// preview. Editor sends the rendered yaml; server runs the same
// playbookSyncState through the override path and returns the
// SyncState. Deliberately POST: the body carries the yaml.
func TestHandlePlaybookSyncState_OverrideRoundTrip(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)

	body := strings.NewReader(`{"yaml":"id: foo\nsymptom: x\nentrypoint: n\nnodes:\n  n: {description: brand new}\n","type":"investigation"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbooks/foo/sync-state", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "foo")
	a.handlePlaybookSyncState(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	var got SyncState
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, SyncStatusLocalDrift, got.Status, "preview must compare the override yaml, not the saved file")
}

// The handler validates id and type before they hit filepath.Join in
// the resolver — defence-in-depth against a crafted request probing
// arbitrary YAML files via the synced/local-drift response. The auth
// token already gates this surface to the local operator, but unvalidated
// input is the kind of footgun that reads "minor" until it's "critical
// for the next reachable surface".
func TestHandlePlaybookSyncState_RejectsPathTraversalInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
		body string
	}{
		{
			name: "id with path traversal",
			id:   "../etc/passwd",
			body: `{}`,
		},
		{
			name: "id with separator",
			id:   "foo/bar",
			body: `{}`,
		},
		{
			name: "type with path traversal",
			id:   "foo",
			body: `{"type":"../etc"}`,
		},
		{
			name: "type with separator",
			id:   "foo",
			body: `{"type":"a/b"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newSyncStateAPI(t)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/playbooks/x/sync-state", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.SetPathValue("id", tc.id)
			a.handlePlaybookSyncState(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "must reject before hitting filepath.Join — body was %q", rec.Body)
		})
	}
}

// Empty body (no Content-Length, e.g. a chunked transfer with no
// chunks) must NOT 400 — the resolver's "use the saved-file answer"
// path is the documented behaviour for a body-less call. The earlier
// implementation only short-circuited on ContentLength == 0, leaving
// chunked empty bodies to fail json.Decode with io.EOF.
func TestHandlePlaybookSyncState_TolerateEmptyBody(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	writeYAML(t, a.opts.UserPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)
	writeYAML(t, a.opts.PluginPlaybooksDir, "investigation", "foo", fixturePlaybookYAML)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbooks/foo/sync-state", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "foo")
	a.handlePlaybookSyncState(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "empty body must yield the saved-file answer, not 400. body: %s", rec.Body)
	var got SyncState
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, SyncStatusSynced, got.Status, "saved file matches upstream")
}

// sessionSyncStateFor is the pure classifier for sessions: maps a
// local Investigation's push state and PR lifecycle (plus whether the
// upstream clone is even aware of the slug) to one of synced / closed
// / local-only / upstream-only / unknown. Same role for sessions that
// playbookSyncStateFor plays for playbooks — the single oracle every
// session-drift caller must end up at.
func TestSessionSyncStateFor_AllBranches(t *testing.T) {
	t.Parallel()
	merged := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	closed := time.Date(2026, 5, 2, 11, 0, 0, 0, time.UTC)

	t.Run("upstream-only when there's no local Investigation", func(t *testing.T) {
		t.Parallel()
		got := sessionSyncStateFor(sessionSyncStateInputs{HasLocal: false})
		assert.Equal(t, SyncStatusUpstreamOnly, got.Status, "no local copy yet — operator could adopt via replay")
		assert.Nil(t, got.PR, "upstream-only entries don't carry PR sidecar; the upstream listing renders its own metadata")
	})

	t.Run("local-only when local exists and never pushed", func(t *testing.T) {
		t.Parallel()
		got := sessionSyncStateFor(sessionSyncStateInputs{HasLocal: true})
		assert.Equal(t, SyncStatusLocalOnly, got.Status)
		assert.Nil(t, got.PR, "no PR yet — nothing to sidecar")
	})

	t.Run("closed PR — re-push needed, sidecar still set so UI can link the closed PR", func(t *testing.T) {
		t.Parallel()
		got := sessionSyncStateFor(sessionSyncStateInputs{
			HasLocal:   true,
			Pushed:     true,
			PushURL:    "https://github.com/example/sessions/pull/42",
			PRState:    PRStateClosed,
			PRClosedAt: &closed,
		})
		assert.Equal(t, SyncStatusClosed, got.Status, "closed PR is its own status — distinct from drift; UI prompts re-push")
		require.NotNil(t, got.PR)
		assert.Equal(t, "closed", got.PR.State)
		assert.Equal(t, "2026-05-02T11:00:00Z", got.PR.ClosedAt, "RFC3339-formatted timestamp")
		assert.Empty(t, got.PR.MergedAt)
	})

	t.Run("synced — merged PR carries timestamp", func(t *testing.T) {
		t.Parallel()
		got := sessionSyncStateFor(sessionSyncStateInputs{
			HasLocal:   true,
			Pushed:     true,
			PushURL:    "https://github.com/example/sessions/pull/7",
			PRState:    PRStateMerged,
			PRMergedAt: &merged,
		})
		assert.Equal(t, SyncStatusSynced, got.Status)
		require.NotNil(t, got.PR)
		assert.Equal(t, "merged", got.PR.State)
		assert.Equal(t, "2026-05-01T09:30:00Z", got.PR.MergedAt)
	})

	t.Run("synced — open PR has URL but no timestamp yet", func(t *testing.T) {
		t.Parallel()
		got := sessionSyncStateFor(sessionSyncStateInputs{
			HasLocal: true,
			Pushed:   true,
			PushURL:  "https://github.com/example/sessions/pull/9",
			PRState:  PRStateOpen,
		})
		assert.Equal(t, SyncStatusSynced, got.Status)
		require.NotNil(t, got.PR)
		assert.Equal(t, "open", got.PR.State)
		assert.Empty(t, got.PR.MergedAt)
		assert.Empty(t, got.PR.ClosedAt)
	})

	t.Run("synced — pushed via reconcile (no PushURL): no PR sidecar", func(t *testing.T) {
		// ReconcileUpstreamPushed sets PushedAt without a PushURL —
		// it knows the upstream session.md exists but has no PR
		// reference. We still mark the session synced (the canonical
		// answer to "is this on upstream?"); just no PR sidecar to
		// render.
		t.Parallel()
		got := sessionSyncStateFor(sessionSyncStateInputs{
			HasLocal: true,
			Pushed:   true,
		})
		assert.Equal(t, SyncStatusSynced, got.Status)
		assert.Nil(t, got.PR, "no PushURL → no PR badge to render")
	})
}

// The in-app upstream-sessions sync button (handleSessionsUpstreamSync)
// must trigger ReconcileUpstreamPushed after the git reset — otherwise
// a session that just appeared upstream (peer pushed and we synced it
// down) stays "unpushed" in the sidebar until launcher restart. This
// test sets up a bare/clone pair, registers a matching local
// Investigation, pushes the session.md to the bare repo, then hits
// the sync handler and verifies the local Investigation now reports
// SyncStatusSynced — would fail before the reconcile call was added
// to the handler.
func TestHandleSessionsUpstreamSync_TriggersReconcile(t *testing.T) {
	skipIfNoGit(t)
	bare, clone := initBareAndClone(t)
	// initBareAndClone leaves the bare repo without a default branch
	// pointer; the production handler does `git reset --hard origin/HEAD`
	// which needs that pointer to exist. Set it once here so the test
	// exercises the same code path the real launcher hits.
	if out, err := exec.Command("git", "-C", bare, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput(); err != nil {
		t.Fatalf("set bare HEAD: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", clone, "remote", "set-head", "origin", "main").CombinedOutput(); err != nil {
		t.Fatalf("set origin HEAD: %v\n%s", err, out)
	}

	// Register a local Investigation whose slug we'll plant on origin.
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	created := time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC)
	inv, err := mgr.Register(&Investigation{
		ID:        "abcdef0123456789abcdef0123456789",
		Namespace: "prod-eu-1",
		CreatedAt: created,
	})
	require.NoError(t, err)
	require.Nil(t, inv.PushedAt, "fresh registration must not be marked pushed")

	// Push the matching session.md to origin from the clone, simulating
	// "peer pushed it from another machine and now origin/HEAD has it".
	slug := computeSessionSlug(created, "prod-eu-1", inv.ID)
	// Lay out per-session files at `<clone>/sessions/<monthDir>/<slug>/`,
	// matching the default-profile layout where sessions_path=sessions
	// and SessionsPath is the work-dir.
	sessionsWorkDir := filepath.Join(clone, "sessions")
	sessionDir := filepath.Join(sessionsWorkDir, monthDirForSlug(slug), slug)
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "session.md"), []byte("---\n---\n"), 0o644))
	for _, args := range [][]string{
		{"-C", clone, "add", "."},
		{"-C", clone, "commit", "-m", "add session", "--quiet"},
		{"-C", clone, "push", "origin", "main"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	a := &apiHandlers{
		opts: Options{
			SessionsPath:      sessionsWorkDir,
			SessionsCloneRoot: clone,
			SessionsRepo:      "sourcehawk/triagent-sessions",
		},
		manager: mgr,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions-upstream/sync", nil)
	a.handleSessionsUpstreamSync(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "sync failed: %s", rec.Body)

	// The reconcile must have flipped PushedAt — the canonical signal
	// for the SyncState classifier — without waiting for a launcher
	// restart.
	dto := mgr.Get(inv.ID).Snapshot()
	assert.True(t, dto.Pushed, "in-app sync must reconcile so the matching session.md flips PushedAt immediately")
	assert.Equal(t, SyncStatusSynced, dto.SyncState.Status, "syncState must reflect the reconcile result")
}

// wikiSyncStateFor is the binary classifier for wiki resources. The
// existing helper unsyncedWikiFiles uses `git diff --name-only
// origin/HEAD HEAD` which collapses "edited locally" and "added
// locally" into one bucket — same UX answer (push to share).
// Distinguishing drift from local-only would require an extra
// `git ls-tree` per call site; not worth it given the list UI
// renders the same icon for both. Falls back to "synced" when the
// caller's diff set is empty (matches the helper's behaviour: a
// failed git command surfaces as "no drift" rather than panicking
// the operator with phantom badges).
func TestWikiSyncStateFor_BothBranches(t *testing.T) {
	t.Parallel()
	t.Run("path in diff → local-drift", func(t *testing.T) {
		t.Parallel()
		got := wikiSyncStateFor(true)
		assert.Equal(t, SyncStatusLocalDrift, got.Status)
		assert.Contains(t, got.Reason, "not yet on upstream")
	})
	t.Run("path not in diff → synced", func(t *testing.T) {
		t.Parallel()
		got := wikiSyncStateFor(false)
		assert.Equal(t, SyncStatusSynced, got.Status)
	})
}

// Genuinely malformed JSON should return 400 — the EOF tolerance
// must not turn into "any body parses".
func TestHandlePlaybookSyncState_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	a := newSyncStateAPI(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/playbooks/foo/sync-state", strings.NewReader(`{"yaml":`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "foo")
	a.handlePlaybookSyncState(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "malformed JSON must 400")
}
