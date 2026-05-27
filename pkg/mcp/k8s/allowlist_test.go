package k8s

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAllowlist_EmbeddedDefault(t *testing.T) {
	t.Parallel()

	list, err := LoadAllowlist("")
	require.NoError(t, err, "load default")
	require.NotEmpty(t, list.Kinds, "default allow-list is empty")

	var hasPod, hasConfigMap bool
	for _, k := range list.Kinds {
		if k.Kind == "Pod" && k.Group == "" {
			hasPod = true
		}
		if k.Kind == "ConfigMap" && k.Group == "" {
			hasConfigMap = true
			assert.True(t, k.Redact, "default ConfigMap entry must have redact:true")
		}
	}
	assert.True(t, hasPod, "default allow-list missing Pod")
	assert.True(t, hasConfigMap, "default allow-list missing ConfigMap")
}

func TestLoadAllowlist_OverrideFile(t *testing.T) {
	t.Parallel()

	tmp := filepath.Join(t.TempDir(), "custom.json")
	err := os.WriteFile(tmp, []byte(`{"kinds":[
		{"group":"zeebe.example.io","version":"v1","kind":"ZeebeCluster","description":"top-level"}
	]}`), 0o600)
	require.NoError(t, err)

	list, err := LoadAllowlist(tmp)
	require.NoError(t, err, "load override")
	require.Len(t, list.Kinds, 1)
	assert.Equal(t, "ZeebeCluster", list.Kinds[0].Kind)
	assert.Equal(t, "top-level", list.Kinds[0].Description, "description did not pass through")
}

func TestLoadAllowlist_DropsSecret(t *testing.T) {
	t.Parallel()

	tmp := filepath.Join(t.TempDir(), "secret.json")
	err := os.WriteFile(tmp, []byte(`{"kinds":[
		{"group":"","version":"v1","kind":"Secret"},
		{"group":"core","version":"v1","kind":"secret"},
		{"group":"","version":"v1","kind":"Pod"}
	]}`), 0o600)
	require.NoError(t, err)

	list, err := LoadAllowlist(tmp)
	require.NoError(t, err, "load")
	for _, k := range list.Kinds {
		assert.NotEqual(t, "Secret", k.Kind, "Secret survived filtering: %+v", k)
		assert.NotEqual(t, "secret", k.Kind, "Secret survived filtering: %+v", k)
	}
	require.Len(t, list.Kinds, 1, "expected only Pod to survive")
	assert.Equal(t, "Pod", list.Kinds[0].Kind, "expected only Pod to survive")
}

func TestLoadAllowlist_RejectsMissingFields(t *testing.T) {
	t.Parallel()

	tmp := filepath.Join(t.TempDir(), "bad.json")
	err := os.WriteFile(tmp, []byte(`{"kinds":[{"group":"","version":"","kind":"Pod"}]}`), 0o600)
	require.NoError(t, err)

	_, err = LoadAllowlist(tmp)
	require.Error(t, err, "expected error for missing version")
}

func TestLoadAllowlist_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := LoadAllowlist("/does/not/exist.json")
	require.Error(t, err, "expected error for missing file")
}

func TestEmbeddedDefaultsAreGeneric(t *testing.T) {
	data := defaultKindsJSON
	var raw struct {
		Kinds []struct {
			Group string `json:"group"`
			Kind  string `json:"kind"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range raw.Kinds {
		if strings.Contains(k.Group, "example.io") {
			t.Errorf("Org-specific group %q must not be in embedded baseline (kind=%q)", k.Group, k.Kind)
		}
		if strings.Contains(k.Group, "upbound.io") || strings.Contains(k.Group, "crossplane.io") {
			t.Errorf("Crossplane group %q must not be in embedded baseline (kind=%q)", k.Group, k.Kind)
		}
	}
}
