package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createProfile should copy the embedded `default` profile (profile.yaml
// + prompts/) into <dir>/<name>/ and rewrite the top-level name: line so
// the new profile identifies as <name>. This is the lightweight escape
// hatch from "I want to fork the default profile" — without it the
// operator has to dig into the source tree to find the embedded copy.
func TestCreateProfile_CopiesDefaultAndRenames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := createProfile(createProfileFlags{Name: "my-team", Dir: dir})
	require.NoError(t, err)

	yamlPath := filepath.Join(dir, "my-team", "profile.yaml")
	b, err := os.ReadFile(yamlPath)
	require.NoError(t, err, "profile.yaml must exist at <dir>/<name>/profile.yaml")

	yaml := string(b)
	assert.Contains(t, yaml, "name: my-team", "name: line must be rewritten to the new profile name")
	assert.NotContains(t, yaml, "name: default", "old name: must be replaced, not duplicated")

	// prompts/ should be carried over alongside profile.yaml.
	for _, p := range []string{"architecture.md", "system.md", "strategies.md", "editor.md", "wiki_editor.md"} {
		_, err := os.Stat(filepath.Join(dir, "my-team", "prompts", p))
		assert.NoError(t, err, "prompts/%s should be copied alongside profile.yaml", p)
	}
}

// createProfile should preserve comments in the copied yaml. The default
// profile is heavily commented (it's the canonical reference for every
// field) and the whole point of forking is to keep that documentation
// in front of the operator.
func TestCreateProfile_PreservesComments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, createProfile(createProfileFlags{Name: "preserved", Dir: dir}))
	b, err := os.ReadFile(filepath.Join(dir, "preserved", "profile.yaml"))
	require.NoError(t, err)
	yaml := string(b)
	// Pin a couple of comment lines from the default to prove the line
	// stream is intact (not round-tripped through yaml.Marshal which
	// strips comments).
	assert.Contains(t, yaml, "# triagent default profile")
	assert.Contains(t, yaml, "# Human-readable identifier.")
}

// Refusing to clobber an existing dir is the cheap-but-important safety
// net — operators run shell history and a careless re-invocation must
// not nuke whatever edits they've made.
func TestCreateProfile_RefusesExistingDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "existing"), 0o700))
	err := createProfile(createProfileFlags{Name: "existing", Dir: dir})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "exist")
}

// The name has to be a valid profile slug (matches what profile.Validate
// will accept downstream). Catching this at the CLI layer is friendlier
// than producing a forked profile that fails to load on the next
// `triagent start`.
func TestCreateProfile_RejectsInvalidName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "Foo Bar", "../escape", "with/slash"} {
		t.Run(name, func(t *testing.T) {
			err := createProfile(createProfileFlags{Name: name, Dir: t.TempDir()})
			require.Error(t, err, "name %q should be rejected", name)
		})
	}
}
