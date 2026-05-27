package teleport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RequiresKubeconfig(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeconfig path is required")
}

func TestNew_DefaultsProvider(t *testing.T) {
	t.Parallel()
	srv, err := New(Options{KubeconfigPath: "/tmp/anything"})
	require.NoError(t, err)
	assert.NotNil(t, srv.provider, "default provider should be wired")
}
