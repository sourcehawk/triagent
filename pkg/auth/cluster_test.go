package auth_test

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/pkg/auth"
)

// Compile-time check: the test file does not compile if the interface
// or types are missing the expected shape.
var _ auth.Provider = (*stubProvider)(nil)

type stubProvider struct{}

func (stubProvider) ListClusters(ctx context.Context) ([]auth.Cluster, error) {
	return nil, nil
}
func (stubProvider) Login(ctx context.Context, name string) (*auth.LoginResult, error) {
	return &auth.LoginResult{ClusterName: name, ContextName: name}, nil
}
func (stubProvider) IsAuthenticated() bool { return true }

func TestClusterTypes(t *testing.T) {
	c := auth.Cluster{Name: "n", ID: "id", Labels: map[string]string{"env": "dev"}}
	if c.FilterValue() == "" {
		t.Fatal("FilterValue empty")
	}
	if len(c.Columns()) != 2 {
		t.Fatalf("Columns len=%d, want 2", len(c.Columns()))
	}
}
