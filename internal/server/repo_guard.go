package server

import (
	"net/http"
)

// requireUpstreamRepo writes a 400 and returns false when the configured
// upstream repo is empty. Used by every sync / push-PR handler so an
// operator running on the embedded default profile (no defaults.*_repo
// set) gets an actionable error pointing at the field to fill in,
// rather than a confusing failure deep in a git or gh subprocess.
//
// `kind` matches the profile field stem and the YAML key suffix:
//
//	"playbooks" → defaults.playbooks_repo
//	"wiki"      → defaults.wiki_repo
//	"sessions"  → defaults.sessions_repo
//
// Returns true when repo is non-empty; the caller proceeds with its
// normal flow. Returns false when empty; the caller MUST short-circuit
// because writeError has already drained the ResponseWriter.
func (a *apiHandlers) requireUpstreamRepo(w http.ResponseWriter, kind, repo string) bool {
	if repo != "" {
		return true
	}
	writeError(w, http.StatusBadRequest,
		"no upstream "+kind+" repo configured — set defaults."+kind+"_repo in your profile (e.g. via `triagent create-profile <name>` and editing the file) and restart the launcher")
	return false
}
