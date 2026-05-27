// Package kubeconfig is the default auth.Provider implementation:
// it reads $KUBECONFIG (or ~/.kube/config) and exposes each context as
// a cluster. Login writes a per-session sub-kubeconfig pinning the
// chosen context; the launcher passes that path as KUBECONFIG to all
// spawned subprocesses (per CLAUDE.md, subprocesses get explicit
// KUBECONFIG, never inherit ambient operator shell state).
package kubeconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sourcehawk/triagent/pkg/auth"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type provider struct{}

func NewProvider() auth.Provider { return &provider{} }

func (p *provider) ListClusters(_ context.Context) ([]auth.Cluster, error) {
	cfg, err := loadKubeconfig()
	if err != nil {
		return nil, err
	}
	out := make([]auth.Cluster, 0, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		out = append(out, auth.Cluster{
			Name: name,
			ID:   ctx.Cluster,
		})
	}
	return out, nil
}

func (p *provider) Login(_ context.Context, name string) (*auth.LoginResult, error) {
	cfg, err := loadKubeconfig()
	if err != nil {
		return nil, err
	}
	if _, ok := cfg.Contexts[name]; !ok {
		return nil, fmt.Errorf("context %q not found in kubeconfig", name)
	}

	// Write a per-session kubeconfig that pins the chosen context.
	dir, err := os.MkdirTemp("", "triagent-kubeconfig-*")
	if err != nil {
		return nil, fmt.Errorf("create per-session dir: %w", err)
	}
	subPath := filepath.Join(dir, "config")
	sub := cfg.DeepCopy()
	sub.CurrentContext = name
	if err := clientcmd.WriteToFile(*sub, subPath); err != nil {
		return nil, fmt.Errorf("write sub-kubeconfig: %w", err)
	}
	return &auth.LoginResult{ClusterName: name, ContextName: subPath}, nil
}

func (p *provider) IsAuthenticated() bool { return true }

func loadKubeconfig() (*clientcmdapi.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if env := os.Getenv("KUBECONFIG"); env != "" {
		rules.ExplicitPath = env
	}
	cfg, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}
