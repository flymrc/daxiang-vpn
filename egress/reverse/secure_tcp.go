package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type securePolicySnapshot struct {
	registry    *secureRegistry
	roots       *x509.CertPool
	certificate tls.Certificate
}

type reverseSecurityReport struct {
	ProtocolVersion            int       `json:"protocol_version"`
	Transport                  string    `json:"transport"`
	RegistryGeneration         uint64    `json:"registry_generation"`
	Degraded                   bool      `json:"degraded"`
	ErrorCode                  string    `json:"error_code,omitempty"`
	LastCheckedAt              time.Time `json:"last_checked_at"`
	RecheckIntervalMillis      int64     `json:"recheck_interval_ms"`
	ClosureTargetMillis        int64     `json:"closure_target_ms"`
	RejectedHandshakes         uint64    `json:"rejected_handshakes"`
	ClosedUnauthorizedSessions uint64    `json:"closed_unauthorized_sessions"`
}

type secureTCPServer struct {
	opts         serverOptions
	lock         *os.File
	mu           sync.Mutex
	snapshot     *securePolicySnapshot
	lastDigest   string
	active       map[*secureYamuxSession]struct{}
	healthReport reverseSecurityReport
	closed       bool
	done         chan struct{}
	closeOnce    sync.Once
}

type secureYamuxSession struct {
	*yamuxSession
	raw          net.Conn
	identity     secureSessionIdentity // immutable handshake identity
	certificates []*x509.Certificate
}

// Abruptly close the TCP socket before TLS close_notify; revocation must not
// wait for a peer's reads or the yamux connection write timeout.
func (s *secureYamuxSession) Close() error {
	_ = s.raw.Close()
	return s.yamuxSession.Close()
}

func newSecureTCPServer(opts serverOptions) (*secureTCPServer, error) {
	if err := validateSecureServerOptions(opts); err != nil {
		return nil, err
	}
	path, err := canonicalSecureRegistryPath(opts.EgressRegistryFile)
	if err != nil {
		return nil, err
	}
	opts.EgressRegistryFile = path
	lock, err := lockSecureRegistry(opts.EgressRegistryFile)
	if err != nil {
		return nil, fmt.Errorf("registry already owned or lock failed: %w", err)
	}
	s := &secureTCPServer{opts: opts, lock: lock, active: map[*secureYamuxSession]struct{}{}, done: make(chan struct{}), healthReport: reverseSecurityReport{
		ProtocolVersion: 2, Transport: secureTCPTransport, RecheckIntervalMillis: secureRecheckInterval.Milliseconds(), ClosureTargetMillis: 5000,
	}}
	if err := s.refresh(); err != nil {
		lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *secureTCPServer) refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("secure listener closed")
	}
	roots, err := loadSecureRoots(s.opts.TLSCAFile)
	var pair tls.Certificate
	if err == nil {
		pair, _, err = parseSecureLocalCertificate(s.opts.TLSCertFile, s.opts.TLSKeyFile, "hub", s.opts.HubID, roots)
	}
	var registry *secureRegistry
	var digest string
	if err == nil {
		registry, digest, err = loadSecureRegistry(s.opts.EgressRegistryFile, s.opts.HubID)
	}
	if err == nil && (registry.Generation < s.healthReport.RegistryGeneration || (registry.Generation == s.healthReport.RegistryGeneration && digest != s.lastDigest)) {
		err = errors.New("registry rollback against live accepted generation")
	}
	if err == nil {
		err = commitSecureWatermark(s.opts.EgressRegistryFile, registry, digest, false)
	}
	s.healthReport.LastCheckedAt = time.Now()
	s.healthReport.Degraded = err != nil
	if err != nil {
		s.snapshot = nil
		s.healthReport.ErrorCode = "reverse_authorization_reload_failed"
	} else {
		s.snapshot = &securePolicySnapshot{registry: registry, roots: roots, certificate: pair}
		s.healthReport.ErrorCode = ""
		s.healthReport.RegistryGeneration = registry.Generation
		s.lastDigest = digest
	}
	for session := range s.active {
		invalid := err != nil
		if !invalid {
			_, authErr := authorizeSecureEgress(registry, session.certificates, roots, time.Now())
			invalid = authErr != nil
		}
		if invalid {
			_ = session.Close()
			delete(s.active, session)
			s.healthReport.ClosedUnauthorizedSessions++
		}
	}
	return err
}

func (s *secureTCPServer) health() *reverseSecurityReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := s.healthReport
	return &report
}

func (s *secureTCPServer) reject() {
	s.mu.Lock()
	s.healthReport.RejectedHandshakes++
	s.mu.Unlock()
}

