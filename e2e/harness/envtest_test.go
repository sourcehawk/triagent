//go:build e2e

package harness

import (
	"context"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// setupK8s boots the shared envtest (once), creates a fresh namespace, applies
// the requested fixture manifests, and writes a static kubeconfig into stateDir.
// The returned kubeconfig must resolve to envtest's apiserver and carry a
// context named e2eKubeContext; the applied fixtures must be readable through
// a client built from it. envtestGuardSkip lets the assertion run only when
// the kubebuilder assets are present.
func TestSetupK8s_AppliesFixturesAndWritesKubeconfig(t *testing.T) {
	if reason := envtestUnavailable(); reason != "" {
		t.Skipf("envtest assets unavailable: %s", reason)
	}

	stateDir := t.TempDir()
	res, err := setupK8s(t, stateDir, Options{K8sEnvtest: true, K8s: "with-namespaces-and-pods"})
	if err != nil {
		t.Fatalf("setupK8s: %v", err)
	}
	if res.kubeconfigPath == "" {
		t.Fatal("setupK8s returned empty kubeconfig path")
	}
	if _, err := os.Stat(res.kubeconfigPath); err != nil {
		t.Fatalf("kubeconfig not written: %v", err)
	}

	cfg, err := clientcmd.LoadFromFile(res.kubeconfigPath)
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	if _, ok := cfg.Contexts[e2eKubeContext]; !ok {
		t.Fatalf("kubeconfig has no %q context (contexts: %v)", e2eKubeContext, cfg.Contexts)
	}

	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: res.kubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: e2eKubeContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("build rest config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}

	ctx := context.Background()
	// The two fixture namespaces must exist.
	for _, ns := range []string{"team-a", "team-b"} {
		if _, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err != nil {
			t.Errorf("namespace %q not applied: %v", ns, err)
		}
	}
	// team-a must carry the fixture pods, one of them Failed.
	pods, err := cs.CoreV1().Pods("team-a").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods in team-a: %v", err)
	}
	var failed int
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodFailed {
			failed++
		}
	}
	if len(pods.Items) == 0 {
		t.Error("team-a has no fixture pods")
	}
	if failed == 0 {
		t.Error("team-a has no Failed pod (fixture expects one)")
	}
}
