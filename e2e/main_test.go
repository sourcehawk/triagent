//go:build e2e

// Package e2e holds the golden-path end-to-end suite. It boots the real
// triagent launcher against scripted stubs via the harness package. The
// build tag keeps it out of `make test`; run it with `make test-e2e`.
package e2e

import (
	"net/http"
	"testing"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// TestSmoke_LauncherBootsAndHealthz boots the launcher with the minimal
// fixture profile, asserts the /healthz contract, and confirms the launch
// token plumbing reaches an authenticated /api/* endpoint.
func TestSmoke_LauncherBootsAndHealthz(t *testing.T) {
	h := harness.Launch(t, harness.Options{Profile: "minimal"})

	profile, version := h.Client.Healthz(t)
	if profile != "minimal" {
		t.Errorf("/healthz profile = %q, want %q", profile, "minimal")
	}
	if version == "" {
		t.Error("/healthz version is empty")
	}

	// The token Launch parsed from the launcher's startup log must
	// authenticate a cookie-gated endpoint; without it the launcher returns
	// 401. A clean 200 proves the full token round-trip wave-2 depends on.
	status, _ := h.Client.Get(t, "/api/investigations")
	if status != http.StatusOK {
		t.Errorf("GET /api/investigations status = %d, want 200 (token plumbing)", status)
	}
}
