package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSessionDir lays down a metadata.json + events.jsonl inside a fresh
// temp dir so the export/import helpers have something to chew on. The
// metadata mirrors what writeMetadata produces for a real investigation,
// including the local-only fields we expect the share path to scrub.
func fakeSessionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	created := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	meta := persistedMetadata{
		ID:              "sess-abc123",
		Namespace:       "staging-zeebe",
		IncidentURL:     "https://app.incident.io/acme/incidents/42",
		SlackChannelURL: "https://example.slack.com/archives/C123/p456",
		Notes:           "broker pods crashlooping after upgrade",
		MCPConfigPath:   "/tmp/local-only/mcp.json",
		DocsPrefix:      "mcp__example-docs__",
		SessionDir:      dir,
		CreatedAt:       created.Format("2006-01-02T15:04:05Z07:00"),
	}
	require.NoError(t, writePersistedMetadata(dir, meta))
	events := []EventEnvelope{
		{Seq: 1, Kind: envKindUser, Text: "broker is down", Timestamp: created.Add(time.Second)},
		{Seq: 2, Kind: envKindAssistant, Text: "investigating now", Timestamp: created.Add(2 * time.Second)},
		{Seq: 3, Kind: envKindToolUse, ToolID: "t1", ToolName: "mcp__triagent-k8s__get_pod", ToolInput: map[string]any{"name": "zeebe-0"}, Timestamp: created.Add(3 * time.Second)},
	}
	require.NoError(t, writeEventsFile(dir, events))
	return dir
}

func TestBuildShareBundle_DropsLocalFieldsAndCarriesEvents(t *testing.T) {
	t.Parallel()
	dir := fakeSessionDir(t)

	bundle, err := buildShareBundle(dir)
	require.NoError(t, err)

	assert.Equal(t, shareBundleSchemaVersion, bundle.SchemaVersion)
	assert.False(t, bundle.ExportedAt.IsZero(), "exportedAt should be set")
	assert.Equal(t, "https://app.incident.io/acme/incidents/42", bundle.Source.IncidentURL)
	require.Len(t, bundle.Events, 3)
	assert.Equal(t, "broker is down", bundle.Events[0].Text)

	// Round-trip through JSON to confirm no local-only field leaks.
	body, err := json.Marshal(bundle)
	require.NoError(t, err)
	for _, leak := range []string{"mcpConfigPath", "sessionDir", "promUrl", "linkedRepos", "/tmp/local-only"} {
		assert.False(t, bytes.Contains(body, []byte(leak)), "share bundle leaked %q on the wire: %s", leak, body)
	}
}

func TestImportRoundtrip_AdoptsArchivedSessionWithProvenance(t *testing.T) {
	t.Parallel()
	src := fakeSessionDir(t)

	bundle, err := buildShareBundle(src)
	require.NoError(t, err)
	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	// Decode + materialise into a different sessions root to mimic a
	// teammate's machine.
	receiverRoot := t.TempDir()
	decoded, err := decodeShareBundle(bytes.NewReader(body))
	require.NoError(t, err)
	dir, importedID, err := materializeImport(receiverRoot, decoded)
	require.NoError(t, err)
	// The imported session keeps the source's investigation id so its
	// slug matches the source's slug — see TestImportRoundtrip_PreservesSlugFromSource
	// for why that match is load-bearing.
	assert.Equal(t, "sess-abc123", importedID, "import should preserve source id so the local slug matches the upstream slug")

	mgr := NewManager(context.Background(), receiverRoot)
	inv, err := mgr.AdoptFromDir(dir)
	require.NoError(t, err)
	dto := inv.Snapshot()

	assert.True(t, dto.Archived, "imported inv should be archived")
	require.NotNil(t, dto.ImportedFrom, "imported inv missing ImportedFrom")
	assert.Equal(t, "sess-abc123", dto.ImportedFrom.InvestigationID)
	assert.False(t, dto.ImportedAt.IsZero(), "ImportedAt should be set")
	assert.Len(t, inv.events, 3, "replayed events")
	// Local-only fields must NOT have been carried over from source.
	assert.Empty(t, dto.MCPConfigPath, "imported inv should not carry source MCP config path")
	assert.Equal(t, dir, dto.SessionDir, "imported inv sessionDir should point at receiver's dir")
}