func (s *secureTCPServer) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for session := range s.active {
			_ = session.Close()
		}
		clear(s.active)
		close(s.done)
		s.mu.Unlock()
		_ = s.lock.Close()
	})
}

func (s *secureTCPServer) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		NextProtos:             []string{secureTCPALPN},
		SessionTicketsDisabled: true,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			if err := s.refresh(); err != nil {
				return nil, errors.New("authorization configuration unavailable")
			}
			s.mu.Lock()
			p := s.snapshot
			s.mu.Unlock()
			if p == nil {
				return nil, errors.New("authorization configuration unavailable")
			}
			return &tls.Config{
				Certificates:           []tls.Certificate{p.certificate},
				ClientCAs:              p.roots,
				ClientAuth:             tls.RequireAndVerifyClientCert,
				MinVersion:             tls.VersionTLS13,
				MaxVersion:             tls.VersionTLS13,
				NextProtos:             []string{secureTCPALPN},
				SessionTicketsDisabled: true,
				VerifyConnection: func(cs tls.ConnectionState) error {
					if cs.NegotiatedProtocol != secureTCPALPN {
						return errors.New("secure reverse ALPN required")
					}
					_, err := authorizeSecureEgress(p.registry, cs.PeerCertificates, p.roots, time.Now())
					return err
				},
			}, nil
		},
	}
}

func serveSecureTCPTunnel(opts serverOptions, manager *sessionManager, ready chan<- error) error {
	s, err := newSecureTCPServer(opts)
	if err != nil {
		ready <- err
		return err
	}
	defer s.close()
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		ready <- err
		return err
	}
	defer ln.Close()
	manager.secureTransport = s
	ready <- nil
	return s.serve(ln, manager)
}

func (s *secureTCPServer) serve(ln net.Listener, manager *sessionManager) error {
	go func() {
		ticker := time.NewTicker(secureRecheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = s.refresh()
			case <-s.done:
				return
			}
		}
	}()
	// Bound expensive unauthenticated TLS handshakes independently of sessions.
	semaphore := make(chan struct{}, 64)
	config := s.tlsConfig()
	for {
		raw, err := ln.Accept()
		if err != nil {
			return err
		}
		select {
		case semaphore <- struct{}{}:
			go func() {
				var once sync.Once
				release := func() { once.Do(func() { <-semaphore }) }
				defer release()
				s.handle(raw, config, manager, release)
			}()
		default:
			s.reject()
			_ = raw.Close()
		}
	}
}

func (s *secureTCPServer) handle(raw net.Conn, config *tls.Config, manager *sessionManager, releaseHandshake func()) {
	defer raw.Close()
	conn := tls.Server(raw, config)
	_ = raw.SetDeadline(time.Now().Add(secureHandshakeTimeout))
	ctx, cancel := context.WithTimeout(context.Background(), secureHandshakeTimeout)
	err := conn.HandshakeContext(ctx)
	cancel()
	if err == nil {
		var hello string
		hello, err = readLineBytewise(conn, 64)
		if err == nil && hello != secureTCPHello {
			err = errors.New("secure reverse hello required")
		}
	}
	if err != nil {
		s.reject()
		return
	}

	// Revalidate under the same lock as policy publication and session admission.
	// A revoke that occurred after TLS verification must not admit a late session.
	s.mu.Lock()
	p := s.snapshot
	var identity secureSessionIdentity
	if p == nil || s.closed {
		err = errors.New("authorization configuration unavailable")
	} else {
		identity, err = authorizeSecureEgress(p.registry, conn.ConnectionState().PeerCertificates, p.roots, time.Now())
	}
	if err == nil {
		count := 0
		for active := range s.active {
			if active.identity.EgressID == identity.EgressID && !active.IsClosed() {
				count++
			}
		}
		if count >= secureMaxSessionsPerEgress || len(s.active) >= 256 {
			err = errors.New("registered egress session limit")
		}
	}
	if err != nil {
		s.healthReport.RejectedHandshakes++
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if _, err = io.WriteString(conn, secureTCPAck); err != nil {
		s.reject()
		return
	}
	_ = raw.SetDeadline(time.Time{})
	session, err := yamux.Server(conn, secureYamuxConfig())
	if err != nil {
		s.reject()
		return
	}
	s.mu.Lock()
	p = s.snapshot
	if p == nil || s.closed {
		err = errors.New("authorization configuration unavailable")
	} else {
		identity, err = authorizeSecureEgress(p.registry, conn.ConnectionState().PeerCertificates, p.roots, time.Now())
	}
	count := 0
	for active := range s.active {
		if active.identity.EgressID == identity.EgressID && !active.IsClosed() {
			count++
		}
	}
	if err == nil && (count >= secureMaxSessionsPerEgress || len(s.active) >= 256) {
		err = errors.New("registered egress session limit")
	}
	if err != nil {
		s.healthReport.RejectedHandshakes++
		s.mu.Unlock()
		_ = raw.Close()
		_ = session.Close()
		return
	}
	secure := &secureYamuxSession{yamuxSession: &yamuxSession{session: session}, raw: raw, identity: identity, certificates: conn.ConnectionState().PeerCertificates}
	s.active[secure] = struct{}{}
	s.mu.Unlock()
	defer secure.Close()
	manager.set(secure)
	if releaseHandshake != nil {
		releaseHandshake()
	}
	log.Printf("reverse tls client authenticated egress=%s credential=%s protocol=2", identity.EgressID, identity.CredentialID)
	<-session.CloseChan()
	manager.clearCurrent(secure)
	s.mu.Lock()
	delete(s.active, secure)
	s.mu.Unlock()
}

func secureYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 10 * time.Second
	cfg.MaxStreamWindowSize = 4 * 1024 * 1024
	cfg.ConnectionWriteTimeout = 30 * time.Second
	return cfg
}

