package promforward

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	dead     atomic.Bool // tests flip true to simulate a forward that died after becoming ready
}

func (s *stubForwarder) Start(_ context.Context) (string, error) {
	if s.startErr != nil {
		return "", s.startErr
	}
	s.started.Add(1)
	return s.url, nil
}
func (s *stubForwarder) Stop()         { s.stopped.Add(1) }
func (s *stubForwarder) IsAlive() bool { return !s.dead.Load() && s.stopped.Load() == 0 }

// stubKubeBuilder hands back a fixed rest.Config / clientset for any
// investigation id and context name. The Manager doesn't use these values when
// its Factory is the stub one.
func stubKubeBuilder(_, _ string) (*rest.Config, kubernetes.Interface, error) {
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
	})
	tgt := Target{Service: "p", Namespace: "n", Port: 9090}
	url1, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgt)
	require.NoError(t, err)
	url2, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgt)
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
	tgt := Target{Service: "p", Namespace: "n", Port: 9090}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			s := stubs[idx]
			idx++
			return s, nil
		},
		KubeBuilder: stubKubeBuilder,
	})
	url, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgt)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7002", url)
	url, err = mgr.Get(context.Background(), "inv-1", "cluster-b", tgt)
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
	})
	_, err := mgr.Get(context.Background(), "inv-1", "cluster-a", Target{Service: "p", Namespace: "n", Port: 9090})
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
		KubeBuilder: func(_, _ string) (*rest.Config, kubernetes.Interface, error) {
			return nil, nil, errors.New("kubeconfig parse error")
		},
	})
	_, err := mgr.Get(context.Background(), "inv-1", "any", Target{Service: "p", Namespace: "n", Port: 9090})
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
	_, err := mgr.Get(context.Background(), "inv-1", "any", Target{Service: "p", Namespace: "n", Port: 9090})
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
	_, err := mgr.Get(context.Background(), "inv-1", "", Target{Service: "p", Namespace: "n", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contextName")
}

func TestManager_GetReturnsErrorWhenFactoryReturnsNil(t *testing.T) {
	t.Parallel()
	mgr := NewManager(Options{
		Factory:     func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) { return nil, nil },
		KubeBuilder: stubKubeBuilder,
	})
	_, err := mgr.Get(context.Background(), "inv-1", "any", Target{Service: "p", Namespace: "n", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil forwarder")
}

// Regression: ForwardPorts can exit after readiness (pod restart,
// dropped SPDY). Without a liveness check, Manager.Get's cache hit
// would keep handing the prom MCP a dead loopback URL forever. After
// the first forward "dies" (stub flips IsAlive → false), the next Get
// must skip the cache and provision a fresh forwarder.
func TestManager_GetReprovisionsWhenCachedForwarderDied(t *testing.T) {
	t.Parallel()
	stubA := &stubForwarder{url: "http://127.0.0.1:7050"}
	stubB := &stubForwarder{url: "http://127.0.0.1:7051"}
	idx := 0
	stubs := []*stubForwarder{stubA, stubB}
	tgt := Target{Service: "p", Namespace: "n", Port: 9090}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			s := stubs[idx]
			idx++
			return s, nil
		},
		KubeBuilder: stubKubeBuilder,
	})
	url1, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgt)
	require.NoError(t, err)
	assert.Equal(t, stubA.url, url1)

	// Simulate the underlying ForwardPorts loop exiting (pod restart).
	stubA.dead.Store(true)

	url2, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgt)
	require.NoError(t, err)
	assert.Equal(t, stubB.url, url2, "dead cached forward must trigger re-provision")
	assert.Equal(t, int32(1), stubA.stopped.Load(), "dead forward should be torn down before re-provision")
	assert.Equal(t, int32(1), stubB.started.Load())
}

func TestManager_GetSwapsOnDifferentTarget(t *testing.T) {
	t.Parallel()
	stubA := &stubForwarder{url: "http://127.0.0.1:7010"}
	stubB := &stubForwarder{url: "http://127.0.0.1:7011"}
	idx := 0
	stubs := []*stubForwarder{stubA, stubB}
	tgtA := Target{Service: "prom-a", Namespace: "monitoring", Port: 9090}
	tgtB := Target{Service: "prom-b", Namespace: "monitoring", Port: 9090}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			s := stubs[idx]
			idx++
			return s, nil
		},
		KubeBuilder: stubKubeBuilder,
	})
	url, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgtA)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7010", url)
	url, err = mgr.Get(context.Background(), "inv-1", "cluster-a", tgtB)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7011", url)
	assert.Equal(t, int32(1), stubA.stopped.Load(), "old forwarder must be stopped when target changes")
	assert.Equal(t, int32(1), stubB.started.Load())
}

// raceStubForwarder lets a test block inside Start until signalled, and
// observe when Stop is called. It is used by the Stop-during-provisioning
// race test.
type raceStubForwarder struct {
	url          string
	startBegan   chan struct{} // closed when Start is entered
	startProceed chan struct{} // closed by the test to let Start return
	stopRan      chan struct{} // closed (once) when Stop is called
	once         sync.Once
}

func (s *raceStubForwarder) Start(_ context.Context) (string, error) {
	close(s.startBegan)
	<-s.startProceed
	return s.url, nil
}

func (s *raceStubForwarder) Stop() {
	s.once.Do(func() { close(s.stopRan) })
}

func (s *raceStubForwarder) IsAlive() bool {
	select {
	case <-s.stopRan:
		return false
	default:
		return true
	}
}

