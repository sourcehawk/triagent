package promforward

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubeBuilder turns an investigation id and a kubeconfig context name into a
// (rest.Config, kubernetes.Interface) pair. The Manager calls it on first Get
// for a new context. Production wires it to a clientcmd-backed builder that
// reads the per-investigation KubeconfigPath via the investigation id; tests
// inject a stub.
type KubeBuilder func(investigationID, contextName string) (*rest.Config, kubernetes.Interface, error)

// Options configures the Manager.
type Options struct {
	// Factory builds a PortForwarder. Defaults to newClientGoForwarder.
	Factory Factory
	// KubeBuilder turns a context name into kube clients. Required.
	KubeBuilder KubeBuilder
	// Target is the in-cluster Prometheus service to forward to. Same
	// for every investigation in a launcher run (driven by the active
	// profile's defaults.prometheus).
	Target Target
}

// Manager owns one PortForwarder per investigation. The Get method
// provisions lazily on first use and re-provisions when the bound
// kubeconfig context changes.
type Manager struct {
	opts Options

	mu    sync.Mutex
	bound map[string]boundEntry // investigation id → current entry
}

type boundEntry struct {
	contextName string
	fwd         PortForwarder
	url         string
}

// NewManager returns a Manager with the provided options. If opts.Factory
// is nil it defaults to newClientGoForwarder.
func NewManager(opts Options) *Manager {
	if opts.Factory == nil {
		opts.Factory = newClientGoForwarder
	}
	return &Manager{opts: opts, bound: map[string]boundEntry{}}
}

// Get returns the loopback URL of the port-forward bound for the given
// investigation + kubeconfig context. Provisions a fresh forward on
// first call or when the context differs from the previously-bound one
// (the prior forward is torn down).
func (m *Manager) Get(ctx context.Context, investigationID, contextName string) (string, error) {
	if contextName == "" {
		return "", fmt.Errorf("contextName is required")
	}

	m.mu.Lock()
	if existing, ok := m.bound[investigationID]; ok && existing.contextName == contextName {
		m.mu.Unlock()
		return existing.url, nil
	}
	prior, hadPrior := m.bound[investigationID]
	delete(m.bound, investigationID)
	m.mu.Unlock()

	if hadPrior {
		// Tear down the prior forwarder before provisioning the new
		// one. A transient Start failure on the new context therefore
		// leaves the investigation with no working forward — the
		// caller's next Get retries from scratch. Keeping the old
		// forward alive as a fallback is tempting, but would mean
		// the prom MCP could see a URL bound to the OLD cluster
		// after a switch_context succeeded — a correctness hazard
		// worse than a transient outage.
		prior.fwd.Stop()
	}

	cfg, cs, err := m.opts.KubeBuilder(investigationID, contextName)
	if err != nil {
		return "", fmt.Errorf("build kube client for context %q: %w", contextName, err)
	}
	fwd, err := m.opts.Factory(cfg, cs, m.opts.Target)
	if err != nil {
		return "", fmt.Errorf("build forwarder: %w", err)
	}
	if fwd == nil {
		return "", fmt.Errorf("factory returned nil forwarder")
	}
	url, err := fwd.Start(ctx)
	if err != nil {
		fwd.Stop()
		return "", err
	}

	m.mu.Lock()
	if existing, ok := m.bound[investigationID]; ok {
		// A concurrent Get raced us and already stored an entry. The
		// caller-visible behaviour is "last writer wins" — but we must
		// not leak our just-provisioned forwarder. Stop ours, return
		// the existing URL if its context matches ours; otherwise
		// surface our URL but stop the conflict's forwarder. Order
		// doesn't matter — whichever Stop runs second is a no-op
		// because each entry holds a distinct forwarder.
		if existing.contextName == contextName {
			m.mu.Unlock()
			fwd.Stop()
			return existing.url, nil
		}
		// Different contexts raced for the same id. Stop the now-stale
		// peer and store ours.
		m.bound[investigationID] = boundEntry{contextName: contextName, fwd: fwd, url: url}
		m.mu.Unlock()
		existing.fwd.Stop()
		return url, nil
	}
	m.bound[investigationID] = boundEntry{contextName: contextName, fwd: fwd, url: url}
	m.mu.Unlock()
	return url, nil
}

// Stop tears down the forwarder for investigationID. Idempotent.
func (m *Manager) Stop(investigationID string) {
	m.mu.Lock()
	entry, ok := m.bound[investigationID]
	delete(m.bound, investigationID)
	m.mu.Unlock()
	if ok {
		entry.fwd.Stop()
	}
}
