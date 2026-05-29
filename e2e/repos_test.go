//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// Flow 5 repos page. The repos surface reconciles four sources with
// independent failure modes: the profile's linked_repos (defaults), the
// user_repos.yaml (user-local), the cached gh metadata, and the
// repo-summary vault. These backend invariants pin what an operator sees
// when one of the four repos has a summary and three don't, and that the
// regenerate path is single-flight: a second refresh while one is in
// flight is a no-op (409), not a second worker.
//
// The single-flight observable is the second POST .../summary/refresh
// returning 409 while the first worker's claude sub-agent is gated on a
// wait_for_signal — proving no second generation was admitted, not merely
// that the UI shows "generating". Releasing the signal lets the first
// worker finish; the SSE phase=success event then confirms the summary
// landed.

// The four repos the mixed scenario reconciles. acme/payments (a default)
// ships with a pre-baked summary; the other three are empty-state.
// acme/gateway (user-local) is the regenerate target — the harness seeds a
// local clone for it so the summary worker's EnsureClone succeeds offline.
const (
	flow5RegenOwner = "acme"
	flow5RegenName  = "gateway"
)

// TestRepos_BackendInvariants drives the repos page's REST + SSE surface
// through the real launcher and pins the four-source reconciliation plus
// the single-flight regenerate contract.
func TestRepos_BackendInvariants(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:      "with-linked-repos",
		RepoFixtures: "mixed",
		StubScript:   "repos-regenerate",
		GhScript:     "repos-mixed",
	})

	// Four-source reconciliation: the profile's linked_repos surface as
	// "defaults", the user_repos.yaml as "user". Both groups render.
	defaults, user := listRepos(t, h)
	assertRepoListed(t, defaults, "acme", "payments")
	assertRepoListed(t, defaults, "acme", "billing")
	assertRepoListed(t, user, "acme", "gateway")
	assertRepoListed(t, user, "acme", "notifier")

	// Summary present for exactly one of the four; the other three are
	// empty-state. The seeded payments summary carries a distinctive
	// marker proving the cache was read, not synthesized.
	if exists, content := repoSummary(t, h, "acme", "payments"); !exists {
		t.Fatalf("acme/payments summary should exist (seeded fixture)")
	} else if !strings.Contains(content, "TRIAGENT-E2E-FLOW5-PAYMENTS-SUMMARY-MARKER") {
		t.Errorf("payments summary missing seed marker; got %q", content)
	}
	for _, empty := range [][2]string{{"acme", "billing"}, {"acme", "gateway"}, {"acme", "notifier"}} {
		if exists, _ := repoSummary(t, h, empty[0], empty[1]); exists {
			t.Errorf("%s/%s should be empty-state (no summary), but one exists", empty[0], empty[1])
		}
	}

	// Regenerate the empty gateway summary. Open an SSE subscription first
	// so the started/success phase events are observed deterministically.
	stream := h.Client.OpenStream(t)
	defer stream.Close()

	if status := refreshRepoSummary(t, h, flow5RegenOwner, flow5RegenName); status != http.StatusAccepted {
		t.Fatalf("first refresh: status = %d, want 202", status)
	}

	// The worker's claude sub-agent is now gated on the wait_for_signal in
	// the regenerate stub script. Wait for the phase=started event so the
	// in-flight window is open, then prove single-flight: a second refresh
	// returns 409 (already in flight) — no second worker admitted.
	waitForRepoSummaryPhase(t, stream, flow5RegenOwner, flow5RegenName, "started")
	if status := refreshRepoSummary(t, h, flow5RegenOwner, flow5RegenName); status != http.StatusConflict {
		t.Fatalf("second in-flight refresh: status = %d, want 409 (single-flight)", status)
	}

	// Status endpoint corroborates the in-flight state independent of the
	// 409 — the worker is genuinely running, not just rejecting duplicates.
	if !repoSummaryInFlight(t, h, flow5RegenOwner, flow5RegenName) {
		t.Errorf("status endpoint should report inFlight=true while the worker runs")
	}

	// Release the gate so the first (only) worker finishes. The success
	// phase event confirms the generation completed; the summary then
	// reads back with the regenerated marker.
	h.ReleaseSignal(t, "regenerate-released")
	waitForRepoSummaryPhase(t, stream, flow5RegenOwner, flow5RegenName, "success")

	exists, content := repoSummary(t, h, flow5RegenOwner, flow5RegenName)
	if !exists {
		t.Fatalf("gateway summary should exist after regenerate completed")
	}
	if !strings.Contains(content, "TRIAGENT-E2E-FLOW5-REGENERATED-MARKER") {
		t.Errorf("regenerated summary missing marker; got %q", content)
	}
}

