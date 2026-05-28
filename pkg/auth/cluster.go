// Package auth defines provider-agnostic types and interfaces for
// Kubernetes cluster access. The package name "auth" matches the directory
// path pkg/auth/ and avoids colliding with k8s.io/* in import lists.
package auth

import (
	"context"
	"errors"
	"fmt"
)

// Cluster represents a Kubernetes cluster available through an access provider.
//
// KubeContext is the kubeconfig context name the provider uses to talk
// to this cluster — populated by providers that own the kubeconfig
// layout (e.g. Teleport's `<teleportCluster>-<kubeCluster>` form).
// Empty for providers that don't know the answer; downstream callers
// fall back to the agent invoking switch_context.
type Cluster struct {
	Name        string            `json:"name"`
	ID          string            `json:"full_id"`
	KubeContext string            `json:"kubeContext,omitempty"`
	Labels      map[string]string `json:"labels"`
}

// FilterValue returns the text used for interactive search matching.
func (c Cluster) FilterValue() string {
	return fmt.Sprintf("%s %s", c.Name, c.ID)
}

// Columns returns the display values for the picker's table columns.
func (c Cluster) Columns() []string {
	return []string{c.Name, c.ID}
}

// LoginResult contains information about a successful cluster login.
type LoginResult struct {
	ClusterName string
	ContextName string
}

// Provider is the interface for listing and authenticating to Kubernetes
// clusters through an access layer (e.g. Teleport, kubeconfig, gcloud).
type Provider interface {
	ListClusters(ctx context.Context) ([]Cluster, error)
	Login(ctx context.Context, clusterName string) (*LoginResult, error)
	// IsAuthenticated reports whether the provider currently holds a valid
	// session. Cheap to call. Used by preflight to decide whether to bail
	// with a provider-specific re-auth instruction.
	IsAuthenticated() bool
}

// ErrAuthExpired is returned by preflight when the provider reports no
// active session. Providers wrap it in a user-facing message; preflight
// surfaces the wrapped error verbatim.
var ErrAuthExpired = errors.New("auth session expired or missing")

// ReauthAdvisor is an optional interface a provider may implement to
// supply a user-facing re-authentication instruction. Preflight calls
// it when IsAuthenticated() returns false AND the provider does not
// implement Authenticator (or its auto-login attempt failed).
type ReauthAdvisor interface {
	ReauthAdvice() string
}

// Authenticator is an optional interface a provider may implement to
// attempt establishing a session automatically when one is missing
// (e.g. Teleport's tsh browser SSO flow). Preflight calls
// EnsureAuthenticated when IsAuthenticated() returns false, before
// falling back to ReauthAdvice. The call blocks until the operator
// finishes the flow or cancels.
type Authenticator interface {
	EnsureAuthenticated(ctx context.Context) error
}