func secureClientTLSConfig(opts clientOptions) (*tls.Config, []*x509.Certificate, error) {
	if err := validateSecureClientOptions(opts); err != nil {
		return nil, nil, err
	}
	roots, err := loadSecureRoots(opts.TLSCAFile)
	if err != nil {
		return nil, nil, err
	}
	pair, chain, err := parseSecureLocalCertificate(opts.TLSCertFile, opts.TLSKeyFile, "egress", opts.EgressID, roots)
	if err != nil {
		return nil, nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{pair}, RootCAs: roots, ServerName: opts.TLSServerName,
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{secureTCPALPN}, SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.NegotiatedProtocol != secureTCPALPN {
				return errors.New("secure reverse ALPN required")
			}
			if len(cs.PeerCertificates) == 0 {
				return errors.New("Hub certificate missing")
			}
			_, err := secureCertificateIdentity(cs.PeerCertificates[0], "hub", opts.HubID)
			return err
		},
	}, chain, nil
}

func secureTCPClientOnce(addr string, opts clientOptions, tunnelBind *tunnelBindController) error {
	config, localChain, err := secureClientTLSConfig(opts)
	if err != nil {
		return err
	}
	iface := opts.TunnelBindInterface
	if tunnelBind != nil {
		iface = tunnelBind.interfaceForDial()
	}
	raw, err := dialTCP(addr, 15*time.Second, iface)
	if err != nil {
		if tunnelBind != nil {
			tunnelBind.recordFailure(iface, err)
		}
		return err
	}
	defer raw.Close()
	conn := tls.Client(raw, config)
	_ = raw.SetDeadline(time.Now().Add(secureHandshakeTimeout))
	ctx, cancel := context.WithTimeout(context.Background(), secureHandshakeTimeout)
	err = conn.HandshakeContext(ctx)
	cancel()
	if err == nil {
		_, err = io.WriteString(conn, secureTCPHello)
	}
	if err == nil {
		var ack string
		ack, err = readLineBytewise(conn, 64)
		if err == nil && ack != secureTCPAck {
			err = errors.New("secure reverse acceptance missing")
		}
	}
	if err != nil {
		if tunnelBind != nil {
			tunnelBind.recordFailure(iface, err)
		}
		return err
	}
	_ = raw.SetDeadline(time.Time{})
	session, err := yamux.Client(conn, secureYamuxConfig())
	if err != nil {
		return err
	}
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	opts.sessionContext = sessionCtx
	defer func() { cancelSession(); _ = session.Close() }()
	if tunnelBind != nil {
		tunnelBind.recordSuccess(iface)
	}
	log.Printf("connected to authenticated reverse TLS Hub=%s egress=%s protocol=2", opts.HubID, opts.EgressID)
	// TLS only checks validity during the handshake. Recheck both retained
	// chains against the current trust file and clock while streams are open.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(secureRecheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				roots, err := loadSecureRoots(opts.TLSCAFile)
				if err == nil {
					err = verifySecureChain(localChain, roots, x509.ExtKeyUsageClientAuth, "", time.Now())
				}
				if err == nil {
					err = verifySecureChain(conn.ConnectionState().PeerCertificates, roots, x509.ExtKeyUsageServerAuth, opts.TLSServerName, time.Now())
				}
				if err != nil {
					_ = raw.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		go handleClientStream(stream, opts)
	}
}
