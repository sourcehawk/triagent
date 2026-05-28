//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// Flow 3 — playbook editor, backend invariants. Drives the real
// launcher against the with-pending-proposal vault and pins the
// observable golden-path contracts of the polymorphic editor over a
// playbook subject: the vault's three playbooks are listed; the pending
// proposal is enumerated with its diff; an editor chat session is a real
// claude-stub round-trip whose reply lands in the editor transcript; a
// manual edit round-trips to disk; and approving the proposal promotes
// the draft onto the playbook file and clears the pending ledger.
//
// These are invariants, not whole-body snapshots — every assertion
// targets a product contract the editor.Session code path must hold, and
// every write goes through the real triagent-mcp subprocesses
// (validate-playbook / write-user-playbook / promote-proposal).
func TestPlaybookEditor_BackendInvariants(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:          "minimal",
		PlaybookFixtures: "with-pending-proposal",
		StubScript:       "playbook-editor",
	})

	// The vault's three user playbooks are all listed. The list also
	// carries the launcher-bundled system metas, so assert containment,
	// not an exact count.
	ids := playbookIDs(t, h)
	for _, want := range []string{"payments_latency", "broker_ooms", "capture_routing"} {
		if !ids[want] {
			t.Errorf("playbook %q not listed by /api/playbooks (got %v)", want, keys(ids))
		}
	}

	// The seeded pending proposal is enumerated against payments_latency,
	// flagged as an update (the id already exists on disk), not a new one.
	prop := findPlaybookProposal(t, h, "payments_latency")
	if prop.ProposalID != "abc123def456" {
		t.Errorf("pending proposal id = %q, want abc123def456", prop.ProposalID)
	}
	if prop.IsNew {
		t.Errorf("proposal for an existing playbook should not be flagged is_new")
	}

	// The proposal diff is retrievable: base = the saved playbook, new =
	// the draft that adds the downstream-dependency check.
	base, next := playbookProposalDiff(t, h, prop.ProposalID)
	if !strings.Contains(base, "isolate_hot_path") {
		t.Errorf("proposal base_yaml missing the current playbook body (got %q)", truncate(base))
	}
	if !strings.Contains(next, "check_downstream") {
		t.Errorf("proposal new_yaml missing the proposed downstream-check node (got %q)", truncate(next))
	}

	// Editor chat is a genuine claude-stub round-trip over the playbook
	// subject. Open the session, wait for the synthetic end, and confirm
	// the stub's reply landed in the editor transcript.
	stream := h.Client.OpenStream(t)
	defer stream.Close()
	sessID := createPlaybookEditorSession(t, h, "payments_latency")
	waitForEditorEnd(t, stream, sessID)
	if !editorTranscriptHasAssistant(t, h, sessID, "PLAYBOOK-EDITOR-STUB-REPLY") {
		t.Errorf("editor chat reply not found in transcript for session %s", sessID)
	}

	// Manual edit save: change the symptom and POST it back. The launcher
	// validates + writes via triagent-mcp; the on-disk YAML reflects the
	// edited field.
	const newSymptom = "Payments API p99 latency breaching SLO (edited via e2e manual save)"
	editedYAML := strings.Replace(
		readPlaybookYAML(t, h, "payments_latency"),
		"Payments API p99 latency spiking past SLO during checkout",
		newSymptom,
		1,
	)
	savePlaybook(t, h, "payments_latency", "investigation", editedYAML)
	onDisk := readPlaybookFromDisk(t, h, "minimal", "investigation", "payments_latency")
	if !strings.Contains(onDisk, newSymptom) {
		t.Errorf("manual edit did not reach disk: %q not in %q", newSymptom, truncate(onDisk))
	}

	// Accept the proposal: promote the draft onto the playbook file and
	// clear the pending ledger. After approval the promoted body carries
	// the downstream-check node and the proposal no longer lists as
	// pending.
	approvePlaybookProposal(t, h, prop.ProposalID)
	promoted := readPlaybookFromDisk(t, h, "minimal", "investigation", "payments_latency")
	if !strings.Contains(promoted, "check_downstream") {
		t.Errorf("approved proposal did not promote onto the playbook file (got %q)", truncate(promoted))
	}
	if findOptionalPlaybookProposal(t, h, "payments_latency") != nil {
		t.Errorf("proposal still listed as pending after approval")
	}
	if !resolutionMarkerExists(t, h, "minimal", prop.ProposalID) {
		t.Errorf("approval did not write a .resolved ledger marker for %s", prop.ProposalID)
	}
}