// When a bundle's source slug already exists in the upstream sessions
// clone, the post-import reconcile must mark the local copy as pushed.
// That's what flips the sidebar to the "synced" checkmark right after
// import, instead of only after the next launcher restart (when the
// startup ReconcileUpstreamPushed sweep would have caught it).
func TestHandleImportInvestigation_MarksPushedWhenUpstreamMatch(t *testing.T) {
	t.Parallel()
	// Custom source dir with a hex id so the resulting slug satisfies
	// sessionSlugPattern (fakeSessionDir's "sess-abc123" doesn't —
	// ReconcileUpstreamPushed silently skips slugs that don't match).
	srcDir := t.TempDir()
	created := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	require.NoError(t, writePersistedMetadata(srcDir, persistedMetadata{
		ID:         "abcdef0123456789",
		Namespace:  "staging-zeebe",
		SessionDir: srcDir,
		CreatedAt:  created.Format("2006-01-02T15:04:05Z07:00"),
	}))
	require.NoError(t, writeEventsFile(srcDir, []EventEnvelope{}))
	bundle, err := buildShareBundle(srcDir)
	require.NoError(t, err)
	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	// Lay down a fake upstream sessions clone containing exactly the
	// session.md we expect post-import slug to land on.
	wantSlug := computeSessionSlug(created, "staging-zeebe", "abcdef0123456789")
	sessionsClone := t.TempDir()
	// Mirror production: SessionsPath is the work-dir (cloneRoot +
	// sessions_path). The default profile sets sessions_path=sessions,
	// so the work-dir is `<clone>/sessions/`. Per-session files live at
	// `<work-dir>/<monthDir>/<slug>/session.md`.
	sessionsWorkDir := filepath.Join(sessionsClone, "sessions")
	upstreamDir := filepath.Join(sessionsWorkDir, monthDirForSlug(wantSlug), wantSlug)
	require.NoError(t, os.MkdirAll(upstreamDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "session.md"), []byte("---\n---\n"), 0o644))

	receiverRoot := t.TempDir()
	a := &apiHandlers{
		opts: Options{
			SessionsRoot: receiverRoot,
			SessionsPath: sessionsWorkDir,
		},
		manager: NewManager(context.Background(), receiverRoot),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	a.handleImportInvestigation(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "import failed: %s", rec.Body)

	var dto InvestigationDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, wantSlug, dto.Slug, "imported slug must match upstream slug")
	assert.True(t, dto.Pushed, "imported session matching an upstream session.md must be marked pushed so the sidebar checkmark shows immediately")
}

// Re-importing the same bundle through the HTTP handler should be
// idempotent: the second call returns the already-adopted investigation
// instead of creating a duplicate or leaving an orphan dir on disk. With
// slug-preserving import (TestImportRoundtrip_PreservesSlugFromSource),
// the SessionDoc replay flow short-circuits before re-importing, so this
// path mostly catches drag-drop / file-picker re-imports — but the
// idempotence is what makes those safe.
func TestHandleImportInvestigation_ReimportIsIdempotent(t *testing.T) {
	t.Parallel()
	src := fakeSessionDir(t)
	bundle, err := buildShareBundle(src)
	require.NoError(t, err)
	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	receiverRoot := t.TempDir()
	a := &apiHandlers{
		opts:    Options{SessionsRoot: receiverRoot},
		manager: NewManager(context.Background(), receiverRoot),
	}

	doImport := func() InvestigationDTO {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/investigations/import", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		a.handleImportInvestigation(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "import failed: %s", rec.Body)
		var dto InvestigationDTO
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		return dto
	}

	first := doImport()
	second := doImport()
	assert.Equal(t, first.ID, second.ID, "re-import must return the same investigation, not a duplicate")
	assert.Equal(t, first.SessionDir, second.SessionDir, "re-import must not orphan a fresh session dir")

	// The manager should hold exactly one investigation, not one per
	// import call.
	all := a.manager.List()
	assert.Len(t, all, 1, "re-import must not register a second investigation")

	// Belt-and-braces: only one session dir exists on disk under the
	// receiver root. A leaked materialised dir would show up here.
	entries, err := os.ReadDir(receiverRoot)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "re-import leaked an extra on-disk session dir")
}

