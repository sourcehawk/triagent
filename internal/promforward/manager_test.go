package promforward

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type stubForwarder struct {
	url      string
	startErr error
	started  atomic.Int32
	stopped  atomic.Int32
}

func (s *stubForwarder) Start(_ context.Context) (string, error) {
	if s.startErr != nil {
		return "", s.startErr
	}
	s.started.Add(1)
	return s.url, nil
}
func (s *stubForwarder) Stop() { s.stopped.Add(1) }

// stubKubeBuilder hands back a fixed rest.Config / clientset for any
// context name. The Manager doesn't use these values when its Factory
// is the stub one.
func stubKubeBuilder(_ string) (*rest.Config, kubernetes.Interface, error) {
	return &rest.Config{}, nil, nil
}

func TestManager_GetProvisionsOnce(t *testing.T) {
	t.Parallel()
	stub := &stubForwarder{url: "http://127.0.0.1:7001"}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			return stub, nil
		},
		KubeBuilder: stubKubeBuilder,
		Target:      Target{Service: "p", Namespace: "n", Port: 9090},
	})
	url1, err := mgr.Get(context.Background(), "inv-1", "cluster-a")
	require.NoError(t, err)
	url2, err := mgr.Get(context.Background(), "inv-1", "cluster-a")
	require.NoError(t, err)
	assert.Equal(t, url1, url2)
	assert.Equal(t, "http://127.0.0.1:7001", url1)
	assert.Equal(t, int32(1), stub.started.Load(), "second Get with same context must hit cache")
}

func TestManager_GetSwapsOnDifferentContext(t *testing.T) {
	t.Parallel()
	stubA := &stubForwarder{url: "http://127.0.0.1:7002"}
	stubB := &stubForwarder{url: "http://127.0.0.1:7003"}
	idx := 0
	stubs := []*stubForwarder{stubA, stubB}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			s := stubs[idx]
			idx++
			return s, nil
		},
		KubeBuilder: stubKubeBuilder,
		Target:      Target{},
	})
	url, err := mgr.Get(context.Background(), "inv-1", "cluster-a")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7002", url)
	url, err = mgr.Get(context.Background(), "inv-1", "cluster-b")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7003", url)
	assert.Equal(t, int32(1), stubA.stopped.Load(), "cluster-a forward must be stopped after swap to cluster-b")
	assert.Equal(t, int32(1), stubB.started.Load())
}

func TestManager_Stop(t *testing.T) {
	t.Parallel()
	stub := &stubForwarder{url: "http://127.0.0.1:7004"}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			return stub, nil
		},
		KubeBuilder: stubKubeBuilder,
		Target:      Target{},
	})
	_, err := mgr.Get(context.Background(), "inv-1", "cluster-a")
	require.NoError(t, err)
	mgr.Stop("inv-1")
	assert.Equal(t, int32(1), stub.stopped.Load())
}

func TestManager_StopUnknownIsNoop(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{
		Factory:     func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) { return nil, nil },
		KubeBuilder: stubKubeBuilder,
	})
	mgr.Stop("never-started") // must not panic
}

func TestManager_GetReturnsKubeBuilderError(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			return &stubForwarder{}, nil
		},
		KubeBuilder: func(string) (*rest.Config, kubernetes.Interface, error) {
			return nil, nil, errors.New("kubeconfig parse error")
		},
	})
	_, err := mgr.Get(context.Background(), "inv-1", "any")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeconfig")
}

func TestManager_GetReturnsForwarderStartError(t *testing.T) {
	t.Parallel()
	stub := &stubForwarder{startErr: errors.New("no pods")}
	mgr := NewManager(Options{
		Factory:     func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) { return stub, nil },
		KubeBuilder: stubKubeBuilder,
	})
	_, err := mgr.Get(context.Background(), "inv-1", "any")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pods")
	assert.Equal(t, int32(1), stub.stopped.Load(), "fwd.Stop must be called when Start fails")
}

func TestManager_GetRequiresContextName(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{
		Factory:     func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) { return &stubForwarder{}, nil },
		KubeBuilder: stubKubeBuilder,
	})
	_, err := mgr.Get(context.Background(), "inv-1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contextName")
}

func TestManager_GetReturnsErrorWhenFactoryReturnsNil(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{
		Factory:     func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) { return nil, nil },
		KubeBuilder: stubKubeBuilder,
	})
	_, err := mgr.Get(context.Background(), "inv-1", "any")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil forwarder")
}
