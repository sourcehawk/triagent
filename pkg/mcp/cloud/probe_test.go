package cloud

import (
	"context"
	"errors"
	"testing"
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !st.Valid || st.AssumedIdentity != "ro-sa@proj.iam.gserviceaccount.com" {
		t.Fatalf("probe did not return the provider identity: %+v", st)
	}
}

func TestProbeSurfacesProviderErrorAsInvalid(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "aws", identityErr: errors.New("token expired")}
	st, err := Probe(context.Background(), p)
	if err != nil {
		t.Fatalf("Probe should degrade, not error: %v", err)
	}
	if st.Valid {
		t.Fatal("expected Valid=false when the provider errors")
	}
	if st.Provider != "aws" {
		t.Fatalf("expected provider name carried through, got %q", st.Provider)
	}
	if st.Hint == "" {
		t.Fatal("expected the provider error surfaced as a hint")
	}
}

func TestProbeInvalidWhenIdentityEmpty(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "gcp", identity: IdentityStatus{Provider: "gcp", Valid: true}}
	st, err := Probe(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Valid {
		t.Fatal("an empty resolved identity must not be reported valid")
	}
}
