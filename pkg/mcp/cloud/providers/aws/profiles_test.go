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

func writeSource(t *testing.T, dir, body string) string {
	t.Helper()
	src := filepath.Join(dir, "operator-config")
	require.NoError(t, os.WriteFile(src, []byte(body), 0o600))
	return src
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// TestWriteManagedProfilesGeneratesTargetFromSource proves the target is a
// self-contained copy of the operator config (so source_profile resolves) plus
// the managed blocks, and that the operator's own file is never touched.
func TestWriteManagedProfilesGeneratesTargetFromSource(t *testing.T) {
	dir := t.TempDir()
	srcBody := "[profile operator-test]\nregion = eu-west-1\n"
	src := writeSource(t, dir, srcBody)
	target := filepath.Join(dir, "aws", "config")
	accs := []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/triage-readonly"},
		{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/triage-readonly"},
	}
	require.NoError(t, writeManagedProfiles(target, src, "prod-aws", "operator-test", accs))

	got := readFile(t, target)
	assert.Contains(t, got, "[profile operator-test]", "operator profile must be copied so source_profile resolves")
	assert.Contains(t, got, managedSentinel)
	assert.Contains(t, got, "[profile triagent-cloud-prod-aws-111111111111]")
	assert.Contains(t, got, "[profile triagent-cloud-prod-aws-222222222222]")
	assert.Contains(t, got, "source_profile = operator-test")

	assert.Equal(t, srcBody, readFile(t, src), "operator's own config must never be modified")
}

// TestWriteManagedProfilesIdempotent proves a second write for the same alias
// replaces the prior block rather than appending a duplicate, and does not
// duplicate the operator copy.
func TestWriteManagedProfilesIdempotent(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "[profile sso-admin]\n")
	target := filepath.Join(dir, "config")
	accs := []Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}
	require.NoError(t, writeManagedProfiles(target, src, "prod-aws", "sso-admin", accs))
	require.NoError(t, writeManagedProfiles(target, src, "prod-aws", "sso-admin", accs))

	got := readFile(t, target)
	assert.Equal(t, 1, strings.Count(got, "[profile triagent-cloud-prod-aws-111111111111]"))
	assert.Equal(t, 1, strings.Count(got, "# BEGIN triagent-cloud-prod-aws"))
	assert.Equal(t, 1, strings.Count(got, managedSentinel))
	assert.Equal(t, 1, strings.Count(got, "[profile sso-admin]"), "operator copy must not be duplicated")
}

// TestWriteManagedProfilesCopiesOperatorConfig proves the operator's profiles
// (e.g. the SSO base the assume-role layers over) are present in the target.
func TestWriteManagedProfilesCopiesOperatorConfig(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "[profile sso-admin]\nsso_start_url = https://example.awsapps.com/start\n")
	target := filepath.Join(dir, "config")
	accs := []Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}
	require.NoError(t, writeManagedProfiles(target, src, "prod-aws", "sso-admin", accs))

	got := readFile(t, target)
	assert.Contains(t, got, "[profile sso-admin]")
	assert.Contains(t, got, "sso_start_url = https://example.awsapps.com/start")
}

// TestWriteManagedProfilesStripsStaleManagedBlocks proves an operator config
// that still carries an old in-place triagent block (from the pre-owned-file
// era) does not get that block duplicated into the target copy.
func TestWriteManagedProfilesStripsStaleManagedBlocks(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "[profile operator-test]\nregion = eu-west-1\n\n"+
		"# BEGIN triagent-cloud-old\n[profile triagent-cloud-old-1]\nrole_arn = arn:old\nsource_profile = operator-test\n# END triagent-cloud-old\n")
	target := filepath.Join(dir, "config")
	require.NoError(t, writeManagedProfiles(target, src, "aws-camunda", "operator-test",
		[]Account{{ID: "095352988152", RoleARN: "arn:aws:iam::095352988152:role/triagent-readonly"}}))

	got := readFile(t, target)
	assert.NotContains(t, got, "triagent-cloud-old", "stale managed block from the operator copy must be stripped")
	assert.Contains(t, got, "[profile triagent-cloud-aws-camunda-095352988152]")
	assert.Contains(t, got, "[profile operator-test]")
}

// TestWriteManagedProfilesTwoAliasesCarryForward proves two aliases' blocks
// coexist in the managed region: rewriting one preserves the other.
func TestWriteManagedProfilesTwoAliasesCarryForward(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "[profile base]\n")
	target := filepath.Join(dir, "config")
	require.NoError(t, writeManagedProfiles(target, src, "prod-aws", "base",
		[]Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}))
	require.NoError(t, writeManagedProfiles(target, src, "staging-aws", "base",
		[]Account{{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"}}))
	require.NoError(t, writeManagedProfiles(target, src, "prod-aws", "base",
		[]Account{{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"}}))

	got := readFile(t, target)
	assert.Equal(t, 1, strings.Count(got, "# BEGIN triagent-cloud-prod-aws"))
	assert.Equal(t, 1, strings.Count(got, "# BEGIN triagent-cloud-staging-aws"))
	assert.Equal(t, 1, strings.Count(got, "[profile base]"))
}

// TestWriteManagedProfilesConcurrentAliasesSurvive pins that concurrent
// generation for different aliases into the same target does not drop a block:
// the read-modify-write is serialized, so every alias's managed section survives.
func TestWriteManagedProfilesConcurrentAliasesSurvive(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "[profile sso-admin]\n")
	target := filepath.Join(dir, "config")
	aliases := []string{"alpha", "bravo", "charlie", "delta", "echo"}

	var wg sync.WaitGroup
	for _, alias := range aliases {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			assert.NoError(t, writeManagedProfiles(target, src, a, "sso-admin", []Account{
				{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/" + a},
			}))
		}(alias)
	}
	wg.Wait()

	got := readFile(t, target)
	for _, a := range aliases {
		begin, _ := blockMarkers(a)
		assert.Contains(t, got, begin, "alias %q block must survive concurrent generation", a)
	}
}

// TestWriteManagedProfilesRefusesUnparseableConfig pins the fail-closed
// guarantee: when the operator config carries a stray fragment, triagent refuses
// to write the target (rather than landing something the aws CLI cannot parse)
// and never creates the file.
func TestWriteManagedProfilesRefusesUnparseableConfig(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "[profile operator-test]\nregion = eu-west-1\n\n-test\n")
	target := filepath.Join(dir, "config")

	err := writeManagedProfiles(target, src, "aws-camunda", "operator-test",
		[]Account{{ID: "095352988152", RoleARN: "arn:aws:iam::095352988152:role/r"}})
	require.Error(t, err, "must refuse when the merged result is unparseable")
	assert.Contains(t, err.Error(), "-test", "error should name the offending line")

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "target must not be written when the write is refused")
}
