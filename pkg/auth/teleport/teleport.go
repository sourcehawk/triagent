// Package teleport is the Teleport implementation of the auth.Provider
// interface. It wraps the vendored c1-sdk/teleport/{auth,k8s} slice so
// the launcher depends only on the auth.Provider interface and this adapter.
package teleport

import (
	"context"
	"fmt"

	"github.com/sourcehawk/triagent/pkg/auth"
	sdk "github.com/sourcehawk/triagent/pkg/auth/teleport/internal/sdk"
)

// Default Teleport connection values, re-exported from the vendored SDK.
// Consumers that have per-instance config should pass it via Config.
const (
	DefaultProxyAddr     = sdk.DefaultProxyAddr
	DefaultAuthConnector = sdk.DefaultAuthConnector
)

// Config carries the org-specific Teleport connection parameters.
type Config struct {
	Proxy         string
	AuthConnector string
}

// NewProvider returns an auth.Provider backed by Teleport SSO via tsh.
func NewProvider(cfg Config) auth.Provider {
	proxy := cfg.Proxy
	if proxy == "" {
		proxy = sdk.DefaultProxyAddr
	}
	connector := cfg.AuthConnector
	if connector == "" {
		connector = sdk.DefaultAuthConnector
	}
	return &adapter{inner: sdk.NewProviderWithConfig(proxy, connector), proxy: proxy, connector: connector}
}

type adapter struct {
	inner     *sdk.Provider
	proxy     string
	connector string
}

func (a *adapter) ListClusters(ctx context.Context) ([]auth.Cluster, error) {
	return a.inner.ListClusters(ctx)
}

func (a *adapter) Login(ctx context.Context, name string) (*auth.LoginResult, error) {
	return a.inner.Login(ctx, name)
}

// IsAuthenticated reports whether a valid Teleport session exists.
func (a *adapter) IsAuthenticated() bool {
	return a.inner.IsAuthenticated()
}

// ReauthAdvice returns a user-facing instruction for re-authenticating
// with Teleport when IsAuthenticated() returns false.
func (a *adapter) ReauthAdvice() string {
	return fmt.Sprintf("Teleport session expired or missing — run `tsh login --proxy=%s --auth=%s` in your terminal, then retry",
		a.proxy, a.connector)
}

// EnsureAuthenticated triggers the Teleport SSO browser flow when no
// valid session exists. Blocks until the operator finishes the redirect
// or cancels. Returns nil if a session is already (or now) present.
func (a *adapter) EnsureAuthenticated(ctx context.Context) error {
	return a.inner.EnsureAuthenticated(ctx)
}
