//go:build e2e

package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// e2eKubeContext is the single context name baked into every kubeconfig the
// harness writes for a k8s test. The stub binds to it via switch_context, and
// the launcher freezes a copy of the kubeconfig at preflight — so the name has
// to be stable and shared between the two.
const e2eKubeContext = "e2e"

// sharedEnv is the process-wide envtest cluster, booted lazily on the first
// K8s launch and torn down by TestMain after the run. One apiserver + etcd is
// shared across every k8s test; per-test isolation is a fresh namespace, not a
// fresh cluster (booting envtest costs seconds, namespaces cost milliseconds).
var (
	sharedEnvOnce sync.Once
	sharedEnv     *envtest.Environment
	sharedEnvCfg  *rest.Config
	sharedEnvErr  error
)

// k8sSetup is what setupK8s hands back to Launch: the path to the static
// kubeconfig the launcher must point KUBECONFIG at.
type k8sSetup struct {
	kubeconfigPath string
}

// EnvtestUnavailable returns a non-empty reason when the kubebuilder
// apiserver+etcd assets can't be located, so k8s tests skip cleanly on a
// runner that hasn't fetched them rather than failing the suite. An explicit
// KUBEBUILDER_ASSETS wins; otherwise the default setup-envtest cache path
// (~/.local/share/kubebuilder-envtest/k8s/<ver>) is probed. Exported so the
// e2e package's k8s test gates on the same probe the harness uses.
func EnvtestUnavailable() string { return envtestUnavailable() }

func envtestUnavailable() string {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "kube-apiserver")); err == nil {
			return ""
		}
		return fmt.Sprintf("KUBEBUILDER_ASSETS=%s has no kube-apiserver", dir)
	}
	// Probe for the actual apiserver binary, not just the directory: a cache
	// root that exists but is empty or partially populated would otherwise
	// report "available" and then fail in env.Start() instead of skipping.
	if _, err := os.Stat(filepath.Join(resolveEnvtestAssets(), "kube-apiserver")); err != nil {
		return "no KUBEBUILDER_ASSETS and no usable cached assets (kube-apiserver) under " + defaultEnvtestCacheRoot()
	}
	return ""
}

// defaultEnvtestCacheRoot is where setup-envtest caches the binary bundles.
func defaultEnvtestCacheRoot() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "kubebuilder-envtest", "k8s")
	}
	return ""
}

// resolveEnvtestAssets returns the directory holding kube-apiserver/etcd. An
// explicit KUBEBUILDER_ASSETS wins. Otherwise the newest bundle under the
// setup-envtest cache root is selected (lexical max — version dirs sort
// correctly within a single k8s minor, which is all the suite pins).
func resolveEnvtestAssets() string {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		return dir
	}
	root := defaultEnvtestCacheRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return root
	}
	var pick string
	for _, e := range entries {
		if e.IsDir() && e.Name() > pick {
			pick = e.Name()
		}
	}
	if pick == "" {
		return root
	}
	return filepath.Join(root, pick)
}

// bootSharedEnv starts the shared envtest cluster exactly once. The
// BinaryAssetsDirectory points envtest at the resolved kubebuilder assets so
// it doesn't depend on KUBEBUILDER_ASSETS being exported into the test
// process.
func bootSharedEnv() (*rest.Config, error) {
	sharedEnvOnce.Do(func() {
		env := &envtest.Environment{
			BinaryAssetsDirectory: resolveEnvtestAssets(),
		}
		cfg, err := env.Start()
		if err != nil {
			sharedEnvErr = fmt.Errorf("start envtest: %w", err)
			return
		}
		sharedEnv = env
		sharedEnvCfg = cfg
	})
	return sharedEnvCfg, sharedEnvErr
}

// stopSharedEnv tears the shared cluster down after m.Run(); a no-op when
// envtest was never booted (non-k8s runs pay nothing). The shared cluster is
// booted in whichever test binary first runs a k8s launch, so each binary
// that might boot it must stop it from its own TestMain: the harness package's
// TestMain calls this directly, and the top-level e2e package's TestMain calls
// the exported StopSharedEnv (the k8s flow lives in the e2e package, so its
// apiserver+etcd would otherwise leak when that test process exits).
func stopSharedEnv() {
	if sharedEnv != nil {
		_ = sharedEnv.Stop()
	}
}

// StopSharedEnv is the exported teardown the top-level e2e package's TestMain
// invokes after m.Run(). See stopSharedEnv for why both binaries need it.
func StopSharedEnv() { stopSharedEnv() }

