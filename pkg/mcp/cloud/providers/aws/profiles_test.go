package aws

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestWriteManagedProfilesRefusesUnparseableConfig pins the fail-closed
// guarantee: triagent never lands content in ~/.aws/config that the aws CLI
// cannot parse. A single stray fragment outside any managed block (the
// corruption seen in the field) breaks every profile in the file, including the
// operator's own base credentials, so the write is refused and the file is left
// byte-for-byte untouched.
func TestWriteManagedProfilesRefusesUnparseableConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	original := "[profile operator-test]\nregion = eu-west-1\n\n-test\n"
	require.NoError(t, os.WriteFile(cfg, []byte(original), 0o600))

	err := writeManagedProfiles(cfg, "aws-camunda", "operator-test",
		[]Account{{ID: "095352988152", RoleARN: "arn:aws:iam::095352988152:role/r"}})
	require.Error(t, err, "must refuse to write when the merged result is unparseable")
	assert.Contains(t, err.Error(), "-test", "error should name the offending line")

	b, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.Equal(t, original, string(b), "config must be untouched when the write is refused")
}

// TestWriteManagedProfilesConcurrentAliasesSurvive pins that concurrent
// generation for different aliases into the same config file does not drop a
// block: the read-modify-write is serialized, so every alias's managed section
// is present afterward. Without the lock, racing read-modify-writes clobber each
// other and a block goes missing.
func TestWriteManagedProfilesConcurrentAliasesSurvive(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	aliases := []string{"alpha", "bravo", "charlie", "delta", "echo"}

	var wg sync.WaitGroup
	for _, alias := range aliases {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			err := writeManagedProfiles(cfg, a, "sso-admin", []Account{
				{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/" + a},
			})
			assert.NoError(t, err)
		}(alias)
	}
	wg.Wait()

	data, err := os.ReadFile(cfg)
	require.NoError(t, err)
	got := string(data)
	for _, a := range aliases {
		begin, _ := blockMarkers(a)
		assert.Contains(t, got, begin, "alias %q block must survive concurrent generation", a)
	}
}
