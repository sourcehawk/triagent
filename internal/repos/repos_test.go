package repos

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sourcehawk/triagent/internal/profile"
)

func TestStoreRoundTripAndRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "user_repos.yaml")

	// Empty file → empty list, no error.
	got, err := LoadUserRepos(path)
	require.NoError(t, err, "LoadUserRepos on missing file")
	assert.Empty(t, got)

	// Add two entries. Description must clear MinDescriptionLength at add time.
	require.NoError(t, AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-service", Description: "Example service — core engine and leader-election."}))
	require.NoError(t, AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-ui", Description: "Example UI source — record import and process visualisation."}))

	// Duplicate add fails.
	require.Error(t, AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-service", Description: "Example service — core engine and leader-election."}), "duplicate add should have errored")

	got, err = LoadUserRepos(path)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, r := range got {
		assert.False(t, r.AddedAt.IsZero(), "AddedAt missing for %s", r.Key())
	}

	// Remove one.
	require.NoError(t, RemoveUserRepo(path, "example-org", "example-service"))
	got, _ = LoadUserRepos(path)
	require.Len(t, got, 1)
	assert.Equal(t, "example-ui", got[0].Name)

	// Removing a missing entry errors.
	require.Error(t, RemoveUserRepo(path, "example-org", "ghost"), "removing missing repo should have errored")
}

// TestAddUserRepo_DescriptionRequired verifies the add-time strict
// validator. Description is required (≥ MinDescriptionLength chars after
// trim) so the agent always has prompt-side orientation when no
// architecture summary has been generated for the repo yet. Loading
// existing user_repos.yaml entries with shorter descriptions stays
// lenient (load-time uses validateRepo, not validateRepoForAdd).
func TestAddUserRepo_DescriptionRequired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "user_repos.yaml")

	// Empty description rejected.
	err := AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-service"})
	require.Error(t, err, "empty description should be rejected")
	assert.Contains(t, err.Error(), "description is required")

	// Whitespace-only rejected.
	err = AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-service", Description: "   \t\n  "})
	require.Error(t, err, "whitespace-only description should be rejected")
	assert.Contains(t, err.Error(), "description is required")

	// Below MinDescriptionLength rejected.
	err = AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-service", Description: "too short"})
	require.Error(t, err, "short description should be rejected")
	assert.Contains(t, err.Error(), "at least")

	// Exactly MinDescriptionLength accepted.
	exact := "0123456789012345678901234567890" // 31 chars > 30
	require.NoError(t, AddUserRepo(path, LinkedRepo{Owner: "example-org", Name: "example-service", Description: exact}))
}

// TestLoadUserRepos_LegacyShortDescriptionStillLoads confirms an existing
// user_repos.yaml entry that predates the description rule still loads
// successfully — the strict rule only applies at AddUserRepo time. This
// keeps launcher startup robust against historical files.
func TestLoadUserRepos_LegacyShortDescriptionStillLoads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "user_repos.yaml")

	// Write a file with a short / empty description directly (bypass the
	// strict add-time validator).
	body := []byte("repos:\n  - owner: example-org\n    name: example-service\n  - owner: my-org\n    name: utils\n    description: x\n")
	require.NoError(t, os.WriteFile(path, body, 0o600))

	got, err := LoadUserRepos(path)
	require.NoError(t, err, "load should accept legacy entries with no/short description")
	require.Len(t, got, 2)
}

func TestResolveOrderingAndDedup(t *testing.T) {
	t.Parallel()
	defaults := []LinkedRepo{
		{Owner: "example-org", Name: "example-service"},
		{Owner: "example-org", Name: "example-ui"},
	}
	user := []LinkedRepo{
		{Owner: "example-org", Name: "example-extra", AddedAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)},
		{Owner: "example-org", Name: "example-service", AddedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}, // dupe of default
		{Owner: "example-org", Name: "example-service2", AddedAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)},
	}

	got := Resolve(defaults, user)

	want := []string{
		"example-org/example-service",  // default
		"example-org/example-ui",       // default (also in user but defaults win)
		"example-org/example-extra",    // user, oldest
		"example-org/example-service2", // user
	}
	require.Len(t, got, len(want))
	for i, key := range want {
		assert.Equal(t, key, got[i].Key(), "[%d]", i)
	}
}

func TestEffectiveAlias(t *testing.T) {
	t.Parallel()
	r := LinkedRepo{Owner: "x", Name: "y"}
	assert.Equal(t, "y", r.EffectiveAlias(), "expected name as alias")
	r.Alias = "custom"
	assert.Equal(t, "custom", r.EffectiveAlias(), "expected explicit alias")
}

// TestProfileDefaults_FromProfile asserts that ProfileDefaults returns the
// profile's linked_repos in the same order, with no surprises.
func TestProfileDefaults_FromProfile(t *testing.T) {
	t.Parallel()
	p := &profile.Profile{
		LinkedRepos: []profile.LinkedRepo{
			{Owner: "example-org", Name: "example-repo", Alias: "example-repo", Description: "test repo"},
			{Owner: "example-org", Name: "other-repo"},
		},
	}
	got := ProfileDefaults(p)
	require.Len(t, got, 2)
	assert.Equal(t, "example-org/example-repo", got[0].Key())
	assert.Equal(t, "example-org/other-repo", got[1].Key())
}

func TestProfileDefaults_NilProfile(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ProfileDefaults(nil))
}

func TestLinkedRepo_CodefixTimeoutMinutesYAMLRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "user_repos.yaml")
	require.NoError(t, AddUserRepo(path, LinkedRepo{
		Owner: "example-org", Name: "example-service",
		Description:           "Zeebe broker source — process engine and partition leader-election.",
		CodefixTimeoutMinutes: 90,
	}))
	repos, err := LoadUserRepos(path)
	require.NoError(t, err)
	require.Equal(t, 90, repos[0].CodefixTimeoutMinutes)
}