// TestManager_StopDuringProvisioningTearsDownNewForwarder reproduces the
// race where Stop runs while Get is between fwd.Start returning and the
// re-lock that stores the entry. The just-built forwarder must be torn
// down and Get must return an error instead of a leaked, live forwarder.
func TestManager_StopDuringProvisioningTearsDownNewForwarder(t *testing.T) {
	t.Parallel()
	startBegan := make(chan struct{})
	stopRan := make(chan struct{})
	stub := &raceStubForwarder{
		url:          "http://127.0.0.1:9090",
		startBegan:   startBegan,
		startProceed: make(chan struct{}),
		stopRan:      stopRan,
	}
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			return stub, nil
		},
		KubeBuilder: stubKubeBuilder,
	})

	getDone := make(chan error, 1)
	go func() {
		_, err := mgr.Get(context.Background(), "inv-1", "cluster-a", Target{})
		getDone <- err
	}()

	// Wait for Start to begin, then call Stop concurrently.
	<-startBegan
	mgr.Stop("inv-1")

	// Let Start return.
	close(stub.startProceed)

	// Get must return an error and the just-provisioned forwarder must
	// have been torn down.
	err := <-getDone
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed during provisioning")
	<-stopRan // Stop was called on the just-built forwarder.
}

// TestManager_ConcurrentGetWithSwitchPreservesNewest reproduces the race
// where a slow Get for an older context finishes after a faster Get for a
// newer context, and the slow Get would otherwise overwrite the newer
// binding with the stale entry — leaving the prom MCP routed to the old
// cluster while the user has already switched. The Manager must serialize
// provisioning per investigation id so the newer Get's binding wins.
func TestManager_ConcurrentGetWithSwitchPreservesNewest(t *testing.T) {
	t.Parallel()
	fwdA := &raceStubForwarder{
		url:          "http://127.0.0.1:8001",
		startBegan:   make(chan struct{}),
		startProceed: make(chan struct{}),
		stopRan:      make(chan struct{}),
	}
	fwdB := &stubForwarder{url: "http://127.0.0.1:8002"}

	var calls atomic.Int32
	mgr := NewManager(Options{
		Factory: func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) {
			n := calls.Add(1)
			if n == 1 {
				return fwdA, nil
			}
			return fwdB, nil
		},
		KubeBuilder: stubKubeBuilder,
	})
	tgt := Target{Service: "p", Namespace: "n", Port: 9090}

	getAResult := make(chan struct {
		url string
		err error
	}, 1)
	go func() {
		url, err := mgr.Get(context.Background(), "inv-1", "cluster-a", tgt)
		getAResult <- struct {
			url string
			err error
		}{url, err}
	}()

	// Wait until Get(A) is blocked inside Start, then race a Get(B) for
	// the new context. With serialized provisioning, Get(B) blocks behind
	// Get(A)'s in-flight slow path and only resumes once Get(A) finishes;
	// without it, Get(B) finishes first, then Get(A) overwrites with the
	// stale {A, fwdA} entry.
	<-fwdA.startBegan
	getBResult := make(chan struct {
		url string
		err error
	}, 1)
	getBStarted := make(chan struct{})
	go func() {
		close(getBStarted)
		url, err := mgr.Get(context.Background(), "inv-1", "cluster-b", tgt)
		getBResult <- struct {
			url string
			err error
		}{url, err}
	}()
	<-getBStarted

	// Release Get(A). Order of completion is implementation-defined; the
	// invariant under test is the *end state*, not the timing.
	close(fwdA.startProceed)

	waitGet := func(name string, ch <-chan struct {
		url string
		err error
	}) struct {
		url string
		err error
	} {
		select {
		case r := <-ch:
			return r
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not return within 2s", name)
			return struct {
				url string
				err error
			}{}
		}
	}
	aRes := waitGet("Get(A)", getAResult)
	bRes := waitGet("Get(B)", getBResult)
	require.NoError(t, aRes.err)
	require.NoError(t, bRes.err)
	assert.Equal(t, fwdA.url, aRes.url)
	assert.Equal(t, fwdB.url, bRes.url)

	// fwdA must have been torn down — under serialized provisioning, the
	// newer Get(B) tears it down when replacing the binding.
	select {
	case <-fwdA.stopRan:
	case <-time.After(2 * time.Second):
		t.Fatalf("stale forwarder fwdA was not torn down — newer Get(B) lost the race")
	}
	// fwdB must still be alive: it is the binding that should win.
	assert.Equal(t, int32(0), fwdB.stopped.Load(), "newer forwarder must not be torn down by a stale concurrent Get")

	// Confirm the manager's current binding is the cluster-b entry by
	// reissuing a same-context Get and asserting the cache hits.
	url, err := mgr.Get(context.Background(), "inv-1", "cluster-b", tgt)
	require.NoError(t, err)
	assert.Equal(t, fwdB.url, url)
	assert.Equal(t, int32(1), fwdB.started.Load(), "cluster-b Get must hit the cache, not re-provision")
}

// TestManager_GetAfterStopErrors verifies that a Get call that arrives
// after Stop already returned also gets a closed-error rather than
// provisioning a new forwarder.
func TestManager_GetAfterStopErrors(t *testing.T) {
	t.Parallel()
	stub := &stubForwarder{url: "http://127.0.0.1:7020"}
	mgr := NewManager(Options{
		Factory:     func(*rest.Config, kubernetes.Interface, Target) (PortForwarder, error) { return stub, nil },
		KubeBuilder: stubKubeBuilder,
	})
	_, err := mgr.Get(context.Background(), "inv-1", "cluster-a", Target{Service: "p", Namespace: "n", Port: 9090})
	require.NoError(t, err)
	mgr.Stop("inv-1")
	_, err = mgr.Get(context.Background(), "inv-1", "cluster-a", Target{Service: "p", Namespace: "n", Port: 9090})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has been closed")
}
