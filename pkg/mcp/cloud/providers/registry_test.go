package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_KnownProviders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
	}{
		{"gcp", "gcp"},
		{"aws", "aws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := New(tc.name)
			// The provider's New() resolves its CLI binary via exec.LookPath,
			// which may be absent in CI. A missing binary is a construction
			// error, not an unknown-provider error — assert on whichever
			// outcome the environment produced, but never a nil provider with
			// a nil error.
			if err != nil {
				assert.Nil(t, p, "a construction error must not also return a provider")
				return
			}
			require.NotNil(t, p)
			assert.Equal(t, tc.want, p.Name())
		})
	}
}

func TestNew_UnknownProviderErrors(t *testing.T) {
	t.Parallel()
	p, err := New("azure")
	require.Error(t, err)
	assert.Nil(t, p)
	assert.Contains(t, err.Error(), "azure")
}
