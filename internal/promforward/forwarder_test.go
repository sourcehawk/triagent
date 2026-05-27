package promforward

import (
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
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	res, err := resolveTarget(cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.NoError(t, err)
	assert.Equal(t, "prometheus-0", res.podName)
	assert.Equal(t, "monitoring", res.podNamespace)
	assert.Equal(t, 9090, res.podPort)
}

func TestResolveTarget_NoPods(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "prometheus"}},
	})
	_, err := resolveTarget(cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pods")
}

func TestResolveTarget_ServiceMissing(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	_, err := resolveTarget(cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service")
}

func TestResolveTarget_PodNotRunning(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "prometheus"}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-0", Namespace: "monitoring", Labels: map[string]string{"app": "prometheus"}},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
	)
	_, err := resolveTarget(cs, Target{Service: "prometheus", Namespace: "monitoring", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no running pods")
}
