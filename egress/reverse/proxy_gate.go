package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"

	"zongheng-vpn/shared/proxygate"
)

// The runtime owns a dedicated, policy-bound listener. Merely supplying a
// controller socket never changes the scope or enables an unconfigured gate.
type proxyGateRuntime struct {
	gate      *proxygate.Gate
	listener  net.Listener
	control   *proxygate.ControlServer
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func prepareProxyGate(opts serverOptions, manager *sessionManager) (*proxyGateRuntime, error) {
	if opts.ProxyGatePolicyFile == "" && opts.ProxyGateControlSocket == "" {
		return nil, nil
	}
	if opts.ProxyGatePolicyFile == "" || opts.ProxyGateControlSocket == "" {
		return nil, errors.New("proxy_gate_requires_policy_and_control_socket")
	}
	// Only the TCP transports provide the full connection-close contract used
	// by quarantine. QUIC's stream Close closes its send direction alone.
	if opts.Transport != "tcp" && opts.Transport != secureTCPTransport {
		return nil, errors.New("proxy_gate_unsupported_transport")
	}
	policy, err := proxygate.LoadPolicy(opts.ProxyGatePolicyFile)
	if err != nil {
		return nil, err
	}
	// The actual bound endpoint, policy endpoint and startup configuration must
	// agree. Wildcard addresses, DNS aliases and ephemeral listeners are not a
	// dedicated v2 namespace.
	if opts.Proxy != policy.Listener {
		return nil, proxygate.ErrPolicy
	}
	gate, err := proxygate.New(*policy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &proxyGateRuntime{gate: gate, cancel: cancel}
	listener, err := net.Listen("tcp", opts.Proxy)
	if err != nil {
		runtime.Close()
		return nil, errors.New("proxy_gate_listener_unavailable")
	}
	runtime.listener = listener
	if err := gate.ValidateListener(listener.Addr()); err != nil {
		runtime.Close()
		return nil, err
	}
	control, err := proxygate.ListenControl(ctx, opts.ProxyGateControlSocket, gate)
	if err != nil {
		runtime.Close()
		return nil, err
	}
	runtime.control = control
	manager.proxyGate = gate
	return runtime, nil
}

func (r *proxyGateRuntime) Serve(server *http.Server) error {
	return server.Serve(r.listener)
}

func (r *proxyGateRuntime) Close() {
	r.closeOnce.Do(func() {
		r.gate.Close()
		r.cancel()
		if r.control != nil {
			r.control.Close()
		}
		if r.listener != nil {
			_ = r.listener.Close()
		}
	})
}

type proxyGateConnKey struct{}

// Preserve the actual accepted socket before net/http owns or hijacks it.
// Managed fetch streams need the same client-side cancellation as CONNECT.
func proxyGateConnContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, proxyGateConnKey{}, conn)
}

type proxyRequestAdmission struct {
	admission *proxygate.Admission
	ctx       context.Context
	cancel    context.CancelFunc
	stop      func() bool
	client    net.Conn
}

func (m *sessionManager) acquireProxyAdmission(req *http.Request) (*proxyRequestAdmission, error) {
	if m.proxyGate == nil {
		return nil, nil
	}
	admission, err := m.proxyGate.AcquireSource(req.RemoteAddr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(admission.Context())
	lease := &proxyRequestAdmission{admission: admission, ctx: ctx, cancel: cancel}
	lease.stop = context.AfterFunc(req.Context(), cancel)
	if conn, ok := req.Context().Value(proxyGateConnKey{}).(net.Conn); ok {
		if err := lease.track(conn); err != nil {
			lease.release()
			return nil, err
		}
		lease.client = conn
	}
	return lease, nil
}

func (m *sessionManager) managedProxyDiagnosticSource(remote string) bool {
	if m.proxyGate == nil {
		return false
	}
	addr, err := netip.ParseAddr(proxyPeer(remote))
	if err != nil {
		// A diagnostic stream must not bypass the gate on an ambiguous source.
		return true
	}
	addr = addr.Unmap()
	for _, source := range m.proxyGate.Policy().ManagedSources {
		if netip.MustParsePrefix(source).Contains(addr) {
			return true
		}
	}
	return false
}

func (a *proxyRequestAdmission) context(req *http.Request) context.Context {
	if a == nil {
		return req.Context()
	}
	return a.ctx
}

func (a *proxyRequestAdmission) track(conn net.Conn) error {
	if a == nil {
		return nil
	}
	return a.admission.Track(conn)
}

func (a *proxyRequestAdmission) reserve() (*proxygate.Registration, error) {
	if a == nil {
		return nil, nil
	}
	return a.admission.Reserve()
}

func abortProxyRegistration(registration *proxygate.Registration) {
	if registration != nil {
		registration.Abort()
	}
}

func attachProxyRegistration(registration *proxygate.Registration, conn net.Conn) (net.Conn, error) {
	if registration == nil {
		return conn, nil
	}
	return registration.AttachFenced(conn)
}

func (a *proxyRequestAdmission) trackClient(conn net.Conn) error {
	if a == nil || a.client != nil {
		return nil
	}
	if err := a.track(conn); err != nil {
		return err
	}
	a.client = conn
	return nil
}

func (a *proxyRequestAdmission) release() {
	if a == nil {
		return
	}
	a.stop()
	a.cancel()
	a.admission.Release()
}

func (m *sessionManager) openProxyCommand(req *http.Request, admission *proxyRequestAdmission, command string) (net.Conn, *bufio.Reader, string, error) {
	return m.openCommandContext(admission.context(req), command, admission)
}