// Importing a bundle should yield a local session whose Slug matches the
// source's Slug exactly. Without this, the upstream-pull flow in
// SessionDoc.openTranscript can't recognise an already-imported session
// (it matches by slug) and re-imports on every click — which is how the
// duplicate-on-replay bug surfaces in production. The slug is derived
// from CreatedAt + Namespace + ID, so preserving the source's CreatedAt
// and InvestigationID is the load-bearing invariant.
func TestImportRoundtrip_PreservesSlugFromSource(t *testing.T) {
	t.Parallel()
	src := fakeSessionDir(t)

	// Source slug is what the launcher would have computed when pushing
	// this session to upstream — i.e. the upstream directory name. The
	// imported copy must reproduce this exactly.
	sourceCreated := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	wantSlug := computeSessionSlug(sourceCreated, "staging-zeebe", "sess-abc123")

	bundle, err := buildShareBundle(src)
	require.NoError(t, err)
	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	receiverRoot := t.TempDir()
	decoded, err := decodeShareBundle(bytes.NewReader(body))
	require.NoError(t, err)
	dir, _, err := materializeImport(receiverRoot, decoded)
	require.NoError(t, err)

	mgr := NewManager(context.Background(), receiverRoot)
	inv, err := mgr.AdoptFromDir(dir)
	require.NoError(t, err)
	dto := inv.Snapshot()

	assert.Equal(t, wantSlug, dto.Slug, "imported slug must match source slug so upstream-pull dedup works")
}

func TestImportRoundtrip_ReExportPreservesOriginalSource(t *testing.T) {
	// Re-sharing an already-imported investigation should preserve the
	// original ImportedFrom rather than walking the chain by one hop on
	// each share.
	t.Parallel()
	src := fakeSessionDir(t)
	first, err := buildShareBundle(src)
	require.NoError(t, err)

	receiverRoot := t.TempDir()
	dir, _, err := materializeImport(receiverRoot, first)
	require.NoError(t, err)

	second, err := buildShareBundle(dir)
	require.NoError(t, err, "buildShareBundle (re-export)")
	assert.Equal(t, "sess-abc123", second.Source.InvestigationID, "re-export source id should walk through to original")
}

func TestDecodeShareBundle_RejectsUnknownSchemaVersion(t *testing.T) {
	t.Parallel()
	body := []byte(`{"schemaVersion":99,"source":{"contextName":"a","namespace":"b"},"events":[]}`)
	_, err := decodeShareBundle(bytes.NewReader(body))
	require.Error(t, err, "expected error for unknown schema version")
}

// Sessions started from a Slack alert or an incident URL don't carry a
// namespace — preflight requires "at least one of (cluster, incident URL,
// slack channel, notes)" and Slack-triggered investigations satisfy that
// without ever binding a namespace. Bundles for those sessions ship with
// `source.namespace: ""` and must still import cleanly.
func TestDecodeShareBundle_AcceptsEmptyNamespace(t *testing.T) {
	t.Parallel()
	body := []byte(`{"schemaVersion":1,"source":{"namespace":"","label":"alert from slack","notes":"broker crashloop"},"events":[]}`)
	bundle, err := decodeShareBundle(bytes.NewReader(body))
	require.NoError(t, err, "namespaceless bundle should decode — Slack-alert sessions have no namespace")
	assert.Equal(t, "alert from slack", bundle.Source.Label)
}

