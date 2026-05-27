// Package k8sx contains the small slice of client-go construction the triagent-mcp
// binary needs. It mirrors investigate/internal/k8sx; kept separate so the
// dispatcher can build a *rest.Config without depending on investigate/.
package k8sx

import (
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// BuildRESTConfig loads kubeconfigPath (or the client-go defaults if empty)
// and selects contextName (or the kubeconfig's current context if empty),
// returning a *rest.Config ready to drive any client-go client.
func BuildRESTConfig(kubeconfigPath, contextName string) (*rest.Config, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loading.ExplicitPath = kubeconfigPath
	} else if env := os.Getenv("KUBECONFIG"); env != "" {
		loading.ExplicitPath = env
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}
