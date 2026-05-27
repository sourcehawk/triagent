package k8sx

import (
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// New builds a Client by reading kubeconfig at kubeconfigPath and selecting
// contextName. namespace may be empty for scope-unknown investigations —
// callers that pass it use NamespaceExists / CanListPods only when it is
// non-empty; the agent-facing MCP tools take a per-call namespace arg.
//
// If kubeconfigPath is empty, the usual client-go defaults apply:
// $KUBECONFIG first, then ~/.kube/config.
func New(kubeconfigPath, contextName, namespace string) (*Client, error) {
	cfg, err := BuildRESTConfig(kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clientset: %w", err)
	}

	return &Client{
		Clientset: cs,
		Namespace: namespace,
	}, nil
}

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
