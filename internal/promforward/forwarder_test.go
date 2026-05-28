package promforward

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestResolveTarget_ServiceSelectorMatchesPod(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 9090, TargetPort: intstr.FromInt(9090)},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "prometheus-0", Namespace: "monitoring",
				Labels: map[string]string{"app": "prometheus"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
	)
	res, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.NoError(t, err)
	assert.Equal(t, "prometheus-0", res.podName)
	assert.Equal(t, "monitoring", res.podNamespace)
	assert.Equal(t, 9090, res.podPort)
}

func TestResolveTarget_NoPods(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "prometheus"},
			Ports: []corev1.ServicePort{
				{Name: "web", Port: 9090, TargetPort: intstr.FromInt(9090)},
			},
		},
	})
	_, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pods")
}

func TestResolveTarget_ServiceMissing(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	_, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service")
}

func TestResolveTarget_PodNotRunning(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 9090, TargetPort: intstr.FromInt(9090)},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
	)
	_, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ready running pods")
}

// Regression: a Running pod with PodReady != True is excluded from
// Service endpoints by kube-proxy. The forwarder must mirror that
// filter so the prom MCP isn't pointed at an unready backend.
func TestResolveTarget_PodRunningButNotReady(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 9090, TargetPort: intstr.FromInt(9090)},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			},
		},
	)
	_, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ready running pods")
}

// Regression: when one matching pod is Running-only and a later one is
// Running+Ready, the resolver must pick the Ready one.
func TestResolveTarget_PrefersReadyPodOverRunningOnly(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 9090, TargetPort: intstr.FromInt(9090)},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-1", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)
	res, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.NoError(t, err)
	assert.Equal(t, "prometheus-1", res.podName, "Ready pod must be preferred over Running-only pod")
}

func TestResolveTarget_TargetPortInt(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 9090, TargetPort: intstr.FromInt(8080)},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "prom", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}}},
			}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
	)
	res, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.NoError(t, err)
	assert.Equal(t, 8080, res.podPort)
}

func TestResolveTarget_TargetPortNamed(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 9090, TargetPort: intstr.FromString("http")},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "prom", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 9999}}},
			}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		},
	)
	res, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.NoError(t, err)
	assert.Equal(t, 9999, res.podPort)
}

func TestResolveTarget_NoMatchingServicePort(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "prometheus"},
				Ports: []corev1.ServicePort{
					{Name: "metrics", Port: 8080, TargetPort: intstr.FromInt(8080)},
				},
			},
		},
	)
	_, err := resolveTarget(context.Background(), cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no port matching")
}
