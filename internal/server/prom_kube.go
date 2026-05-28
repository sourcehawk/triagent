package server

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// buildKubeConfigForContext loads path as a kubeconfig and selects
// the named context. Used by the prom port-forward manager to build
// kube clients without re-creating the loading-rules wiring at every
// call site.
func buildKubeConfigForContext(path, ctxName string) (*rest.Config, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: ctxName}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}