// TestRepos_Browser drives the same scenario, then hands the regenerate
// target to the Playwright spec, which owns the DOM half: linked + user
// groups render, summary vs empty-state, and the regenerate click →
// summary appears. The single-flight backend invariant is pinned in
// TestRepos_BackendInvariants; the browser only asserts what it can see.
func TestRepos_Browser(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:      "with-linked-repos",
		RepoFixtures: "mixed",
		StubScript:   "repos-regenerate",
		GhScript:     "repos-mixed",
		Browser:      true,
	})

	h.Browser.SetEnv("TRIAGENT_REPO_OWNER", flow5RegenOwner)
	h.Browser.SetEnv("TRIAGENT_REPO_NAME", flow5RegenName)
	h.Browser.SetEnv("TRIAGENT_REPO_WITH_SUMMARY", "acme/payments")

	// Phase 1: reconciliation, activity panel, and single-flight. The
	// single-flight spec clicks refresh — the worker's sub-agent blocks on a
	// gate signal — asserts the disabled/"generating…" button and the 409 on
	// a concurrent refresh, then ends with the worker STILL gated. Because
	// Browser.Run is synchronous, when it returns those in-flight assertions
	// are provably complete; the worker has not been released, so there is no
	// release-vs-probe race (the flake the old timer-based releaser had).
	h.Browser.Run(t, "repos.spec.ts")

	// The worker is gated post-single-flight. Release deterministically now,
	// then wait for completion before the result spec reads the summary.
	if !repoSummaryInFlight(t, h, flow5RegenOwner, flow5RegenName) {
		t.Fatal("regenerate worker should still be gated in-flight after repos.spec.ts returned")
	}
	h.ReleaseSignal(t, "regenerate-released")

	deadline := time.Now().Add(60 * time.Second)
	for {
		if exists, content := repoSummary(t, h, flow5RegenOwner, flow5RegenName); exists &&
			strings.Contains(content, "TRIAGENT-E2E-FLOW5-REGENERATED-MARKER") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("regenerated summary did not land after releasing the gate")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Phase 2: the regenerated summary now renders in the DOM.
	h.Browser.Run(t, "repos-regen-result.spec.ts")
}

// listRepos fetches GET /api/repos and returns the defaults + user groups.
func listRepos(t *testing.T, h *harness.Harness) (defaults, user []listedRepo) {
	t.Helper()
	status, body := h.Client.Get(t, "/api/repos")
	if status != http.StatusOK {
		t.Fatalf("GET /api/repos status = %d (body %s)", status, body)
	}
	var parsed struct {
		Defaults []listedRepo `json:"defaults"`
		User     []listedRepo `json:"user"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode repos list: %v (body %s)", err, body)
	}
	return parsed.Defaults, parsed.User
}

type listedRepo struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func assertRepoListed(t *testing.T, group []listedRepo, owner, name string) {
	t.Helper()
	for _, r := range group {
		if r.Owner == owner && r.Name == name {
			return
		}
	}
	t.Errorf("repo %s/%s not present in group %v", owner, name, group)
}

// repoSummary fetches GET .../summary and returns whether it exists + the
// content body.
func repoSummary(t *testing.T, h *harness.Harness, owner, name string) (bool, string) {
	t.Helper()
	status, body := h.Client.Get(t, "/api/repos/"+owner+"/"+name+"/summary")
	if status != http.StatusOK {
		t.Fatalf("GET summary %s/%s status = %d (body %s)", owner, name, status, body)
	}
	var parsed struct {
		Exists  bool   `json:"exists"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode summary %s/%s: %v (body %s)", owner, name, err, body)
	}
	return parsed.Exists, parsed.Content
}

// repoSummaryInFlight reads GET .../summary/status and reports inFlight.
func repoSummaryInFlight(t *testing.T, h *harness.Harness, owner, name string) bool {
	t.Helper()
	status, body := h.Client.Get(t, "/api/repos/"+owner+"/"+name+"/summary/status")
	if status != http.StatusOK {
		t.Fatalf("GET status %s/%s = %d (body %s)", owner, name, status, body)
	}
	var parsed struct {
		InFlight bool `json:"inFlight"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode status %s/%s: %v (body %s)", owner, name, err, body)
	}
	return parsed.InFlight
}

// refreshRepoSummary POSTs the refresh trigger and returns the HTTP status.
func refreshRepoSummary(t *testing.T, h *harness.Harness, owner, name string) int {
	t.Helper()
	status, _ := h.Client.PostJSON(t, "/api/repos/"+owner+"/"+name+"/summary/refresh",
		map[string]string{"kind": "freeform"})
	return status
}

// waitForRepoSummaryPhase blocks until a repo_summary_state envelope for
// owner/name reaches the given phase on the multiplexed SSE stream.
func waitForRepoSummaryPhase(t *testing.T, s *harness.Stream, owner, name, phase string) {
	t.Helper()
	desc := "repo_summary_state " + owner + "/" + name + " phase=" + phase
	s.WaitForEvent(t, desc, func(ev harness.StreamEvent) bool {
		if ev.Kind != "repo_summary_state" {
			return false
		}
		var d struct {
			RepoSummary struct {
				Owner string `json:"owner"`
				Name  string `json:"name"`
				Phase string `json:"phase"`
			} `json:"repoSummary"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		return d.RepoSummary.Owner == owner && d.RepoSummary.Name == name && d.RepoSummary.Phase == phase
	}, 60*time.Second)
}
