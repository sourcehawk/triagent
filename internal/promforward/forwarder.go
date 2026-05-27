// Package promforward owns the launcher-side Prometheus port-forward
// lifecycle. The Manager (Task 5) exposes a Get(invID, contextName) API
// that returns the loopback URL of an active port-forward, provisioning
// one lazily when context first appears. Drives prom MCP rebinding via
// the resolver endpoint in internal/server (Task 6).
package promforward

import (
	"context"
	"fmt"
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Target identifies the in-cluster Prometheus service to forward to.
// Mirrors profile.Defaults.Prometheus.
type Target struct {
	Service   string
	Namespace string
	Port      int
}

// PortForwarder is the interface the Manager uses to actually establish
// a port-forward. The production implementation wraps
// k8s.io/client-go/tools/portforward; tests inject a stub.
type PortForwarder interface {
	// Start blocks until the forward is established or ctx fires.
	// Returns the local loopback URL the launcher should hand out.
	Start(ctx context.Context) (string, error)
	// Stop tears down the forward. Idempotent.
	Stop()
}

// Factory builds a PortForwarder for a (rest.Config, kubernetes.Clientset, Target).
// Production wires this to newClientGoForwarder (Task 4); tests inject a stub.
type Factory func(cfg *rest.Config, cs kubernetes.Interface, target Target) (PortForwarder, error)

type resolveTargetResult struct {
	podName      string
	podNamespace string
	podPort      int
}

// resolveTarget finds a single running pod backing target.Service.
// client-go's portforward only accepts a pod, so we resolve the service
// to a pod selector and pick the first running pod.
func resolveTarget(cs kubernetes.Interface, target Target) (resolveTargetResult, error) {
	svc, err := cs.CoreV1().Services(target.Namespace).Get(context.Background(), target.Service, metav1.GetOptions{})
	if err != nil {
		return resolveTargetResult{}, fmt.Errorf("get service %s/%s: %w", target.Namespace, target.Service, err)
	}
	if len(svc.Spec.Selector) == 0 {
		return resolveTargetResult{}, fmt.Errorf("service %s/%s has no selector", target.Namespace, target.Service)
	}
	selector := labelSelector(svc.Spec.Selector)
	pods, err := cs.CoreV1().Pods(target.Namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return resolveTargetResult{}, fmt.Errorf("list pods for %s: %w", svc.Name, err)
	}
	if len(pods.Items) == 0 {
		return resolveTargetResult{}, fmt.Errorf("no pods match selector %q for service %s/%s", selector, target.Namespace, target.Service)
	}
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		return resolveTargetResult{
			podName:      p.Name,
			podNamespace: p.Namespace,
			podPort:      target.Port,
		}, nil
	}
	return resolveTargetResult{}, fmt.Errorf("no running pods for service %s/%s", target.Namespace, target.Service)
}

func labelSelector(m map[string]string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

// freeLoopbackPort returns a free local port for client-go to bind on.
// Brief TOCTOU window — another process can grab it between Close and
// Listen — but in practice rare on a developer machine.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
