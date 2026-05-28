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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Target identifies the in-cluster Prometheus service to forward to.
// Mirrors profile.Defaults.Prometheus. The JSON tags match the
// lower-camel shape the rest of the API uses (e.g. promOverrideBody on
// the form side, persisted promPersistedTarget on disk) so clients see
// one consistent field naming.
type Target struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace,omitempty"`
	Port      int    `json:"port,omitempty"`
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
	// IsAlive reports whether the underlying forwarder loop is still
	// running. The Manager's cache-hit path consults this so a forward
	// that died after becoming ready (pod restart, SPDY drop) is
	// re-provisioned on the next Get rather than handing the prom MCP
	// a dead URL forever.
	IsAlive() bool
}

// Factory builds a PortForwarder for a (rest.Config, kubernetes.Clientset, Target).
// Production wires this to newClientGoForwarder (Task 4); tests inject a stub.
type Factory func(cfg *rest.Config, cs kubernetes.Interface, target Target) (PortForwarder, error)

type resolveTargetResult struct {
	podName      string
	podNamespace string
	podPort      int
}

// resolveTarget finds a single running pod backing target.Service and
// resolves the actual pod port from the matching ServicePort.TargetPort.
// client-go's portforward only accepts a pod, so we resolve the service
// to a pod selector and pick the first running pod.
func resolveTarget(ctx context.Context, cs kubernetes.Interface, target Target) (resolveTargetResult, error) {
	svc, err := cs.CoreV1().Services(target.Namespace).Get(ctx, target.Service, metav1.GetOptions{})
	if err != nil {
		return resolveTargetResult{}, fmt.Errorf("get service %s/%s: %w", target.Namespace, target.Service, err)
	}
	if len(svc.Spec.Selector) == 0 {
		return resolveTargetResult{}, fmt.Errorf("service %s/%s has no selector", target.Namespace, target.Service)
	}

	// Find the ServicePort matching target.Port so we can resolve its
	// TargetPort. Kubernetes lets Services expose port X while routing
	// to a different numeric or named port on the pod; we can't assume
	// they're the same.
	var svcPort *corev1.ServicePort
	for i := range svc.Spec.Ports {
		if int(svc.Spec.Ports[i].Port) == target.Port {
			svcPort = &svc.Spec.Ports[i]
			break
		}
	}
	if svcPort == nil {
		return resolveTargetResult{}, fmt.Errorf("service %s/%s has no port matching %d", target.Namespace, target.Service, target.Port)
	}

	selector := labelSelector(svc.Spec.Selector)
	pods, err := cs.CoreV1().Pods(target.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return resolveTargetResult{}, fmt.Errorf("list pods for %s: %w", svc.Name, err)
	}
	if len(pods.Items) == 0 {
		return resolveTargetResult{}, fmt.Errorf("no pods match selector %q for service %s/%s", selector, target.Namespace, target.Service)
	}
	// Require both Running phase and Ready=True. Ready is the same
	// signal kube-proxy uses to route Service traffic; a pod that is
	// Running but still failing startup/readiness probes is excluded
	// from the Service endpoints, so port-forwarding to it would bind
	// the prom MCP to a backend the Service itself wouldn't trust.
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		if !isPodReady(&p) {
			continue
		}
		podPort, err := resolvePodPort(&p, svcPort.TargetPort)
		if err != nil {
			return resolveTargetResult{}, fmt.Errorf("resolve target port for pod %s: %w", p.Name, err)
		}
		return resolveTargetResult{
			podName:      p.Name,
			podNamespace: p.Namespace,
			podPort:      podPort,
		}, nil
	}
	return resolveTargetResult{}, fmt.Errorf("no ready running pods for service %s/%s", target.Namespace, target.Service)
}

// isPodReady reports whether the pod has its PodReady condition set to
// True. Mirrors k8s.io/kubernetes/pkg/api/v1/pod.IsPodReady without
// pulling in the kube-internal dependency.
func isPodReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// resolvePodPort converts a ServicePort.TargetPort to a numeric pod
// port. Int values pass through; named string ports get looked up on
// the pod's container specs.
func resolvePodPort(pod *corev1.Pod, target intstr.IntOrString) (int, error) {
	switch target.Type {
	case intstr.Int:
		return int(target.IntVal), nil
	case intstr.String:
		for _, c := range pod.Spec.Containers {
			for _, port := range c.Ports {
				if port.Name == target.StrVal {
					return int(port.ContainerPort), nil
				}
			}
		}
		return 0, fmt.Errorf("named port %q not found on pod containers", target.StrVal)
	default:
		return 0, fmt.Errorf("unknown TargetPort type %v", target.Type)
	}
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
