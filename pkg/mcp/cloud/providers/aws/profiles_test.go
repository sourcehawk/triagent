package aws

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileName(t *testing.T) {
	assert.Equal(t, "triagent-cloud-prod-aws-111111111111", profileName("prod-aws", "111111111111"))
}

func TestWriteManagedProfilesBlock(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	accs := []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/triage-readonly"},
		{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/triage-readonly"},
	}
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))

	b, err := os.ReadFile(cfg)
	require.NoError(t, err)
	got := string(b)
	assert.Contains(t, got, "# BEGIN triagent-cloud-prod-aws")
	assert.Contains(t, got, "# END triagent-cloud-prod-aws")
	assert.Contains(t, got, "[profile triagent-cloud-prod-aws-111111111111]")
	assert.Contains(t, got, "[profile triagent-cloud-prod-aws-222222222222]")
	assert.Contains(t, got, "role_arn       = arn:aws:iam::111111111111:role/triage-readonly")
	assert.Contains(t, got, "source_profile = sso-admin")
}

// TestWriteManagedProfilesIdempotent proves a second write for the same alias
// replaces the prior block rather than appending a duplicate.
func TestWriteManagedProfilesIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	accs := []Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))

	b, err := os.ReadFile(cfg)
	require.NoError(t, err)
	got := string(b)
	assert.Equal(t, 1, strings.Count(got, "[profile triagent-cloud-prod-aws-111111111111]"))
	assert.Equal(t, 1, strings.Count(got, "# BEGIN triagent-cloud-prod-aws"))
}

// TestWriteManagedProfilesPreservesForeignContent proves the managed block is
// delimited: pre-existing operator profiles outside it survive a rewrite.
func TestWriteManagedProfilesPreservesForeignContent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	foreign := "[profile sso-admin]\nsso_start_url = https://example.awsapps.com/start\n\n"
	require.NoError(t, os.WriteFile(cfg, []byte(foreign), 0o600))

	accs := []Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-admin", accs))

	b, err := os.ReadFile(cfg)
	require.NoError(t, err)
	got := string(b)
	assert.Contains(t, got, "[profile sso-admin]")
	assert.Contains(t, got, "sso_start_url = https://example.awsapps.com/start")
	assert.Equal(t, 1, strings.Count(got, "[profile sso-admin]"), "foreign content must not be duplicated")
}

// TestWriteManagedProfilesTwoAliases proves two managed blocks for different
// aliases coexist: rewriting one leaves the other intact.
func TestWriteManagedProfilesTwoAliases(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-prod",
		[]Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}))
	require.NoError(t, writeManagedProfiles(cfg, "staging-aws", "sso-staging",
		[]Account{{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"}}))
	require.NoError(t, writeManagedProfiles(cfg, "prod-aws", "sso-prod",
		[]Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}))

	b, err := os.ReadFile(cfg)
	require.NoError(t, err)
	got := string(b)
	assert.Contains(t, got, "# BEGIN triagent-cloud-prod-aws")
	assert.Contains(t, got, "# BEGIN triagent-cloud-staging-aws")
	assert.Equal(t, 1, strings.Count(got, "# BEGIN triagent-cloud-prod-aws"))
	assert.Equal(t, 1, strings.Count(got, "# BEGIN triagent-cloud-staging-aws"))
}
