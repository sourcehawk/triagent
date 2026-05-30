package cloud

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeReturnsProviderIdentity(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{
		name: "gcp",
		identity: IdentityStatus{
			Provider:        "gcp",
			AssumedIdentity: "ro-sa@proj.iam.gserviceaccount.com",
			Valid:           true,
		},
	}
	st, err := Probe(context.Background(), p)
	require.NoError(t, err)
	assert.True(t, st.Valid)
	assert.Equal(t, "ro-sa@proj.iam.gserviceaccount.com", st.AssumedIdentity)
}

func TestProbeSurfacesProviderErrorAsInvalid(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "aws", identityErr: errors.New("token expired")}
	st, err := Probe(context.Background(), p)
	require.NoError(t, err, "Probe should degrade, not error")
	assert.False(t, st.Valid, "expected Valid=false when the provider errors")
	assert.Equal(t, "aws", st.Provider, "expected provider name carried through")
	assert.NotEmpty(t, st.Hint, "expected the provider error surfaced as a hint")
}

func TestProbeInvalidWhenIdentityEmpty(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "gcp", identity: IdentityStatus{Provider: "gcp", Valid: true}}
	st, err := Probe(context.Background(), p)
	require.NoError(t, err)
	assert.False(t, st.Valid, "an empty resolved identity must not be reported valid")
}