// TestPlaybookEditor_Browser drives the same vault, then hands control
// to the Playwright spec, which asserts the rendered DOM: the three
// fixture playbooks render as cards, the pending proposal shows its
// proposed-badge in the sidenav, and clicking a card mounts the editor
// at ?playbook=<id>. The backend invariants are pinned separately above.
func TestPlaybookEditor_Browser(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:          "minimal",
		PlaybookFixtures: "with-pending-proposal",
		StubScript:       "playbook-editor",
		Browser:          true,
	})

	// Confirm the vault is server-side complete before driving the
	// browser, so a browser-side timeout reads as a render bug.
	ids := playbookIDs(t, h)
	for _, want := range []string{"payments_latency", "broker_ooms", "capture_routing"} {
		if !ids[want] {
			t.Fatalf("playbook %q missing server-side before browser run (got %v)", want, keys(ids))
		}
	}
	if findOptionalPlaybookProposal(t, h, "payments_latency") == nil {
		t.Fatalf("pending proposal missing server-side before browser run")
	}

	h.Browser.Run(t, "playbook.spec.ts")
}

// ── playbook REST helpers ─────────────────────────────────────────

// playbookIDs returns the set of playbook ids from GET /api/playbooks.
func playbookIDs(t *testing.T, h *harness.Harness) map[string]bool {
	t.Helper()
	status, body := h.Client.Get(t, "/api/playbooks")
	if status != http.StatusOK {
		t.Fatalf("GET /api/playbooks status = %d (body %s)", status, body)
	}
	var parsed struct {
		Playbooks []struct {
			ID string `json:"id"`
		} `json:"playbooks"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode playbooks list: %v (body %s)", err, body)
	}
	out := map[string]bool{}
	for _, p := range parsed.Playbooks {
		out[p.ID] = true
	}
	return out
}

// readPlaybookYAML returns the active YAML body for a playbook id via
// GET /api/playbooks/{id}.
func readPlaybookYAML(t *testing.T, h *harness.Harness, id string) string {
	t.Helper()
	status, body := h.Client.Get(t, "/api/playbooks/"+id)
	if status != http.StatusOK {
		t.Fatalf("GET /api/playbooks/%s status = %d (body %s)", id, status, body)
	}
	var parsed struct {
		YAML string `json:"yaml"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode playbook %s: %v (body %s)", id, err, body)
	}
	return parsed.YAML
}

// savePlaybook POSTs an edited YAML body to /api/playbooks/{id} and
// fails on a non-204 (surfacing validation errors in the body).
func savePlaybook(t *testing.T, h *harness.Harness, id, typeName, yaml string) {
	t.Helper()
	status, body := h.Client.PostJSON(t, "/api/playbooks/"+id, map[string]any{
		"yaml": yaml,
		"type": typeName,
	})
	if status != http.StatusNoContent {
		t.Fatalf("POST /api/playbooks/%s status = %d (body %s)", id, status, body)
	}
}

// playbookProposalItem is the subset of the proposal-list row the
// backend assertions read.
type playbookProposalItem struct {
	ProposalID string `json:"proposal_id"`
	PlaybookID string `json:"playbook_id"`
	IsNew      bool   `json:"is_new"`
}

// listPlaybookProposals returns every pending playbook proposal.
func listPlaybookProposals(t *testing.T, h *harness.Harness) []playbookProposalItem {
	t.Helper()
	status, body := h.Client.Get(t, "/api/playbook-proposals")
	if status != http.StatusOK {
		t.Fatalf("GET /api/playbook-proposals status = %d (body %s)", status, body)
	}
	var parsed struct {
		Proposals []playbookProposalItem `json:"proposals"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode proposals: %v (body %s)", err, body)
	}
	return parsed.Proposals
}

// findOptionalPlaybookProposal returns the pending proposal for a
// playbook id, or nil when none is listed.
func findOptionalPlaybookProposal(t *testing.T, h *harness.Harness, playbookID string) *playbookProposalItem {
	t.Helper()
	for _, p := range listPlaybookProposals(t, h) {
		if p.PlaybookID == playbookID {
			cp := p
			return &cp
		}
	}
	return nil
}

// findPlaybookProposal is findOptionalPlaybookProposal but fails the
// test when the proposal is absent.
func findPlaybookProposal(t *testing.T, h *harness.Harness, playbookID string) playbookProposalItem {
	t.Helper()
	p := findOptionalPlaybookProposal(t, h, playbookID)
	if p == nil {
		t.Fatalf("no pending proposal for playbook %q", playbookID)
	}
	return *p
}

// playbookProposalDiff returns (base_yaml, new_yaml) for a proposal id.
func playbookProposalDiff(t *testing.T, h *harness.Harness, proposalID string) (base, next string) {
	t.Helper()
	status, body := h.Client.Get(t, "/api/playbook-proposals/"+proposalID)
	if status != http.StatusOK {
		t.Fatalf("GET /api/playbook-proposals/%s status = %d (body %s)", proposalID, status, body)
	}
	var parsed struct {
		BaseYAML string `json:"base_yaml"`
		NewYAML  string `json:"new_yaml"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode proposal %s: %v (body %s)", proposalID, err, body)
	}
	if parsed.Status != "pending" {
		t.Fatalf("proposal %s status = %q, want pending", proposalID, parsed.Status)
	}
	return parsed.BaseYAML, parsed.NewYAML
}

