// Package k8sx wraps the narrow slice of client-go the investigate plugin needs:
// building a typed clientset from a kubeconfig+context, and two preflight
// checks (namespace exists, caller can list pods in it). Kept separate from
// internal/mcp so the MCP server and the UI share one construction path.
package k8sx

import (
	"context"

	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Client bundles a typed Kubernetes clientset with the namespace the plugin
// is scoped to. All MCP tool handlers receive this struct and must never
// operate outside Namespace.
type Client struct {
	Clientset kubernetes.Interface
	Namespace string
}

// NamespaceExists returns true if the bound namespace is visible to the caller.
// A NotFound error is treated as a clean "no"; other errors propagate.
func (c *Client) NamespaceExists(ctx context.Context) (bool, error) {
	_, err := c.Clientset.CoreV1().Namespaces().Get(ctx, c.Namespace, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// CanListPods probes whether the caller may list pods in the bound namespace.
// Uses a SelfSubjectAccessReview so it works for any auth backend.
// When disallowed, the human-readable reason (if any) is returned alongside.
func (c *Client) CanListPods(ctx context.Context) (bool, string, error) {
	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: c.Namespace,
				Verb:      "list",
				Resource:  "pods",
			},
		},
	}
	result, err := c.Clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return result.Status.Allowed, result.Status.Reason, nil
}
