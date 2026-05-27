package k8sx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestNamespaceExists(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		objects []runtime.Object
		ns      string
		want    bool
		wantErr bool
	}{
		{
			name:    "namespace present",
			objects: []runtime.Object{newNamespace("abc-zeebe")},
			ns:      "abc-zeebe",
			want:    true,
		},
		{
			name:    "namespace missing",
			objects: []runtime.Object{newNamespace("other-zeebe")},
			ns:      "abc-zeebe",
			want:    false,
		},
		{
			name:    "no namespaces at all",
			objects: nil,
			ns:      "abc-zeebe",
			want:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cs := fake.NewSimpleClientset(tc.objects...)
			c := &Client{Clientset: cs, Namespace: tc.ns}

			got, err := c.NamespaceExists(context.Background())
			assert.Equal(t, tc.wantErr, err != nil, "unexpected error: %v", err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNamespaceExists_PropagatesNonNotFoundErrors(t *testing.T) {
	t.Parallel()

	cs := fake.NewSimpleClientset()
	cs.PrependReactor("get", "namespaces", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})

	c := &Client{Clientset: cs, Namespace: "abc-zeebe"}

	_, err := c.NamespaceExists(context.Background())
	require.Error(t, err)
	require.False(t, apierrors.IsNotFound(err), "expected non-NotFound error to propagate, got NotFound")
}

func TestCanListPods(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		allow       bool
		reason      string
		wantAllowed bool
		wantReason  string
	}{
		{"allowed", true, "", true, ""},
		{"denied with reason", false, "no access to namespace", false, "no access to namespace"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cs := fake.NewSimpleClientset()
			cs.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
				return true, &authv1.SelfSubjectAccessReview{
					Status: authv1.SubjectAccessReviewStatus{
						Allowed: tc.allow,
						Reason:  tc.reason,
					},
				}, nil
			})

			c := &Client{Clientset: cs, Namespace: "abc-zeebe"}
			allowed, reason, err := c.CanListPods(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.wantAllowed, allowed)
			assert.Equal(t, tc.wantReason, reason)
		})
	}
}

func newNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}