func TestExportFilename_Sanitises(t *testing.T) {
	t.Parallel()
	got := exportFilename(InvestigationDTO{
		ID:        "abcdef0123456789",
		Namespace: "z e e b e",
	})
	want := "z-e-e-b-e-abcdef01.triagent.json"
	assert.Equal(t, want, got)
}

func TestShareBundle_SlackPickerFieldsRoundTrip(t *testing.T) {
	// All three Slack fields should survive the full build→materialise→adopt cycle.
	t.Parallel()
	dir := t.TempDir()
	created := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	meta := persistedMetadata{
		ID:        "sess-slack-full",
		Namespace: "staging-zeebe",
		SlackChannelURL: "https://example.slack.com/archives/C123/p456",
		SlackChannelID:  "C123ABC",
		SlackChannelName: "incidents-platform",
		SessionDir:      dir,
		CreatedAt:       created.Format("2006-01-02T15:04:05Z07:00"),
	}
	require.NoError(t, writePersistedMetadata(dir, meta))
	require.NoError(t, writeEventsFile(dir, []EventEnvelope{}))

	bundle, err := buildShareBundle(dir)
	require.NoError(t, err)
	assert.Equal(t, "https://example.slack.com/archives/C123/p456", bundle.Source.SlackChannelURL)
	assert.Equal(t, "C123ABC", bundle.Source.SlackChannelID)
	assert.Equal(t, "incidents-platform", bundle.Source.SlackChannelName)

	// Full round-trip: encode → decode → materialise → adopt.
	body, err := json.Marshal(bundle)
	require.NoError(t, err)
	decoded, err := decodeShareBundle(bytes.NewReader(body))
	require.NoError(t, err)

	receiverRoot := t.TempDir()
	importedDir, newID, err := materializeImport(receiverRoot, decoded)
	require.NoError(t, err)
	require.NotEmpty(t, newID)

	mgr := NewManager(context.Background(), receiverRoot)
	inv, err := mgr.AdoptFromDir(importedDir)
	require.NoError(t, err)
	dto := inv.Snapshot()

	require.NotNil(t, dto.ImportedFrom, "ImportedFrom must be set")
	assert.Equal(t, "https://example.slack.com/archives/C123/p456", dto.ImportedFrom.SlackChannelURL)
	assert.Equal(t, "C123ABC", dto.ImportedFrom.SlackChannelID)
	assert.Equal(t, "incidents-platform", dto.ImportedFrom.SlackChannelName)
}

func TestShareBundle_OlderInvestigationsURLOnlyStillWork(t *testing.T) {
	// Investigations that only have SlackChannelURL (no ID/Name) must still
	// round-trip cleanly — the new fields are additive and backward-compatible.
	t.Parallel()
	dir := t.TempDir()
	created := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	meta := persistedMetadata{
		ID:        "sess-url-only",
		Namespace: "staging-zeebe",
		SlackChannelURL: "https://example.slack.com/archives/C999/p000",
		// SlackChannelID and SlackChannelName deliberately absent.
		SessionDir: dir,
		CreatedAt:  created.Format("2006-01-02T15:04:05Z07:00"),
	}
	require.NoError(t, writePersistedMetadata(dir, meta))
	require.NoError(t, writeEventsFile(dir, []EventEnvelope{}))

	bundle, err := buildShareBundle(dir)
	require.NoError(t, err)
	assert.Equal(t, "https://example.slack.com/archives/C999/p000", bundle.Source.SlackChannelURL)
	assert.Empty(t, bundle.Source.SlackChannelID, "SlackChannelID should be empty for URL-only investigation")
	assert.Empty(t, bundle.Source.SlackChannelName, "SlackChannelName should be empty for URL-only investigation")

	body, err := json.Marshal(bundle)
	require.NoError(t, err)
	decoded, err := decodeShareBundle(bytes.NewReader(body))
	require.NoError(t, err)

	receiverRoot := t.TempDir()
	importedDir, newID, err := materializeImport(receiverRoot, decoded)
	require.NoError(t, err)
	require.NotEmpty(t, newID)

	mgr := NewManager(context.Background(), receiverRoot)
	inv, err := mgr.AdoptFromDir(importedDir)
	require.NoError(t, err)
	dto := inv.Snapshot()

	require.NotNil(t, dto.ImportedFrom)
	assert.Equal(t, "https://example.slack.com/archives/C999/p000", dto.ImportedFrom.SlackChannelURL)
	assert.Empty(t, dto.ImportedFrom.SlackChannelID, "SlackChannelID should be empty (backward-compat)")
	assert.Empty(t, dto.ImportedFrom.SlackChannelName, "SlackChannelName should be empty (backward-compat)")
}

