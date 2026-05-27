package promforward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// newClientGoForwarder is the production Factory. Wraps
// k8s.io/client-go/tools/portforward.PortForwarder behind the
// promforward.PortForwarder interface.
func newClientGoForwarder(cfg *rest.Config, cs kubernetes.Interface, target Target) (PortForwarder, error) {
	return &clientGoForwarder{cfg: cfg, cs: cs, target: target}, nil
}

type clientGoForwarder struct {
	cfg    *rest.Config
	cs     kubernetes.Interface
	target Target

	mu        sync.Mutex
	stopCh    chan struct{}
	localPort int
}

func (f *clientGoForwarder) Start(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopCh != nil {
		return "http://127.0.0.1:" + strconv.Itoa(f.localPort), nil
	}
	res, err := resolveTarget(ctx, f.cs, f.target)
	if err != nil {
		return "", err
	}
	local, err := freeLoopbackPort()
	if err != nil {
		return "", fmt.Errorf("pick local port: %w", err)
	}

	host := f.cfg.Host
	if host == "" {
		return "", fmt.Errorf("rest.Config has empty Host")
	}
	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse host: %w", err)
	}
	u.Path = "/api/v1/namespaces/" + res.podNamespace + "/pods/" + res.podName + "/portforward"

	transport, upgrader, err := spdy.RoundTripperFor(f.cfg)
	if err != nil {
		return "", fmt.Errorf("spdy roundtripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, u)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	ports := []string{strconv.Itoa(local) + ":" + strconv.Itoa(res.podPort)}

	pf, err := portforward.New(dialer, ports, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return "", fmt.Errorf("build port-forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- pf.ForwardPorts()
	}()

	select {
	case <-readyCh:
		f.stopCh = stopCh
		f.localPort = local
		return "http://127.0.0.1:" + strconv.Itoa(local), nil
	case err := <-errCh:
		return "", fmt.Errorf("port-forward did not become ready: %w", err)
	case <-ctx.Done():
		close(stopCh)
		return "", ctx.Err()
	}
}

func (f *clientGoForwarder) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopCh == nil {
		return
	}
	close(f.stopCh)
	f.stopCh = nil
	f.localPort = 0
}
