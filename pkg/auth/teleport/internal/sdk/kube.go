// Vendored and adapted from an internal SDK (teleport/k8s/kube.go).
// Package renamed to sdk for use in github.com/sourcehawk/triagent.
// auth types replaced with pkg/auth types; NaturalLess inlined via
// github.com/maruel/natural (already an indirect dep).

package sdk

import (
	"context"
	"fmt"
	"sort"

	"github.com/charmbracelet/log"
	"github.com/gravitational/teleport/api/client"
	apiproto "github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/profile"
	"github.com/gravitational/teleport/api/types"
	"github.com/maruel/natural"
	"github.com/sourcehawk/triagent/pkg/auth"
)

// AuthService abstracts Teleport authentication for testability.
type AuthService interface {
	EnsureLoggedIn() (*profile.Profile, error)
	FindTshBinary() (string, error)
	IsAuthenticated() bool
}

// TeleportClient abstracts the Teleport API client operations we need.
type TeleportClient interface {
	GetKubernetesServers(ctx context.Context) ([]types.KubeServer, error)
	GenerateUserCerts(ctx context.Context, req apiproto.UserCertsRequest) (*apiproto.Certs, error)
	Close() error
}

// ClientFactory creates a TeleportClient from a profile.
type ClientFactory interface {
	New(ctx context.Context) (TeleportClient, error)
}

// CredentialWriter handles generating and writing kube credentials to disk.
type CredentialWriter interface {
	GenerateKubeCredentials(ctx context.Context, clt TeleportClient, prof *profile.Profile, kubeName string) error
}

// CALoader loads the Teleport CA certificate.
type CALoader interface {
	LoadTeleportCA(prof *profile.Profile) ([]byte, error)
}

// KubeconfigUpdater updates the kubeconfig file.
type KubeconfigUpdater interface {
	UpdateKubeconfig(kubeName, teleportCluster, proxyAddr, tshPath string, caData []byte) error
}

// --- Default implementations ---

// defaultAuthService delegates to the local auth functions.
type defaultAuthService struct {
	inner *Auth
}

func (d defaultAuthService) EnsureLoggedIn() (*profile.Profile, error) { return d.inner.EnsureLoggedIn() }
func (d defaultAuthService) FindTshBinary() (string, error)            { return d.inner.FindTshBinary() }
func (d defaultAuthService) IsAuthenticated() bool                     { return d.inner.IsAuthenticated() }

// defaultClientFactory creates a real Teleport API client using profile credentials.
type defaultClientFactory struct{}

func (defaultClientFactory) New(ctx context.Context) (TeleportClient, error) {
	return client.New(ctx, client.Config{
		Credentials: []client.Credentials{
			client.LoadProfile("", ""),
		},
	})
}

// defaultCredentialWriter uses the real generateKubeCredentials function.
type defaultCredentialWriter struct{}

func (defaultCredentialWriter) GenerateKubeCredentials(ctx context.Context, clt TeleportClient, prof *profile.Profile, kubeName string) error {
	return generateKubeCredentials(ctx, clt, prof, kubeName)
}

// defaultCALoader uses the real loadTeleportCA function.
type defaultCALoader struct{}

func (defaultCALoader) LoadTeleportCA(prof *profile.Profile) ([]byte, error) {
	return loadTeleportCA(prof)
}

// defaultKubeconfigUpdater uses the real updateKubeconfig function.
type defaultKubeconfigUpdater struct{}

func (defaultKubeconfigUpdater) UpdateKubeconfig(kubeName, teleportCluster, proxyAddr, tshPath string, caData []byte) error {
	return updateKubeconfig(kubeName, teleportCluster, proxyAddr, tshPath, caData)
}

// Provider implements auth.Provider using Teleport as the access layer.
type Provider struct {
	// Auth handles Teleport SSO authentication and session management.
	Auth AuthService
	// Clients creates Teleport API client connections.
	Clients ClientFactory
	// Creds generates and writes kube-scoped TLS credentials.
	Creds CredentialWriter
	// CA loads the Teleport CA certificate for kubeconfig trust.
	CA CALoader
	// Kubeconfig updates the local kubeconfig file.
	Kubeconfig KubeconfigUpdater
}

// NewProvider returns a new Teleport-backed Kubernetes provider with default dependencies.
func NewProvider() *Provider {
	a := DefaultAuth
	return &Provider{
		Auth:       defaultAuthService{inner: a},
		Clients:    defaultClientFactory{},
		Creds:      defaultCredentialWriter{},
		CA:         defaultCALoader{},
		Kubeconfig: defaultKubeconfigUpdater{},
	}
}

