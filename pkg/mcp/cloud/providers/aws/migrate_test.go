package aws

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripManagedBlocksFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	body := "[profile operator-test]\nregion = eu-west-1\n\n" +
		"# BEGIN triagent-cloud-camunda\n[profile triagent-cloud-camunda-1]\nrole_arn = arn\nsource_profile = operator-test\n# END triagent-cloud-camunda\n"
	require.NoError(t, os.WriteFile(cfg, []byte(body), 0o600))

	changed, err := StripManagedBlocksFromConfig(cfg)
	require.NoError(t, err)
	assert.True(t, changed, "a config with a managed block must be rewritten")

	got, err := os.ReadFile(cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "triagent-cloud-camunda", "managed block must be removed")
	assert.Contains(t, string(got), "[profile operator-test]", "operator profiles must be preserved")

	// Idempotent: a second pass changes nothing.
	changed, err = StripManagedBlocksFromConfig(cfg)
	require.NoError(t, err)
	assert.False(t, changed, "second pass must be a no-op")
}

func TestStripManagedBlocksFromConfig_AbsentOrClean(t *testing.T) {
	dir := t.TempDir()

	changed, err := StripManagedBlocksFromConfig(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.False(t, changed, "absent file is a no-op")

	clean := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(clean, []byte("[profile operator-test]\nregion = eu-west-1\n"), 0o600))
	changed, err = StripManagedBlocksFromConfig(clean)
	require.NoError(t, err)
	assert.False(t, changed, "config without managed blocks is a no-op")
}
