package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateUserCacheDir redirects os.UserCacheDir() to a temp dir for the
// duration of the test. Without this, runClean tests would nuke the
// developer's real ~/.cache/triagent-mcp/slack — including any channels they've
// accumulated from real launcher use. Linux: $XDG_CACHE_HOME wins over
// $HOME/.cache. macOS uses $HOME/Library/Caches and ignores XDG, so we
// override $HOME there too. Both setenvs are scoped to the test via
// t.Setenv (auto-restored on test end).
func isolateUserCacheDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp) // macOS UserCacheDir uses $HOME/Library/Caches
	dir, err := slackCacheDir()
	require.NoError(t, err)
	return dir
}

func TestCleanTargets_IncludesSlackCacheWhenPresent(t *testing.T) {
	dir := isolateUserCacheDir(t)
	// Pre-create the slack cache root so the cleanTargets resolver
	// reports it. cleanTargets returns paths whose resolvers succeed;
	// runClean later filters absent paths.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "C1"), 0o700))

	targets, err := cleanTargets(false)
	require.NoError(t, err)
	var slackPath string
	for _, target := range targets {
		if target.Label == "slack channel caches" {
			slackPath = target.Path
			break
		}
	}
	require.NotEmpty(t, slackPath, "cleanTargets must include the slack cache directory")
	assert.Equal(t, dir, slackPath)
}

func TestRunClean_RemovesSlackCacheWhenPresent(t *testing.T) {
	dir := isolateUserCacheDir(t)
	// Materialise a stub slack cache under the (now-isolated) resolver
	// location. runClean's existing filter keeps only paths that exist, so
	// we have to put a real directory there.
	channelDir := filepath.Join(dir, "C-runclean-test")
	require.NoError(t, os.MkdirAll(channelDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "marker.txt"), []byte("present"), 0o600))

	require.NoError(t, runClean(cleanFlags{Yes: true}))

	_, statErr := os.Stat(channelDir)
	assert.True(t, os.IsNotExist(statErr),
		"runClean(--yes) must remove the slack cache root and everything under it; got: %v", statErr)
}

// TestRunClean_OnlyPlaybooksIncludesUserDir verifies that --only-playbooks
// is a full playbook reset — it nukes the user playbooks dir alongside
// the upstream clone + bundled extract, even without --include-user.
// The broad "triagent clean" still protects the user dir by default
// (covered implicitly: cleanTargets(false) doesn't list it); this is the
// explicit-scope opt-in.
func TestRunClean_OnlyPlaybooksIncludesUserDir(t *testing.T) {
	// Isolate $HOME so userPlaybooksDir() resolves into a temp tree.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	// The default profile templates user_playbooks_dir as
	// ${XDG_CONFIG_HOME}/triagent/${PROFILE_NAME}/playbooks; with
	// XDG_CONFIG_HOME pointed at our temp dir above and the embedded
	// "default" profile in use, Paths.Resolve produces this path.
	userDir := filepath.Join(tmp, ".config", "triagent", "default", "playbooks")
	require.NoError(t, os.MkdirAll(userDir, 0o700))
	authored := filepath.Join(userDir, "investigation", "my_custom.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(authored), 0o700))
	require.NoError(t, os.WriteFile(authored, []byte("id: my_custom\nsymptom: test\nschema_version: 1\nentrypoint: x\nnodes: {x: {description: d, terminal_advice: a}}\n"), 0o600))

	require.NoError(t, runClean(cleanFlags{Yes: true, OnlyPlaybooks: true}))

	_, statErr := os.Stat(authored)
	assert.True(t, os.IsNotExist(statErr),
		"runClean(--only-playbooks) must remove the user playbooks dir even without --include-user; got: %v", statErr)
}