// newProviderWithAuth returns a Provider that uses the given Auth instance.
func newProviderWithAuth(a *Auth) *Provider {
	return &Provider{
		Auth:       defaultAuthService{inner: a},
		Clients:    defaultClientFactory{},
		Creds:      defaultCredentialWriter{},
		CA:         defaultCALoader{},
		Kubeconfig: defaultKubeconfigUpdater{},
	}
}

// NewProviderWithConfig returns a Provider configured with the given proxy and connector.
func NewProviderWithConfig(proxy, connector string) *Provider {
	return newProviderWithAuth(newAuth(proxy, connector))
}

// IsAuthenticated reports whether a valid, non-expired Teleport session exists
// without triggering a login.
func (p *Provider) IsAuthenticated() bool {
	return p.Auth.IsAuthenticated()
}

// EnsureAuthenticated ensures a valid Teleport session exists, triggering SSO
// login if needed. Use this to separate the auth phase from cluster listing.
func (p *Provider) EnsureAuthenticated(ctx context.Context) error {
	_, err := p.Auth.EnsureLoggedIn()
	return err
}

// ListClusters returns all Kubernetes clusters registered in Teleport,
// deduplicated by name and sorted by env label (dev < int < prod) then
// by name using natural ordering.
func (p *Provider) ListClusters(ctx context.Context) ([]auth.Cluster, error) {
	if _, err := p.Auth.EnsureLoggedIn(); err != nil {
		return nil, err
	}

	clt, err := p.Clients.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting to Teleport: %w", err)
	}
	defer func() { _ = clt.Close() }()

	kubeServers, err := clt.GetKubernetesServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Kubernetes clusters: %w", err)
	}

	return buildClusterList(kubeServers), nil
}

// buildClusterList deduplicates kube servers by name and sorts the result.
func buildClusterList(kubeServers []types.KubeServer) []auth.Cluster {
	seen := make(map[string]bool)
	clusters := make([]auth.Cluster, 0, len(kubeServers))
	for _, ks := range kubeServers {
		cluster := ks.GetCluster()
		name := cluster.GetName()
		if seen[name] {
			continue
		}
		seen[name] = true
		labels := cluster.GetStaticLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		clusters = append(clusters, auth.Cluster{
			Name:   name,
			ID:     labels["full_id"],
			Labels: labels,
		})
	}

	// Sort by env label (dev < int < prod), then by name (natural order).
	envOrder := map[string]int{"dev": 0, "int": 1, "prod": 2}
	sort.Slice(clusters, func(i, j int) bool {
		ei := envOrder[clusters[i].Labels["env"]]
		ej := envOrder[clusters[j].Labels["env"]]
		if ei != ej {
			return ei < ej
		}
		return natural.Less(clusters[i].Name, clusters[j].Name)
	})

	return clusters
}

// Login logs into a Kubernetes cluster via Teleport. It generates
// kube-scoped TLS credentials, writes them to ~/.tsh, and updates the
// user's kubeconfig with an exec credential plugin pointing to tsh.
func (p *Provider) Login(ctx context.Context, clusterName string) (*auth.LoginResult, error) {
	prof, err := p.Auth.EnsureLoggedIn()
	if err != nil {
		return nil, err
	}

	proxyAddr := prof.WebProxyAddr
	username := prof.Username
	siteName := prof.SiteName

	log.Info("Using Teleport profile", "proxy", proxyAddr, "user", username, "cluster", siteName)

	clt, err := p.Clients.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting to Teleport: %w", err)
	}
	defer func() { _ = clt.Close() }()

	// Verify the requested kube cluster exists.
	kubeServers, err := clt.GetKubernetesServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Kubernetes clusters: %w", err)
	}

	found := false
	for _, ks := range kubeServers {
		if ks.GetCluster().GetName() == clusterName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("kubernetes cluster %q not found in Teleport", clusterName)
	}

	// Generate kube credentials and write them to ~/.tsh.
	if err := p.Creds.GenerateKubeCredentials(ctx, clt, prof, clusterName); err != nil {
		return nil, err
	}

	tshPath, err := p.Auth.FindTshBinary()
	if err != nil {
		return nil, err
	}

	caData, err := p.CA.LoadTeleportCA(prof)
	if err != nil {
		return nil, fmt.Errorf("loading Teleport CA: %w", err)
	}

	if err := p.Kubeconfig.UpdateKubeconfig(clusterName, siteName, proxyAddr, tshPath, caData); err != nil {
		return nil, fmt.Errorf("updating kubeconfig: %w", err)
	}

	contextName := kubeContextName(siteName, clusterName)
	log.Info("Logged in to Kubernetes cluster", "cluster", clusterName)
	log.Info("Context set", "context", contextName)

	return &auth.LoginResult{
		ClusterName: clusterName,
		ContextName: contextName,
	}, nil
}