// setupK8s boots the shared envtest (if needed), applies the requested fixture
// manifests (which create their own namespaces), and writes a static
// kubeconfig into stateDir. Every namespace the fixtures touched is deleted on
// t.Cleanup so reruns within one process start clean. The static kubeconfig
// carries a single context (e2eKubeContext) pointing at envtest's apiserver
// with its CA + client credentials inlined, so the launcher discovers it the
// normal way (no Teleport in the loop).
func setupK8s(t *testing.T, stateDir string, opts Options) (k8sSetup, error) {
	cfg, err := bootSharedEnv()
	if err != nil {
		return k8sSetup{}, err
	}

	kubeconfigPath := filepath.Join(stateDir, "kubeconfig.e2e.yaml")
	if err := writeStaticKubeconfig(cfg, kubeconfigPath); err != nil {
		return k8sSetup{}, fmt.Errorf("write kubeconfig: %w", err)
	}

	applied, err := applyK8sFixtures(cfg, opts.K8sFixtures)
	if err != nil {
		return k8sSetup{}, fmt.Errorf("apply k8s fixtures %q: %w", opts.K8sFixtures, err)
	}

	// Clean up the namespaces the fixtures created so reruns within one
	// process start from a clean cluster. Best-effort — the shared apiserver
	// is torn down at process exit regardless.
	t.Cleanup(func() {
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for ns := range applied {
			_ = cs.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
		}
	})

	return k8sSetup{kubeconfigPath: kubeconfigPath}, nil
}

// writeStaticKubeconfig serialises a one-context kubeconfig for cfg. CA data,
// client cert, and key are inlined so the file is self-contained (no external
// references the launcher's frozen copy could break). The context name is
// e2eKubeContext so switch_context and the launcher agree.
func writeStaticKubeconfig(cfg *rest.Config, path string) error {
	const name = e2eKubeContext
	out := clientcmdapi.NewConfig()
	out.Clusters[name] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: cfg.CAData,
	}
	out.AuthInfos[name] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
	}
	out.Contexts[name] = &clientcmdapi.Context{
		Cluster:  name,
		AuthInfo: name,
	}
	out.CurrentContext = name
	if err := clientcmd.WriteToFile(*out, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// applyK8sFixtures parses every YAML manifest under fixtures/k8s/<scenario>,
// creates each object against the apiserver, and (for pods carrying a
// declared status.phase) patches that phase onto the status subresource —
// envtest has no kubelet, so phase never advances on its own and the
// declared value is the only thing list_resources can report. It returns the
// set of namespaces touched, for cleanup. An empty scenario applies nothing.
func applyK8sFixtures(cfg *rest.Config, scenario string) (map[string]struct{}, error) {
	namespaces := map[string]struct{}{}
	if scenario == "" {
		return namespaces, nil
	}
	dir := fixtureDir("k8s", scenario)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Two passes so namespaces exist before namespaced objects land,
	// regardless of file/document order.
	var deferred []*unstructured.Unstructured
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		objs, err := decodeManifests(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, obj := range objs {
			if obj.GetKind() == "Namespace" {
				if err := applyNamespace(ctx, cs, obj.GetName()); err != nil {
					return nil, err
				}
				namespaces[obj.GetName()] = struct{}{}
				continue
			}
			deferred = append(deferred, obj)
		}
	}
	for _, obj := range deferred {
		if ns := obj.GetNamespace(); ns != "" {
			namespaces[ns] = struct{}{}
		}
		if err := applyObject(ctx, cs, obj); err != nil {
			return nil, err
		}
	}
	return namespaces, nil
}

// decodeManifests reads a (possibly multi-document) YAML file into a slice of
// unstructured objects, skipping empty documents.
func decodeManifests(path string) ([]*unstructured.Unstructured, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []*unstructured.Unstructured
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		raw := map[string]any{}
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(raw) == 0 {
			continue
		}
		out = append(out, &unstructured.Unstructured{Object: raw})
	}
	return out, nil
}

func applyNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %q: %w", name, err)
	}
	return nil
}

// applyObject creates one fixture object. Pods are the only kind the k8s flow
// exercises, so the apply path is typed to Pod: create the spec, then (when
// the manifest declares status.phase) push that phase onto the status
// subresource via UpdateStatus.
func applyObject(ctx context.Context, cs kubernetes.Interface, obj *unstructured.Unstructured) error {
	if obj.GetKind() != "Pod" {
		return fmt.Errorf("fixture kind %q unsupported by the k8s harness (only Pod / Namespace)", obj.GetKind())
	}
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	// Strip status before create — the create endpoint ignores it, and we set
	// it explicitly via the status subresource below.
	unstructured.RemoveNestedField(obj.Object, "status")

	var pod corev1.Pod
	if err := convertUnstructured(obj, &pod); err != nil {
		return fmt.Errorf("convert pod %q: %w", obj.GetName(), err)
	}
	ns := pod.Namespace
	created, err := cs.CoreV1().Pods(ns).Create(ctx, &pod, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create pod %q/%q: %w", ns, pod.Name, err)
	}
	if err == nil && phase != "" {
		created.Status.Phase = corev1.PodPhase(phase)
		if _, err := cs.CoreV1().Pods(ns).UpdateStatus(ctx, created, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("set status.phase=%s on pod %q/%q: %w", phase, ns, pod.Name, err)
		}
	}
	return nil
}

// convertUnstructured round-trips an unstructured object into a typed one via
// its YAML form. Using sigs.k8s.io YAML keeps the json tags honoured.
func convertUnstructured(obj *unstructured.Unstructured, into any) error {
	b, err := yaml.Marshal(obj.Object)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, into)
}
