package main

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/pkg/auth"
)

func TestSetProviderAcceptsClusterProvider(t *testing.T) {
	var p auth.Provider = stubProvider{}
	SetProvider(p)
	if provider == nil {
		t.Fatal("provider not set")
	}
	if _, ok := provider.(stubProvider); !ok {
		t.Fatalf("SetProvider did not store the value (got %T)", provider)
	}
}

type stubProvider struct{}

func (stubProvider) ListClusters(ctx context.Context) ([]auth.Cluster, error) {
	return nil, nil
}
func (stubProvider) Login(ctx context.Context, name string) (*auth.LoginResult, error) {
	return &auth.LoginResult{ClusterName: name, ContextName: name}, nil
}
func (stubProvider) IsAuthenticated() bool { return true }
