package teleport_test

import (
	"testing"

	tp "github.com/sourcehawk/triagent/pkg/auth/teleport"
)

func TestNewProviderReturnsClusterProvider(t *testing.T) {
	p := tp.NewProvider(tp.Config{
		Proxy:         "example.teleport.sh",
		AuthConnector: "okta",
	})
	if p == nil {
		t.Fatal("NewProvider returned nil")
	}
}

func TestConfigDefaults(t *testing.T) {
	// An empty Config must still construct a provider — callers will
	// have validated upstream. Construction is cheap; auth happens lazily.
	p := tp.NewProvider(tp.Config{})
	if p == nil {
		t.Fatal("NewProvider returned nil for empty Config")
	}
}

func TestProviderImplementsIsAuthenticated(t *testing.T) {
	type isAuther interface {
		IsAuthenticated() bool
	}
	p := tp.NewProvider(tp.Config{})
	if _, ok := p.(isAuther); !ok {
		t.Fatal("teleport provider does not expose IsAuthenticated()")
	}
}
