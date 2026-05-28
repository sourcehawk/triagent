package server

import (
	"context"

	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/pkg/mcp/k8s"
)

// resolveActiveContext translates the operator's cluster_id form input
// into the kubeconfig context name the k8s MCP will bind to. Used at
// preflight to pre-seed <sessionDir>/active-context so the MCP's
// startup hydrate finds the context — without this, the first agent
// tool call still hits "no active kubernetes context" even though the
// operator already picked a cluster on the form.
//
// Returns "" with no error in every non-fatal case (no provider, no
// input, list failure, no match, or matched cluster without a
// KubeContext): the launcher degrades to the standard switch_context
// flow rather than failing preflight on a discovery hiccup. Matches
// by ID first, then by Name in a second pass — the picker sends c.id
// (full_id), so a colliding name on a different cluster must not beat
// the authoritative ID match.
func resolveActiveContext(ctx context.Context, provider auth.Provider, clusterID string) string {
	if provider == nil || clusterID == "" {
		return ""
	}
	clusters, err := provider.ListClusters(ctx)
	if err != nil {
		return ""
	}
	for _, c := range clusters {
		if c.ID == clusterID {
			return c.KubeContext
		}
	}
	for _, c := range clusters {
		if c.Name == clusterID {
			return c.KubeContext
		}
	}
	return ""
}

// seedActiveContext writes <sessionDir>/active-context so the k8s
// MCP's hydrate-on-startup path picks it up. Returns the kube context
// it wrote (empty when there was nothing to seed) plus any write
// error. Write failures are not fatal to preflight; the caller logs
// and continues — the agent can always invoke switch_context.
func seedActiveContext(ctx context.Context, provider auth.Provider, sessionDir, clusterID string) (string, error) {
	kubeContext := resolveActiveContext(ctx, provider, clusterID)
	if kubeContext == "" {
		return "", nil
	}
	if err := k8s.WriteActiveContextFile(sessionDir, kubeContext); err != nil {
		return "", err
	}
	return kubeContext, nil
}
