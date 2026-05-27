package watches

import (
	"context"
	"testing"
	"time"
)

type fakeSource struct{ kind SourceKind }

func (f fakeSource) Kind() SourceKind { return f.kind }
func (f fakeSource) Poll(_ context.Context, _ Watch, _ WatermarkState) ([]Item, WatermarkState, error) {
	return []Item{{ID: "x", CapturedAt: time.Now().UTC()}}, WatermarkState{}, nil
}

func TestRegistryDispatches(t *testing.T) {
	reg := NewSourceRegistry()
	reg.Register(fakeSource{kind: SourceGitHubIssues})
	src, ok := reg.Get(SourceGitHubIssues)
	if !ok {
		t.Fatal("expected fake to be registered")
	}
	items, _, err := src.Poll(context.Background(), Watch{}, WatermarkState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
}
