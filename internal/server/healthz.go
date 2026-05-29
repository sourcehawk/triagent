package server

import (
	"encoding/json"
	"net/http"

	"github.com/sourcehawk/triagent/internal/profile"
)

// healthzHandler returns the readiness probe used by the e2e harness and
// the boot-options flow. It only registers once the HTTP server is
// listening and the session manager is initialised, so a 200 from it is a
// reliable "the launcher is up" signal. The body reports the resolved
// profile name (so the boot-options flow can assert profile precedence)
// and the launcher version.
//
// GET /healthz → 200 {"profile": "<resolved-name>", "version": "<version>"}
func healthzHandler(profileName, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"profile": profileName,
			"version": version,
		})
	}
}

// profileName returns the resolved profile name, or "" when no profile is
// wired (tests, or a launch without a profile).
func profileName(p *profile.Profile) string {
	if p == nil {
		return ""
	}
	return p.Name
}

// launcherVersion defaults an empty version to "dev" so a local build
// (which doesn't thread -ldflags) still reports a non-empty version, the
// same fallback cmd/triagent uses for the root command.
func launcherVersion(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}
