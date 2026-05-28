package server

import (
	"fmt"

	"github.com/sourcehawk/triagent/internal/promforward"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// newPromForwarder constructs the per-investigation Prometheus
// port-forward manager. It has no profile dependency — per-
// investigation overrides (the operator's prom-config form) can
// supply the full target even when the launcher has no profile
// loaded. Provisioning is unconditional so that override-only prom
// configs don't end up with `triagent-prom` attached in the MCP
// config but a nil forwarder serving 503 "prom not configured" from
// the resolver endpoint.
func newPromForwarder(manager *Manager) *promforward.Manager {
	return promforward.NewManager(promforward.Options{
		KubeBuilder: func(invID, ctxName string) (*rest.Config, kubernetes.Interface, error) {
			inv := manager.Get(invID)
			if inv == nil {
				return nil, nil, fmt.Errorf("investigation %q not found", invID)
			}
			cfg, err := buildKubeConfigForContext(inv.KubeconfigPath, ctxName)
			if err != nil {
				return nil, nil, err
			}
			cs, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				return nil, nil, err
			}
			return cfg, cs, nil
		},
	})
}