// approvePlaybookProposal promotes a proposal and fails on non-200.
func approvePlaybookProposal(t *testing.T, h *harness.Harness, proposalID string) {
	t.Helper()
	status, body := h.Client.PostJSON(t, "/api/playbook-proposals/"+proposalID+"/approve", nil)
	if status != http.StatusOK {
		t.Fatalf("approve proposal %s status = %d (body %s)", proposalID, status, body)
	}
}

// ── editor-session helpers (shared with wiki_test.go) ─────────────

// createPlaybookEditorSession opens an editor chat session bound to a
// playbook subject and returns its id, starting the claude-stub turn.
func createPlaybookEditorSession(t *testing.T, h *harness.Harness, playbookID string) string {
	t.Helper()
	return createEditorSession(t, h, map[string]any{
		"kind": "playbook",
		"playbook": map[string]any{
			"id":      playbookID,
			"version": "HEAD",
		},
	})
}

// createEditorSession is the subject-agnostic editor-session opener — the
// single REST entry point both editor kinds share, so the wiki test
// reuses it to assert editor.Session polymorphism.
func createEditorSession(t *testing.T, h *harness.Harness, subject map[string]any) string {
	t.Helper()
	status, body := h.Client.PostJSON(t, "/api/editor-sessions", map[string]any{
		"subject": subject,
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/editor-sessions status = %d (body %s)", status, body)
	}
	var dto struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("decode editor-session DTO: %v (body %s)", err, body)
	}
	if dto.ID == "" {
		t.Fatalf("editor-session response carried no id (body %s)", body)
	}
	return dto.ID
}

// editorTranscriptHasAssistant reports whether the editor session's
// transcript carries an assistant event whose text contains substr.
func editorTranscriptHasAssistant(t *testing.T, h *harness.Harness, sessID, substr string) bool {
	t.Helper()
	status, body := h.Client.Get(t, "/api/editor-sessions/"+sessID+"/transcript")
	if status != http.StatusOK {
		t.Fatalf("GET editor transcript status = %d (body %s)", status, body)
	}
	var parsed struct {
		Events []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode editor transcript: %v (body %s)", err, body)
	}
	for _, ev := range parsed.Events {
		if ev.Kind == "assistant" && strings.Contains(ev.Text, substr) {
			return true
		}
	}
	return false
}

// waitForEditorEnd blocks until the editor session's synthetic end
// envelope arrives on the multiplex stream.
func waitForEditorEnd(t *testing.T, s *harness.Stream, editorSessionID string) {
	t.Helper()
	s.WaitForEvent(t, "editor end for "+editorSessionID, func(ev harness.StreamEvent) bool {
		if ev.Kind != "end" {
			return false
		}
		var d struct {
			EditorSessionID string `json:"editorSessionId"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		return d.EditorSessionID == editorSessionID
	}, 30*time.Second)
}

// ── disk helpers ──────────────────────────────────────────────────

// playbookDir is the on-disk user-playbook directory the launcher
// persisted into: <StateDir>/triagent/<profile>/playbooks.
func playbookDir(h *harness.Harness, profile string) string {
	return filepath.Join(h.StateDir, "triagent", profile, "playbooks")
}

// readPlaybookFromDisk reads the saved user playbook YAML straight off
// disk (<playbooks>/<type>/<id>.yaml), bypassing the API so the
// assertion is a genuine round-trip check.
func readPlaybookFromDisk(t *testing.T, h *harness.Harness, profile, typeName, id string) string {
	t.Helper()
	path := filepath.Join(playbookDir(h, profile), typeName, id+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read playbook from disk %s: %v", path, err)
	}
	return string(data)
}

// resolutionMarkerExists reports whether the .resolved ledger marker
// for a proposal id is present on disk.
func resolutionMarkerExists(t *testing.T, h *harness.Harness, profile, proposalID string) bool {
	t.Helper()
	path := filepath.Join(playbookDir(h, profile), "proposals", ".resolved", proposalID+".json")
	_, err := os.Stat(path)
	return err == nil
}

// ── small utilities ───────────────────────────────────────────────

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func truncate(s string) string {
	const max = 300
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