func TestShareBundle_LabelRoundtrip(t *testing.T) {
	// Label must survive the full build→decode→materialise→adopt cycle so
	// teammates who import a shared investigation see the same label.
	t.Parallel()
	dir := t.TempDir()
	created := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	meta := persistedMetadata{
		ID:        "sess-label-rt",
		Namespace: "staging-zeebe",
		Label:     "shared label",
		SessionDir:  dir,
		CreatedAt:   created.Format("2006-01-02T15:04:05Z07:00"),
	}
	require.NoError(t, writePersistedMetadata(dir, meta))
	require.NoError(t, writeEventsFile(dir, []EventEnvelope{}))

	bundle, err := buildShareBundle(dir)
	require.NoError(t, err)
	assert.Equal(t, "shared label", bundle.Source.Label, "Label should be present in share bundle source")

	body, err := json.Marshal(bundle)
	require.NoError(t, err)
	decoded, err := decodeShareBundle(bytes.NewReader(body))
	require.NoError(t, err)

	receiverRoot := t.TempDir()
	importedDir, _, err := materializeImport(receiverRoot, decoded)
	require.NoError(t, err)

	mgr := NewManager(context.Background(), receiverRoot)
	inv, err := mgr.AdoptFromDir(importedDir)
	require.NoError(t, err)
	dto := inv.Snapshot()

	assert.Equal(t, "shared label", dto.Label, "Label must round-trip through share bundle")
	require.NotNil(t, dto.ImportedFrom, "ImportedFrom must be set")
	assert.Equal(t, "shared label", dto.ImportedFrom.Label, "ImportedFrom.Label must carry the original label")
}

// Sanity check that what writeMetadata emits is what loadInvestigation can
// read back, including the new ImportedFrom/ImportedAt fields. Catches any
// drift between the two sides of persistence.
func TestPersistRoundtrip_ImportedFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	imported := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	src := ImportedFrom{
		InvestigationID: "src-1",
		Namespace:       "donor-ns",
		IncidentURL:     "https://app.incident.io/acme/incidents/7",
		CreatedAt:       time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	meta := persistedMetadata{
		ID:        "rcv-1",
		Namespace: "donor-ns",
		SessionDir:   dir,
		CreatedAt:    imported.Format("2006-01-02T15:04:05Z07:00"),
		ImportedFrom: &src,
		ImportedAt:   imported,
	}
	require.NoError(t, writePersistedMetadata(dir, meta))

	inv, err := loadInvestigation(dir)
	require.NoError(t, err)
	require.NotNil(t, inv, "loadInvestigation returned nil")
	require.NotNil(t, inv.ImportedFrom, "ImportedFrom did not round-trip")
	assert.Equal(t, "src-1", inv.ImportedFrom.InvestigationID)
	assert.True(t, inv.ImportedAt.Equal(imported), "ImportedAt did not round-trip: got %v want %v", inv.ImportedAt, imported)
	// Confirm the file actually has the field on disk (catches a struct
	// definition that silently omits it).
	body, err := os.ReadFile(filepath.Join(dir, fileMetadata))
	require.NoError(t, err, "read metadata")
	assert.Contains(t, string(body), `"importedFrom"`, "metadata.json missing importedFrom field on disk")
}
