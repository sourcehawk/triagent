package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Subdir-vault layouts put the vault work-dir under the clone
// (`<clone>/wikis`, `<clone>/playbooks`, `<clone>/sessions`) while `.git`
// stays at the clone root. Every upstream status + sync handler must run
// git against the clone root, otherwise the `.git` probe misses and the
// frontend renders a permanently disabled Sync button with "… is not a
// git checkout".

// initSubdirVault returns a bare/clone pair whose clone carries a vault
// work-dir at `<clone>/<subpath>`, with origin/HEAD resolvable so
// `reset --hard origin/HEAD` works the way it does against a real remote.
func initSubdirVault(t *testing.T, subpath string) (clone, vault string) {
	t.Helper()
	bare, clone := initBareAndClone(t)
	for _, args := range [][]string{
		{"-C", bare, "symbolic-ref", "HEAD", "refs/heads/main"},
		{"-C", clone, "remote", "set-head", "origin", "main"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	vault = filepath.Join(clone, subpath)
	require.NoError(t, os.MkdirAll(vault, 0o755))
	return clone, vault
}

func TestHandleWikiUpstreamStatus_SubdirVaultReportsCheckout(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "wikis")

	a := &apiHandlers{opts: Options{WikiPath: vault, WikiCloneRoot: clone, WikiRepo: "owner/wiki"}}
	rr := httptest.NewRecorder()
	a.handleWikiUpstreamStatus(rr, httptest.NewRequest(http.MethodGet, "/api/wiki-upstream", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var body wikiUpstreamStatus
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.True(t, body.GitCheckout, "`.git` lives at the clone root, not in the wiki work-dir")
	assert.NotEmpty(t, body.Commit, "HEAD must resolve via the clone root")
}

func TestHandleWikiUpstreamSync_SubdirVault(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "wikis")

	a := &apiHandlers{opts: Options{WikiPath: vault, WikiCloneRoot: clone, WikiRepo: "owner/wiki"}}
	rr := httptest.NewRecorder()
	a.handleWikiUpstreamSync(rr, httptest.NewRequest(http.MethodPost, "/api/wiki-upstream/sync", nil))
	require.Equal(t, http.StatusOK, rr.Code, "sync failed: %s", rr.Body)
}

func TestHandlePlaybooksUpstreamStatus_SubdirVaultReportsCheckout(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "playbooks")

	a := &apiHandlers{opts: Options{PluginPlaybooksDir: vault, PluginPlaybooksCloneRoot: clone, PlaybooksRepo: "owner/playbooks"}}
	rr := httptest.NewRecorder()
	a.handlePlaybooksUpstreamStatus(rr, httptest.NewRequest(http.MethodGet, "/api/playbooks-upstream", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var body upstreamStatus
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.True(t, body.GitCheckout, "`.git` lives at the clone root, not in the playbooks work-dir")
	assert.NotEmpty(t, body.Commit, "HEAD must resolve via the clone root")
}

func TestHandlePlaybooksUpstreamSync_SubdirVault(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "playbooks")

	a := &apiHandlers{
		opts:      Options{PluginPlaybooksDir: vault, PluginPlaybooksCloneRoot: clone, PlaybooksRepo: "owner/playbooks"},
		metaCache: &metaCache{},
	}
	rr := httptest.NewRecorder()
	a.handlePlaybooksUpstreamSync(rr, httptest.NewRequest(http.MethodPost, "/api/playbooks-upstream/sync", nil))
	require.Equal(t, http.StatusOK, rr.Code, "sync failed: %s", rr.Body)
}

func TestProbeWikiVault_SubdirVault(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "wikis")

	require.NoError(t, probeWikiVault(context.Background(), clone, vault),
		"session preflight must not flag a subdir wiki vault as un-cloned")
}

func TestProbeWikiVault_NoCheckout(t *testing.T) {
	dir := t.TempDir()

	require.Error(t, probeWikiVault(context.Background(), "", dir))
}

// readTypeDirs decides `tracked` by resolving <type>/type.txt at
// origin/HEAD. Rev paths are repo-root-relative unless prefixed with
// `./`, so on a subdir layout the unprefixed form looks up
// `<type>/type.txt` at the repo root and every type reads as untracked —
// which hands the operator the plain-delete UX for a type the next sync
// would restore.
func TestReadTypeDirs_SubdirVaultResolvesTracked(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "playbooks")
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "investigation"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vault, "investigation", "type.txt"), []byte("incident triage"), 0o644))
	for _, args := range [][]string{
		{"-C", clone, "add", "."},
		{"-C", clone, "commit", "-m", "add type", "--quiet"},
		{"-C", clone, "push", "origin", "main"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	types, err := readTypeDirs(context.Background(), clone, vault)
	require.NoError(t, err)
	require.Len(t, types, 1)
	assert.Equal(t, "investigation", types[0].Name)
	assert.True(t, types[0].Tracked, "type.txt is at origin/HEAD under the vault subpath")
}

func TestHandleSessionsUpstreamStatus_SubdirVaultReportsCheckout(t *testing.T) {
	skipIfNoGit(t)
	clone, vault := initSubdirVault(t, "sessions")

	a := &apiHandlers{opts: Options{SessionsPath: vault, SessionsCloneRoot: clone, SessionsRepo: "owner/sessions"}}
	rr := httptest.NewRecorder()
	a.handleSessionsUpstreamStatus(rr, httptest.NewRequest(http.MethodGet, "/api/sessions-upstream", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var body sessionsUpstreamStatus
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.True(t, body.GitCheckout, "`.git` lives at the clone root, not in the sessions work-dir")
	assert.NotEmpty(t, body.Commit, "HEAD must resolve via the clone root")
}
