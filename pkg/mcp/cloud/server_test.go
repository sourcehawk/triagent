package cloud

import "testing"

func TestNewRequiresProvider(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error when Provider is nil")
	}
	if _, err := New(Options{Provider: &fakeProvider{}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
