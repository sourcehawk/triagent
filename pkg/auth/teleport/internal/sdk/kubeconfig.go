// Vendored and adapted from an internal SDK (teleport/k8s/kubeconfig.go).
// Package renamed to sdk for use in github.com/sourcehawk/triagent.

package sdk

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	apiproto "github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/profile"
	"github.com/gravitational/teleport/api/utils/keypaths"
	"github.com/gravitational/teleport/api/utils/keys"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// generateKubeCredentials requests kube-scoped TLS certificates from Teleport
// and writes them to the standard ~/.tsh credential paths.
func generateKubeCredentials(ctx context.Context, clt TeleportClient, prof *profile.Profile, kubeName string) error {
	username := prof.Username
	siteName := prof.SiteName
	proxyAddr := prof.WebProxyAddr

	privKey, err := keys.LoadPrivateKey(prof.UserTLSKeyPath())
	if err != nil {
		return fmt.Errorf("loading TLS private key from profile: %w", err)
	}

	pubKeyPEM, err := privKey.MarshalTLSPublicKey()
	if err != nil {
		return fmt.Errorf("marshalling TLS public key: %w", err)
	}

	certs, err := clt.GenerateUserCerts(ctx, apiproto.UserCertsRequest{
		TLSPublicKey:      pubKeyPEM,
		Username:          username,
		Expires:           time.Now().Add(8 * time.Hour),
		KubernetesCluster: kubeName,
		Usage:             apiproto.UserCertsRequest_Kubernetes,
		RouteToCluster:    siteName,
	})
	if err != nil {
		return fmt.Errorf("generating kube certificates: %w", err)
	}

	if len(certs.TLS) == 0 {
		return fmt.Errorf("teleport returned empty TLS certificate")
	}

	tshDir := prof.Dir
	proxyHost, _, _ := net.SplitHostPort(proxyAddr)
	if proxyHost == "" {
		proxyHost = proxyAddr
	}

	kubeCredDir := keypaths.KubeCredentialDir(tshDir, proxyHost, username, siteName)
	if err := os.MkdirAll(kubeCredDir, 0o700); err != nil {
		return fmt.Errorf("creating kube credential directory: %w", err)
	}

	// Write the combined credential file (TLS key + two newlines + cert).
	// tsh v18+ expects key and cert PEM blocks separated by two newlines.
	privKeyPEM := privKey.PrivateKeyPEM()
	credData := make([]byte, 0, len(privKeyPEM)+1+len(certs.TLS))
	credData = append(credData, privKeyPEM...)
	if len(credData) > 0 && credData[len(credData)-1] != '\n' {
		credData = append(credData, '\n')
	}
	credData = append(credData, '\n')
	credData = append(credData, certs.TLS...)

	credPath := keypaths.KubeCredPath(tshDir, proxyHost, username, siteName, kubeName)
	if err := os.WriteFile(credPath, credData, 0o600); err != nil {
		return fmt.Errorf("writing kube credential: %w", err)
	}

	var caCerts []byte
	for _, ca := range certs.TLSCACerts {
		caCerts = append(caCerts, ca...)
	}

	caPath := filepath.Join(kubeCredDir, kubeName+"-ca.pem")
	if err := os.WriteFile(caPath, caCerts, 0o600); err != nil {
		return fmt.Errorf("writing kube CA certs: %w", err)
	}

	return nil
}

// loadTeleportCA reads the Teleport proxy CA certificate from the tsh profile.
func loadTeleportCA(prof *profile.Profile) ([]byte, error) {
	clusterCAPath := prof.TLSCAPathCluster(prof.SiteName)
	if data, err := os.ReadFile(clusterCAPath); err == nil && len(data) > 0 {
		return data, nil
	}

	legacyCAPath := prof.TLSCAsPath()
	if data, err := os.ReadFile(legacyCAPath); err == nil && len(data) > 0 {
		return data, nil
	}

	return nil, fmt.Errorf("no Teleport CA certificate found at %s", clusterCAPath)
}

// kubeContextName returns the kubeconfig context name for a Teleport kube cluster.
func kubeContextName(teleportCluster, kubeCluster string) string {
	return fmt.Sprintf("%s-%s", teleportCluster, kubeCluster)
}

// updateKubeconfig adds or updates a kubeconfig entry for the given kube cluster.
func updateKubeconfig(kubeName, teleportCluster, proxyAddr, tshPath string, caData []byte) error {
	contextName := kubeContextName(teleportCluster, kubeName)
	clusterName := contextName
	userName := contextName

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig, err := loadingRules.Load()
	if err != nil {
		kubeConfig = clientcmdapi.NewConfig()
	}

	extensions := map[string]string{
		"kubeconfig.teleport.dev/profile-name":          teleportCluster,
		"kubeconfig.teleport.dev/teleport-cluster-name": teleportCluster,
		"teleport.kube.name":                            kubeName,
	}

	serverURL := "https://" + teleportCluster + ":443"

	kubeConfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   serverURL,
		CertificateAuthorityData: caData,
		TLSServerName:            "kube-teleport-proxy-alpn." + teleportCluster,
		Extensions:               makeExtensions(extensions),
	}

	kubeConfig.AuthInfos[userName] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    tshPath,
			Args: []string{
				"kube",
				"credentials",
				"--kube-cluster=" + kubeName,
				"--teleport-cluster=" + teleportCluster,
				"--proxy=" + proxyAddr,
			},
		},
		Extensions: makeExtensions(extensions),
	}

	kubeConfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:    clusterName,
		AuthInfo:   userName,
		Extensions: makeExtensions(extensions),
	}

	kubeConfig.CurrentContext = contextName

	configPath := loadingRules.GetDefaultFilename()
	if err := clientcmd.WriteToFile(*kubeConfig, configPath); err != nil {
		return fmt.Errorf("writing kubeconfig to %s: %w", configPath, err)
	}

	return nil
}

// makeExtensions converts string key-value pairs to kubeconfig extension objects.
func makeExtensions(exts map[string]string) map[string]runtime.Object {
	result := make(map[string]runtime.Object, len(exts))
	for k, v := range exts {
		result[k] = &runtime.Unknown{
			Raw: []byte(fmt.Sprintf(`{"extension":"%s"}`, v)),
		}
	}
	return result
}
