package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/quic-go/quic-go"
	"gopkg.in/yaml.v3"
)

const protocolHello = "ZHREV1"
const quicALPN = "zhreverse/1"

var reverseCommandTimeout = 20 * time.Second

const (
	defaultTunnelBenchBytes = 20_000_000
	maxTunnelBenchBytes     = 100_000_000
	maxTunnelBenchStreams   = 8
	tunnelBenchTimeout      = 2 * time.Minute
)

const (
	stripedConnectHeader      = "X-ZH-Striped-Streams"
	maxStripedConnectStreams  = 2
	stripedFrameHeaderSize    = 12
	stripedChunkSize          = 32 * 1024
	maxStripedPendingBytes    = 8 * 1024 * 1024
	stripedLaneAttachTimeout  = 10 * time.Second
	stripedTargetReadyTimeout = 10 * time.Second
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: zhreverse server|client [flags]")
	}
	switch args[0] {
	case "server":
		return runServer(args[1:])
	case "client":
		return runClient(args[1:])
	default:
		return fmt.Errorf("unknown mode: %s", args[0])
	}
}

type reverseConfig struct {
	Server serverOptions `json:"server" yaml:"server"`
	Client clientOptions `json:"client" yaml:"client"`
}

type serverOptions struct {
	Listen            string        `json:"listen" yaml:"listen"`
	Proxy             string        `json:"proxy" yaml:"proxy"`
	Token             string        `json:"token,omitempty" yaml:"token,omitempty"`
	TokenFile         string        `json:"token_file,omitempty" yaml:"token_file,omitempty"`
	Transport         string        `json:"transport" yaml:"transport"`
	Resolve           string        `json:"resolve" yaml:"resolve"`
	TLSCertFile       string        `json:"tls_cert_file,omitempty" yaml:"tls_cert_file,omitempty"`
	TLSKeyFile        string        `json:"tls_key_file,omitempty" yaml:"tls_key_file,omitempty"`
	EnableFetch       bool          `json:"enable_fetch,omitempty" yaml:"enable_fetch,omitempty"`
	AllowedProxyCIDRs []string      `json:"allowed_proxy_cidrs,omitempty" yaml:"allowed_proxy_cidrs,omitempty"`
	MaxProxyConns     int           `json:"max_proxy_connections,omitempty" yaml:"max_proxy_connections,omitempty"`
	MaxProxyConnsPeer int           `json:"max_proxy_connections_per_client,omitempty" yaml:"max_proxy_connections_per_client,omitempty"`
	ProxyIdleTimeout  time.Duration `json:"proxy_idle_timeout,omitempty" yaml:"proxy_idle_timeout,omitempty"`
	V4OnlyDirect      bool          `json:"v4_only_direct,omitempty" yaml:"v4_only_direct,omitempty"`
}

type clientOptions struct {
	Server             string        `json:"server" yaml:"server"`
	Token              string        `json:"token,omitempty" yaml:"token,omitempty"`
	TokenFile          string        `json:"token_file,omitempty" yaml:"token_file,omitempty"`
	Reconnect          time.Duration `json:"reconnect" yaml:"reconnect"`
	Transport          string        `json:"transport" yaml:"transport"`
	Connections        int           `json:"connections" yaml:"connections"`
	AddressFamily      string        `json:"address_family,omitempty" yaml:"address_family,omitempty"`
	ServerCertSHA256   string        `json:"server_cert_sha256,omitempty" yaml:"server_cert_sha256,omitempty"`
	InsecureSkipVerify bool          `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
}

func defaultServerOptions() serverOptions {
	return serverOptions{
		Listen:           ":39093",
		Proxy:            "127.0.0.1:18081",
		Transport:        "quic",
		Resolve:          "server",
		ProxyIdleTimeout: 2 * time.Minute,
	}
}

func defaultClientOptions() clientOptions {
	return clientOptions{
		Reconnect:     3 * time.Second,
		Transport:     "quic",
		Connections:   4,
		AddressFamily: "auto",
	}
}

func (o serverOptions) withDefaults(defaults serverOptions) serverOptions {
	if o.Listen == "" {
		o.Listen = defaults.Listen
	}
	if o.Proxy == "" {
		o.Proxy = defaults.Proxy
	}
	if o.Transport == "" {
		o.Transport = defaults.Transport
	}
	if o.Resolve == "" {
		o.Resolve = defaults.Resolve
	}
	if o.ProxyIdleTimeout == 0 {
		o.ProxyIdleTimeout = defaults.ProxyIdleTimeout
	}
	return o
}

func (o clientOptions) withDefaults(defaults clientOptions) clientOptions {
	if o.Server == "" {
		o.Server = defaults.Server
	}
	if o.Reconnect == 0 {
		o.Reconnect = defaults.Reconnect
	}
	if o.Transport == "" {
		o.Transport = defaults.Transport
	}
	if o.Connections == 0 {
		o.Connections = defaults.Connections
	}
	if o.AddressFamily == "" {
		o.AddressFamily = defaults.AddressFamily
	}
	return o
}

func loadReverseConfig(path string) (reverseConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return reverseConfig{}, err
	}
	var cfg reverseConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return reverseConfig{}, err
	}
	return cfg, nil
}

func visitedFlags(fs *flag.FlagSet) map[string]bool {
	out := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		out[f.Name] = true
	})
	return out
}

func resolveToken(token string, tokenFile string) (string, error) {
	if strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token), nil
	}
	if strings.TrimSpace(tokenFile) == "" {
		return "", nil
	}
	data, err := os.ReadFile(strings.TrimSpace(tokenFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	defaults := defaultServerOptions()
	configPath := fs.String("config", "", "YAML config path")
	listenAddr := fs.String("listen", defaults.Listen, "reverse tunnel listen address")
	proxyAddr := fs.String("proxy", defaults.Proxy, "local HTTP CONNECT proxy address")
	token := fs.String("token", defaults.Token, "shared auth token")
	tokenFile := fs.String("token-file", defaults.TokenFile, "file containing shared auth token")
	transport := fs.String("transport", defaults.Transport, "reverse transport: tcp or quic")
	resolve := fs.String("resolve", defaults.Resolve, "target DNS side: server or client")
	tlsCertFile := fs.String("tls-cert-file", defaults.TLSCertFile, "TLS certificate for QUIC")
	tlsKeyFile := fs.String("tls-key-file", defaults.TLSKeyFile, "TLS key for QUIC")
	enableFetch := fs.Bool("enable-fetch", defaults.EnableFetch, "enable diagnostic /fetch endpoint")
	maxProxyConns := fs.Int("max-proxy-connections", defaults.MaxProxyConns, "maximum concurrent CONNECT proxy sessions; 0 disables the limit")
	maxProxyConnsPeer := fs.Int("max-proxy-connections-per-client", defaults.MaxProxyConnsPeer, "maximum concurrent CONNECT proxy sessions per client IP; 0 disables the limit")
	proxyIdleTimeout := fs.Duration("proxy-idle-timeout", defaults.ProxyIdleTimeout, "idle timeout for CONNECT proxy sessions; 0 disables idle reaping")
	v4OnlyDirect := fs.Bool("v4-only-direct", defaults.V4OnlyDirect, "deprecated and ignored; Hub never acts as an egress fallback")
	if err := fs.Parse(args); err != nil {
		return err
	}
	explicit := visitedFlags(fs)
	opts := defaults
	if *configPath != "" {
		cfg, err := loadReverseConfig(*configPath)
		if err != nil {
			return err
		}
		opts = cfg.Server.withDefaults(defaults)
	}
	if explicit["listen"] {
		opts.Listen = *listenAddr
	}
	if explicit["proxy"] {
		opts.Proxy = *proxyAddr
	}
	if explicit["token"] {
		opts.Token = *token
	}
	if explicit["token-file"] {
		opts.TokenFile = *tokenFile
	}
	if explicit["transport"] {
		opts.Transport = *transport
	}
	if explicit["resolve"] {
		opts.Resolve = *resolve
	}
	if explicit["tls-cert-file"] {
		opts.TLSCertFile = *tlsCertFile
	}
	if explicit["tls-key-file"] {
		opts.TLSKeyFile = *tlsKeyFile
	}
	if explicit["enable-fetch"] {
		opts.EnableFetch = *enableFetch
	}
	if explicit["max-proxy-connections"] {
		opts.MaxProxyConns = *maxProxyConns
	}
	if explicit["max-proxy-connections-per-client"] {
		opts.MaxProxyConnsPeer = *maxProxyConnsPeer
	}
	if explicit["proxy-idle-timeout"] {
		opts.ProxyIdleTimeout = *proxyIdleTimeout
	}
	if explicit["v4-only-direct"] {
		opts.V4OnlyDirect = *v4OnlyDirect
	}
	resolvedToken, err := resolveToken(opts.Token, opts.TokenFile)
	if err != nil {
		return err
	}
	if resolvedToken == "" {
		return errors.New("--token is required")
	}
	if opts.Resolve != "server" && opts.Resolve != "client" {
		return errors.New("--resolve must be server or client")
	}
	if opts.MaxProxyConns < 0 {
		return errors.New("--max-proxy-connections must be >= 0")
	}
	if opts.MaxProxyConnsPeer < 0 {
		return errors.New("--max-proxy-connections-per-client must be >= 0")
	}
	if opts.ProxyIdleTimeout < 0 {
		return errors.New("--proxy-idle-timeout must be >= 0")
	}

	allowedProxyNets, err := parseCIDRs(opts.AllowedProxyCIDRs)
	if err != nil {
		return err
	}
	manager := &sessionManager{
		resolve:           opts.Resolve,
		fetch:             opts.EnableFetch,
		allowedProxyNets:  allowedProxyNets,
		maxProxyConns:     opts.MaxProxyConns,
		maxProxyConnsPeer: opts.MaxProxyConnsPeer,
		proxyIdleTimeout:  opts.ProxyIdleTimeout,
		activeProxyByPeer: map[string]int{},
	}
	tunnelReady := make(chan error, 1)
	go func() {
		if err := serveTunnel(opts, resolvedToken, manager, tunnelReady); err != nil {
			log.Printf("tunnel listener stopped: %v", err)
		}
	}()
	if err := <-tunnelReady; err != nil {
		return err
	}

	server := &http.Server{
		Addr:              opts.Proxy,
		Handler:           http.HandlerFunc(manager.handleProxy),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if opts.V4OnlyDirect {
		log.Printf("v4_only_direct is deprecated and ignored: Hub must not act as an egress fallback")
	}
	log.Printf("reverse server listening transport=%s resolve=%s tunnel=%s proxy=%s max_proxy_connections=%d max_proxy_connections_per_client=%d proxy_idle_timeout=%s", opts.Transport, opts.Resolve, opts.Listen, opts.Proxy, opts.MaxProxyConns, opts.MaxProxyConnsPeer, opts.ProxyIdleTimeout)
	return server.ListenAndServe()
}

func serveTunnel(opts serverOptions, token string, manager *sessionManager, ready chan<- error) error {
	switch opts.Transport {
	case "tcp":
		return serveTCPTunnel(opts.Listen, token, manager, ready)
	case "quic":
		return serveQUICTunnel(opts, token, manager, ready)
	default:
		err := fmt.Errorf("unknown transport: %s", opts.Transport)
		ready <- err
		return err
	}
}

func serveTCPTunnel(addr string, token string, manager *sessionManager, ready chan<- error) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ready <- err
		return err
	}
	ready <- nil
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleTCPTunnelConn(conn, token, manager)
	}
}

func handleTCPTunnelConn(conn net.Conn, token string, manager *sessionManager) {
	if err := readHello(conn, token); err != nil {
		log.Printf("reject tunnel from %s: %v", conn.RemoteAddr(), err)
		_ = conn.Close()
		return
	}
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 10 * time.Second
	// 蜂窝链路负载时 RTT 可达 0.5-1.5s:默认 256KB 流窗口会把单流吞吐压到
	// 1-4 Mbps,默认 10s 写超时会在缓冲膨胀时误杀整个会话。
	cfg.MaxStreamWindowSize = 4 * 1024 * 1024
	cfg.ConnectionWriteTimeout = 30 * time.Second
	session, err := yamux.Server(conn, cfg)
	if err != nil {
		log.Printf("yamux server error: %v", err)
		_ = conn.Close()
		return
	}
	manager.set(&yamuxSession{session: session})
	log.Printf("reverse tcp client connected from %s", conn.RemoteAddr())
	<-session.CloseChan()
	manager.clear(session)
	log.Printf("reverse tcp client disconnected from %s", conn.RemoteAddr())
}

func serveQUICTunnel(opts serverOptions, token string, manager *sessionManager, ready chan<- error) error {
	tlsConfig, err := serverTLSConfig(opts.TLSCertFile, opts.TLSKeyFile)
	if err != nil {
		ready <- err
		return err
	}
	listener, err := quic.ListenAddr(opts.Listen, tlsConfig, quicConfig())
	if err != nil {
		ready <- err
		return err
	}
	ready <- nil
	defer listener.Close()
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			return err
		}
		go handleQUICConn(conn, token, manager)
	}
}

func handleQUICConn(conn *quic.Conn, token string, manager *sessionManager) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	authStream, err := conn.AcceptStream(ctx)
	cancel()
	if err != nil {
		log.Printf("reject quic tunnel from %s: %v", conn.RemoteAddr(), err)
		_ = conn.CloseWithError(1, "auth stream missing")
		return
	}
	authConn := &quicStreamConn{stream: authStream, local: conn.LocalAddr(), remote: conn.RemoteAddr()}
	if err := readHello(authConn, token); err != nil {
		log.Printf("reject quic tunnel from %s: %v", conn.RemoteAddr(), err)
		_ = authStream.Close()
		_ = conn.CloseWithError(1, "auth failed")
		return
	}
	_ = authStream.Close()

	manager.set(&quicSession{conn: conn})
	log.Printf("reverse quic client connected from %s", conn.RemoteAddr())
	<-conn.Context().Done()
	manager.clear(conn)
	log.Printf("reverse quic client disconnected from %s", conn.RemoteAddr())
}

type tunnelSession interface {
	OpenStream(context.Context) (net.Conn, error)
	Close() error
	IsClosed() bool
	RemoteAddr() net.Addr
}

type yamuxSession struct {
	session *yamux.Session
}

func (s *yamuxSession) OpenStream(context.Context) (net.Conn, error) {
	return s.session.Open()
}

func (s *yamuxSession) Close() error {
	return s.session.Close()
}

func (s *yamuxSession) IsClosed() bool {
	return s.session.IsClosed()
}

func (s *yamuxSession) RemoteAddr() net.Addr {
	return s.session.RemoteAddr()
}

type quicSession struct {
	conn *quic.Conn
}

func (s *quicSession) OpenStream(ctx context.Context) (net.Conn, error) {
	stream, err := s.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return &quicStreamConn{stream: stream, local: s.conn.LocalAddr(), remote: s.conn.RemoteAddr()}, nil
}

func (s *quicSession) Close() error {
	return s.conn.CloseWithError(0, "replaced")
}

func (s *quicSession) IsClosed() bool {
	return s.conn.Context().Err() != nil
}

func (s *quicSession) RemoteAddr() net.Addr {
	return s.conn.RemoteAddr()
}

type sessionManager struct {
	mu                sync.RWMutex
	sessions          []tunnelSession
	sessionStats      map[tunnelSession]*sessionHealth
	next              int
	resolve           string
	fetch             bool
	allowedProxyNets  []*net.IPNet
	maxProxyConns     int
	maxProxyConnsPeer int
	proxyIdleTimeout  time.Duration
	activeProxyMu     sync.Mutex
	activeProxyConns  int
	activeProxyByPeer map[string]int
}

type sessionHealth struct {
	activeStreams       int
	consecutiveFailures int
	ewmaCommandRTT      time.Duration
	lastFailure         time.Time
}

type sessionHealthReport struct {
	GeneratedAt                  time.Time            `json:"generated_at"`
	SessionCount                 int                  `json:"session_count"`
	Sessions                     []sessionHealthEntry `json:"sessions"`
	ActiveProxyConnections       int                  `json:"active_proxy_connections"`
	ActiveProxyConnectionsByPeer map[string]int       `json:"active_proxy_connections_by_peer,omitempty"`
}

type sessionHealthEntry struct {
	Index                int    `json:"index"`
	RemoteAddr           string `json:"remote_addr"`
	ActiveStreams        int    `json:"active_streams"`
	ConsecutiveFailures  int    `json:"consecutive_failures"`
	EWMACommandRTTMillis int64  `json:"ewma_command_rtt_ms"`
	LastFailureAgoMillis int64  `json:"last_failure_ago_ms,omitempty"`
	SchedulerScoreMillis int64  `json:"scheduler_score_ms"`
}

type tunnelBenchReport struct {
	GeneratedAt    time.Time                `json:"generated_at"`
	RequestedBytes int64                    `json:"requested_bytes"`
	Streams        int                      `json:"streams"`
	BytesRead      int64                    `json:"bytes_read"`
	DurationMillis int64                    `json:"duration_ms"`
	BytesPerSecond float64                  `json:"bytes_per_second"`
	Mbps           float64                  `json:"mbps"`
	OK             bool                     `json:"ok"`
	Error          string                   `json:"error,omitempty"`
	PerStream      []tunnelBenchStreamEntry `json:"per_stream"`
}

type tunnelBenchStreamEntry struct {
	Index            int     `json:"index"`
	RequestedBytes   int64   `json:"requested_bytes"`
	BytesRead        int64   `json:"bytes_read"`
	DurationMillis   int64   `json:"duration_ms"`
	BytesPerSecond   float64 `json:"bytes_per_second"`
	Mbps             float64 `json:"mbps"`
	CommandRTTMillis int64   `json:"command_rtt_ms"`
	Error            string  `json:"error,omitempty"`
}

func (m *sessionManager) set(session tunnelSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = append(m.sessions, session)
	if m.sessionStats == nil {
		m.sessionStats = map[tunnelSession]*sessionHealth{}
	}
	if _, ok := m.sessionStats[session]; !ok {
		m.sessionStats[session] = &sessionHealth{}
	}
}

func (m *sessionManager) clear(session any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.sessions[:0]
	for _, current := range m.sessions {
		remove := false
		switch typed := current.(type) {
		case *yamuxSession:
			remove = typed.session == session
		case *quicSession:
			remove = typed.conn == session
		}
		if !remove {
			filtered = append(filtered, current)
		} else if m.sessionStats != nil {
			delete(m.sessionStats, current)
		}
	}
	m.sessions = filtered
	if len(m.sessions) == 0 {
		m.next = 0
	} else if m.next >= len(m.sessions) {
		m.next %= len(m.sessions)
	}
}

func (m *sessionManager) clearCurrent(session tunnelSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.sessions[:0]
	for _, current := range m.sessions {
		if current != session {
			filtered = append(filtered, current)
		} else if m.sessionStats != nil {
			delete(m.sessionStats, current)
		}
	}
	m.sessions = filtered
	if len(m.sessions) == 0 {
		m.next = 0
	} else if m.next >= len(m.sessions) {
		m.next %= len(m.sessions)
	}
}

func (m *sessionManager) openStream() (net.Conn, error) {
	attempts := m.sessionCount()
	for attempt := 0; attempt < attempts; attempt++ {
		session, release := m.reserveSession()
		if session == nil {
			return nil, errors.New("reverse client is not connected")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := session.OpenStream(ctx)
		cancel()
		if err == nil {
			return &trackedConn{Conn: stream, release: release}, nil
		}
		release()
		m.recordSessionFailure(session)
		log.Printf("open stream via %s failed: %v", session.RemoteAddr(), err)
		m.clearCurrent(session)
	}
	return nil, errors.New("no usable reverse client session")
}

func (m *sessionManager) openCommand(command string) (net.Conn, *bufio.Reader, string, error) {
	attempts := m.sessionCount()
	for attempt := 0; attempt < attempts; attempt++ {
		session, release := m.reserveSession()
		if session == nil {
			return nil, nil, "", errors.New("reverse client is not connected")
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := session.OpenStream(ctx)
		cancel()
		if err != nil {
			release()
			m.recordSessionFailure(session)
			log.Printf("open stream via %s failed: %v", session.RemoteAddr(), err)
			m.clearCurrent(session)
			continue
		}
		tracked := &trackedConn{Conn: stream, release: release}
		if err := stream.SetDeadline(time.Now().Add(reverseCommandTimeout)); err != nil {
			_ = tracked.Close()
			m.recordSessionFailure(session)
			log.Printf("set reverse command deadline via %s failed: %v", session.RemoteAddr(), err)
			m.clearCurrent(session)
			continue
		}
		if _, err := fmt.Fprintf(stream, "%s\n", command); err != nil {
			_ = tracked.Close()
			m.recordSessionFailure(session)
			log.Printf("write reverse command via %s failed: %v", session.RemoteAddr(), err)
			m.clearCurrent(session)
			continue
		}
		reader := bufio.NewReader(stream)
		status, err := reader.ReadString('\n')
		if err != nil {
			_ = tracked.Close()
			m.recordSessionFailure(session)
			log.Printf("read reverse command response via %s failed: %v", session.RemoteAddr(), err)
			m.clearCurrent(session)
			continue
		}
		if err := stream.SetDeadline(time.Time{}); err != nil {
			_ = tracked.Close()
			m.recordSessionFailure(session)
			log.Printf("clear reverse command deadline via %s failed: %v", session.RemoteAddr(), err)
			m.clearCurrent(session)
			continue
		}
		m.recordSessionSuccess(session, time.Since(started))
		return tracked, reader, strings.TrimSpace(status), nil
	}
	return nil, nil, "", errors.New("no usable reverse client session")
}

func (m *sessionManager) sessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	filtered := m.sessions[:0]
	for _, session := range m.sessions {
		if session.IsClosed() {
			if m.sessionStats != nil {
				delete(m.sessionStats, session)
			}
			continue
		}
		filtered = append(filtered, session)
		count++
	}
	m.sessions = filtered
	if len(m.sessions) == 0 {
		m.next = 0
	} else if m.next >= len(m.sessions) {
		m.next %= len(m.sessions)
	}
	return count
}

func (m *sessionManager) reserveSession() (tunnelSession, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) == 0 {
		return nil, func() {}
	}
	if m.sessionStats == nil {
		m.sessionStats = map[tunnelSession]*sessionHealth{}
	}
	filtered := m.sessions[:0]
	for _, session := range m.sessions {
		if session.IsClosed() {
			delete(m.sessionStats, session)
			continue
		}
		filtered = append(filtered, session)
	}
	m.sessions = filtered
	if len(m.sessions) == 0 {
		m.next = 0
		return nil, func() {}
	} else if m.next >= len(m.sessions) {
		m.next %= len(m.sessions)
	}

	bestIdx := -1
	var bestScore time.Duration
	for i := 0; i < len(m.sessions); i++ {
		idx := (m.next + i) % len(m.sessions)
		session := m.sessions[idx]
		score := m.sessionScoreLocked(session)
		if bestIdx == -1 || score < bestScore {
			bestIdx = idx
			bestScore = score
		}
	}
	if bestIdx >= 0 {
		session := m.sessions[bestIdx]
		m.sessionStats[session].activeStreams++
		m.next = (bestIdx + 1) % len(m.sessions)
		var once sync.Once
		release := func() {
			once.Do(func() {
				m.releaseSession(session)
			})
		}
		return session, release
	}
	m.sessions = nil
	m.next = 0
	return nil, func() {}
}

func (m *sessionManager) sessionScoreLocked(session tunnelSession) time.Duration {
	return m.sessionScoreAtLocked(session, time.Now())
}

func (m *sessionManager) sessionScoreAtLocked(session tunnelSession, now time.Time) time.Duration {
	health := m.sessionStats[session]
	if health == nil {
		health = &sessionHealth{}
		m.sessionStats[session] = health
	}

	score := time.Duration(health.activeStreams) * 10 * time.Second
	score += time.Duration(health.consecutiveFailures) * 30 * time.Second
	if health.ewmaCommandRTT > 0 {
		score += health.ewmaCommandRTT
	}
	if !health.lastFailure.IsZero() {
		if since := now.Sub(health.lastFailure); since < 30*time.Second {
			score += 30*time.Second - since
		}
	}
	return score
}

func (m *sessionManager) sessionHealthSnapshot() sessionHealthReport {
	now := time.Now()
	report := sessionHealthReport{GeneratedAt: now}

	m.mu.Lock()
	if m.sessionStats == nil {
		m.sessionStats = map[tunnelSession]*sessionHealth{}
	}
	filtered := m.sessions[:0]
	for _, session := range m.sessions {
		if session.IsClosed() {
			delete(m.sessionStats, session)
			continue
		}
		filtered = append(filtered, session)
	}
	m.sessions = filtered
	if len(m.sessions) == 0 {
		m.next = 0
	} else if m.next >= len(m.sessions) {
		m.next %= len(m.sessions)
	}

	report.SessionCount = len(m.sessions)
	report.Sessions = make([]sessionHealthEntry, 0, len(m.sessions))
	for i, session := range m.sessions {
		health := m.sessionStats[session]
		if health == nil {
			health = &sessionHealth{}
			m.sessionStats[session] = health
		}
		lastFailureAgo := int64(0)
		if !health.lastFailure.IsZero() {
			lastFailureAgo = now.Sub(health.lastFailure).Milliseconds()
			if lastFailureAgo < 0 {
				lastFailureAgo = 0
			}
		}
		report.Sessions = append(report.Sessions, sessionHealthEntry{
			Index:                i,
			RemoteAddr:           session.RemoteAddr().String(),
			ActiveStreams:        health.activeStreams,
			ConsecutiveFailures:  health.consecutiveFailures,
			EWMACommandRTTMillis: health.ewmaCommandRTT.Milliseconds(),
			LastFailureAgoMillis: lastFailureAgo,
			SchedulerScoreMillis: m.sessionScoreAtLocked(session, now).Milliseconds(),
		})
	}
	m.mu.Unlock()

	m.activeProxyMu.Lock()
	report.ActiveProxyConnections = m.activeProxyConns
	if len(m.activeProxyByPeer) > 0 {
		report.ActiveProxyConnectionsByPeer = make(map[string]int, len(m.activeProxyByPeer))
		for peer, count := range m.activeProxyByPeer {
			report.ActiveProxyConnectionsByPeer[peer] = count
		}
	}
	m.activeProxyMu.Unlock()

	return report
}

func (m *sessionManager) releaseSession(session tunnelSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	health := m.sessionStats[session]
	if health != nil && health.activeStreams > 0 {
		health.activeStreams--
	}
}

func (m *sessionManager) recordSessionSuccess(session tunnelSession, rtt time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	health := m.sessionStats[session]
	if health == nil {
		return
	}
	health.consecutiveFailures = 0
	health.lastFailure = time.Time{}
	if rtt <= 0 {
		return
	}
	if health.ewmaCommandRTT == 0 {
		health.ewmaCommandRTT = rtt
		return
	}
	health.ewmaCommandRTT = (health.ewmaCommandRTT*7 + rtt) / 8
}

func (m *sessionManager) recordSessionFailure(session tunnelSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	health := m.sessionStats[session]
	if health == nil {
		return
	}
	health.consecutiveFailures++
	health.lastFailure = time.Now()
}

type trackedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func (m *sessionManager) handleProxy(w http.ResponseWriter, req *http.Request) {
	if !m.proxyAllowed(req.RemoteAddr) {
		http.Error(w, "proxy forbidden", http.StatusForbidden)
		return
	}
	if req.Method == http.MethodGet && req.URL.Path == "/fetch" {
		if !m.fetch {
			http.Error(w, "fetch disabled", http.StatusNotFound)
			return
		}
		m.handleFetch(w, req)
		return
	}
	if req.Method == http.MethodGet && req.URL.Path == "/debug/session-health" {
		m.handleSessionHealth(w, req)
		return
	}
	if req.Method == http.MethodGet && req.URL.Path == "/debug/tunnel-bench" {
		m.handleTunnelBench(w, req)
		return
	}
	if req.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	release, ok, reason := m.acquireProxySlot(req.RemoteAddr)
	if !ok {
		http.Error(w, reason, http.StatusTooManyRequests)
		return
	}
	defer release()

	target := req.Host
	if m.resolve == "server" {
		resolvedTarget, err := resolveTarget(req.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		target = resolvedTarget
	}
	stripedStreams, striped, err := parseStripedConnectRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if striped {
		m.handleStripedConnect(w, target, stripedStreams)
		return
	}

	stream, streamReader, status, err := m.openCommand("CONNECT " + target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer stream.Close()
	if strings.TrimSpace(status) != "OK" {
		http.Error(w, strings.TrimSpace(status), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()
	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	pipeBoth(clientConn, &bufferedConn{Conn: stream, reader: streamReader}, m.proxyIdleTimeout)
}

func parseStripedConnectRequest(req *http.Request) (int, bool, error) {
	raw := strings.TrimSpace(req.Header.Get(stripedConnectHeader))
	if raw == "" {
		return 0, false, nil
	}
	streams, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("%s must be an integer", stripedConnectHeader)
	}
	if streams <= 1 {
		return 0, false, nil
	}
	if streams != maxStripedConnectStreams {
		return 0, false, fmt.Errorf("%s currently supports only %d", stripedConnectHeader, maxStripedConnectStreams)
	}
	return streams, true, nil
}

type stripedHubLane struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (m *sessionManager) handleStripedConnect(w http.ResponseWriter, target string, streams int) {
	lanes, err := m.openStripedConnect(target, streams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer closeStripedHubLanes(lanes)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()
	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	relayStripedConnect(clientConn, lanes)
}

func (m *sessionManager) openStripedConnect(target string, streams int) ([]stripedHubLane, error) {
	id := newStripedConnectID()
	lanes := make([]stripedHubLane, 0, streams)
	for i := 0; i < streams; i++ {
		command := fmt.Sprintf("STRIPED_CONNECT %s %d %d %s", id, i, streams, target)
		conn, reader, status, err := m.openCommand(command)
		if err != nil {
			closeStripedHubLanes(lanes)
			return nil, err
		}
		if status != "OK" {
			_ = conn.Close()
			closeStripedHubLanes(lanes)
			return nil, fmt.Errorf("striped connect lane %d: %s", i, status)
		}
		lanes = append(lanes, stripedHubLane{conn: conn, reader: reader})
	}
	return lanes, nil
}

func newStripedConnectID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func closeStripedHubLanes(lanes []stripedHubLane) {
	for _, lane := range lanes {
		if lane.conn != nil {
			_ = lane.conn.Close()
		}
	}
}

type stripedFrameResult struct {
	lane int
	seq  uint64
	data []byte
	eof  bool
	err  error
}

func relayStripedConnect(client net.Conn, lanes []stripedHubLane) {
	if len(lanes) == 0 {
		return
	}
	done := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			close(done)
			_ = client.Close()
			closeStripedHubLanes(lanes)
		})
	}
	defer closeAll()

	go func() {
		_, _ = io.Copy(lanes[0].conn, client)
		closeAll()
	}()

	frames := make(chan stripedFrameResult, len(lanes)*2)
	for i := range lanes {
		go readStripedFrames(done, i, lanes[i].reader, frames)
	}
	if err := writeOrderedStripedFrames(client, frames, len(lanes)); err != nil {
		log.Printf("striped connect relay failed: %v", err)
	}
}

func readStripedFrames(done <-chan struct{}, lane int, reader *bufio.Reader, out chan<- stripedFrameResult) {
	for {
		seq, data, eof, err := readStripedFrame(reader)
		result := stripedFrameResult{lane: lane, seq: seq, data: data, eof: eof, err: err}
		select {
		case out <- result:
		case <-done:
			return
		}
		if err != nil || eof {
			return
		}
	}
}

func writeOrderedStripedFrames(client io.Writer, frames <-chan stripedFrameResult, laneCount int) error {
	nextSeq := uint64(0)
	activeReaders := laneCount
	pending := map[uint64][]byte{}
	pendingBytes := 0
	var eofSeq *uint64

	for activeReaders > 0 {
		result := <-frames
		if result.err != nil {
			if errors.Is(result.err, io.EOF) {
				activeReaders--
				continue
			}
			return result.err
		}
		if result.eof {
			seq := result.seq
			eofSeq = &seq
		} else {
			pending[result.seq] = result.data
			pendingBytes += len(result.data)
			if pendingBytes > maxStripedPendingBytes {
				return errors.New("striped pending window exceeded")
			}
		}

		for {
			if eofSeq != nil && nextSeq == *eofSeq {
				return nil
			}
			data, ok := pending[nextSeq]
			if !ok {
				break
			}
			if _, err := client.Write(data); err != nil {
				return err
			}
			delete(pending, nextSeq)
			pendingBytes -= len(data)
			nextSeq++
		}
	}
	if eofSeq != nil && nextSeq == *eofSeq {
		return nil
	}
	return io.ErrUnexpectedEOF
}

func writeStripedFrame(writer io.Writer, seq uint64, data []byte) error {
	var header [stripedFrameHeaderSize]byte
	binary.BigEndian.PutUint64(header[:8], seq)
	binary.BigEndian.PutUint32(header[8:], uint32(len(data)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	_, err := writer.Write(data)
	return err
}

func readStripedFrame(reader *bufio.Reader) (uint64, []byte, bool, error) {
	var header [stripedFrameHeaderSize]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, false, err
	}
	seq := binary.BigEndian.Uint64(header[:8])
	length := binary.BigEndian.Uint32(header[8:])
	if length == 0 {
		return seq, nil, true, nil
	}
	if length > stripedChunkSize {
		return 0, nil, false, fmt.Errorf("striped frame too large: %d", length)
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return 0, nil, false, err
	}
	return seq, data, false, nil
}

func (m *sessionManager) handleSessionHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(m.sessionHealthSnapshot()); err != nil {
		log.Printf("encode session health failed: %v", err)
	}
}

func (m *sessionManager) handleTunnelBench(w http.ResponseWriter, req *http.Request) {
	totalBytes, streams, err := parseTunnelBenchParams(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	report := m.runTunnelBench(totalBytes, streams)
	w.Header().Set("Content-Type", "application/json")
	if !report.OK {
		w.WriteHeader(http.StatusBadGateway)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		log.Printf("encode tunnel bench failed: %v", err)
	}
}

func parseTunnelBenchParams(req *http.Request) (int64, int, error) {
	query := req.URL.Query()
	totalBytes := int64(defaultTunnelBenchBytes)
	if raw := strings.TrimSpace(query.Get("bytes")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("bytes must be a positive integer")
		}
		totalBytes = parsed
	}
	if totalBytes > maxTunnelBenchBytes {
		return 0, 0, fmt.Errorf("bytes must be <= %d", maxTunnelBenchBytes)
	}

	streams := 1
	if raw := strings.TrimSpace(query.Get("streams")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("streams must be a positive integer")
		}
		streams = parsed
	}
	if streams > maxTunnelBenchStreams {
		return 0, 0, fmt.Errorf("streams must be <= %d", maxTunnelBenchStreams)
	}
	if int64(streams) > totalBytes {
		return 0, 0, errors.New("streams must be <= bytes")
	}
	return totalBytes, streams, nil
}

func splitTunnelBenchBytes(totalBytes int64, streams int) []int64 {
	sizes := make([]int64, streams)
	base := totalBytes / int64(streams)
	remainder := totalBytes % int64(streams)
	for i := range sizes {
		sizes[i] = base
		if remainder > 0 {
			sizes[i]++
			remainder--
		}
	}
	return sizes
}

func (m *sessionManager) runTunnelBench(totalBytes int64, streams int) tunnelBenchReport {
	report := tunnelBenchReport{
		GeneratedAt:    time.Now(),
		RequestedBytes: totalBytes,
		Streams:        streams,
		OK:             true,
		PerStream:      make([]tunnelBenchStreamEntry, streams),
	}

	sizes := splitTunnelBenchBytes(totalBytes, streams)
	started := time.Now()
	var wg sync.WaitGroup
	for i, size := range sizes {
		wg.Add(1)
		go func(index int, requestedBytes int64) {
			defer wg.Done()
			report.PerStream[index] = m.runTunnelBenchStream(index, requestedBytes)
		}(i, size)
	}
	wg.Wait()

	report.DurationMillis = time.Since(started).Milliseconds()
	for _, stream := range report.PerStream {
		report.BytesRead += stream.BytesRead
		if stream.Error != "" {
			report.OK = false
			if report.Error == "" {
				report.Error = stream.Error
			}
		}
	}
	report.BytesPerSecond = rateBytesPerSecond(report.BytesRead, time.Since(started))
	report.Mbps = bytesPerSecondToMbps(report.BytesPerSecond)
	return report
}

func (m *sessionManager) runTunnelBenchStream(index int, requestedBytes int64) tunnelBenchStreamEntry {
	result := tunnelBenchStreamEntry{Index: index, RequestedBytes: requestedBytes}
	started := time.Now()
	stream, reader, status, err := m.openCommand(fmt.Sprintf("BENCH %d", requestedBytes))
	result.CommandRTTMillis = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer stream.Close()
	if status != "OK" {
		result.Error = status
		return result
	}

	if err := stream.SetReadDeadline(time.Now().Add(tunnelBenchTimeout)); err != nil {
		result.Error = err.Error()
		return result
	}
	copied, err := io.CopyN(io.Discard, reader, requestedBytes)
	result.BytesRead = copied
	result.DurationMillis = time.Since(started).Milliseconds()
	result.BytesPerSecond = rateBytesPerSecond(copied, time.Since(started))
	result.Mbps = bytesPerSecondToMbps(result.BytesPerSecond)
	_ = stream.SetReadDeadline(time.Time{})
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func rateBytesPerSecond(bytes int64, elapsed time.Duration) float64 {
	if bytes <= 0 || elapsed <= 0 {
		return 0
	}
	return float64(bytes) / elapsed.Seconds()
}

func bytesPerSecondToMbps(bytesPerSecond float64) float64 {
	return bytesPerSecond * 8 / 1_000_000
}

func (m *sessionManager) acquireProxySlot(remoteAddr string) (func(), bool, string) {
	peer := proxyPeer(remoteAddr)

	m.activeProxyMu.Lock()
	defer m.activeProxyMu.Unlock()

	if m.maxProxyConns > 0 && m.activeProxyConns >= m.maxProxyConns {
		return nil, false, "proxy busy"
	}
	if m.maxProxyConnsPeer > 0 && m.activeProxyByPeer[peer] >= m.maxProxyConnsPeer {
		return nil, false, "proxy busy for client"
	}

	if m.activeProxyByPeer == nil {
		m.activeProxyByPeer = map[string]int{}
	}
	m.activeProxyConns++
	m.activeProxyByPeer[peer]++

	var once sync.Once
	return func() {
		once.Do(func() {
			m.activeProxyMu.Lock()
			defer m.activeProxyMu.Unlock()
			if m.activeProxyConns > 0 {
				m.activeProxyConns--
			}
			if m.activeProxyByPeer[peer] <= 1 {
				delete(m.activeProxyByPeer, peer)
			} else {
				m.activeProxyByPeer[peer]--
			}
		})
	}, true, ""
}

func proxyPeer(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (m *sessionManager) proxyAllowed(remoteAddr string) bool {
	if len(m.allowedProxyNets) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range m.allowedProxyNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed_proxy_cidrs entry %q: %w", value, err)
		}
		out = append(out, network)
	}
	return out, nil
}

func (m *sessionManager) handleFetch(w http.ResponseWriter, req *http.Request) {
	rawURL := req.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		http.Error(w, "url must be absolute http(s)", http.StatusBadRequest)
		return
	}
	encodedURL := base64.RawURLEncoding.EncodeToString([]byte(rawURL))
	stream, reader, statusLine, err := m.openCommand("FETCH " + encodedURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer stream.Close()

	if strings.HasPrefix(statusLine, "ERR ") {
		http.Error(w, strings.TrimPrefix(statusLine, "ERR "), http.StatusBadGateway)
		return
	}
	statusText, ok := strings.CutPrefix(statusLine, "STATUS ")
	if !ok {
		http.Error(w, "invalid fetch response", http.StatusBadGateway)
		return
	}
	status, err := strconv.Atoi(statusText)
	if err != nil {
		http.Error(w, "invalid fetch status", http.StatusBadGateway)
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		line = strings.TrimSpace(line)
		if line == "ENDHDR" {
			break
		}
		name, value, ok := parseEncodedHeader(line)
		if ok {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, reader)
}

func parseEncodedHeader(line string) (string, string, bool) {
	rest, ok := strings.CutPrefix(line, "HEADER ")
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	name, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", false
	}
	value, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false
	}
	return string(name), string(value), true
}

func resolveTarget(authority string) (string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return "", err
	}
	if net.ParseIP(host) != nil {
		return authority, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			return net.JoinHostPort(ip.IP.String(), port), nil
		}
	}
	if len(ips) > 0 {
		return net.JoinHostPort(ips[0].IP.String(), port), nil
	}
	return "", fmt.Errorf("no addresses for %s", host)
}

func runClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	defaults := defaultClientOptions()
	configPath := fs.String("config", "", "YAML config path")
	serverAddr := fs.String("server", defaults.Server, "reverse server address")
	token := fs.String("token", defaults.Token, "shared auth token")
	tokenFile := fs.String("token-file", defaults.TokenFile, "file containing shared auth token")
	reconnect := fs.Duration("reconnect", defaults.Reconnect, "reconnect delay")
	transport := fs.String("transport", defaults.Transport, "reverse transport: tcp or quic")
	connections := fs.Int("connections", defaults.Connections, "number of parallel reverse connections")
	addressFamily := fs.String("address-family", defaults.AddressFamily, "target dial address family: auto, ipv4, or ipv6")
	serverCertSHA256 := fs.String("server-cert-sha256", defaults.ServerCertSHA256, "expected SHA-256 fingerprint of QUIC server certificate")
	insecureSkipVerify := fs.Bool("insecure-skip-verify", defaults.InsecureSkipVerify, "allow QUIC without certificate pinning; unsafe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	explicit := visitedFlags(fs)
	opts := defaults
	if *configPath != "" {
		cfg, err := loadReverseConfig(*configPath)
		if err != nil {
			return err
		}
		opts = cfg.Client.withDefaults(defaults)
	}
	if explicit["server"] {
		opts.Server = *serverAddr
	}
	if explicit["token"] {
		opts.Token = *token
	}
	if explicit["token-file"] {
		opts.TokenFile = *tokenFile
	}
	if explicit["reconnect"] {
		opts.Reconnect = *reconnect
	}
	if explicit["transport"] {
		opts.Transport = *transport
	}
	if explicit["connections"] {
		opts.Connections = *connections
	}
	if explicit["address-family"] {
		opts.AddressFamily = *addressFamily
	}
	if explicit["server-cert-sha256"] {
		opts.ServerCertSHA256 = *serverCertSHA256
	}
	if explicit["insecure-skip-verify"] {
		opts.InsecureSkipVerify = *insecureSkipVerify
	}
	resolvedToken, err := resolveToken(opts.Token, opts.TokenFile)
	if err != nil {
		return err
	}
	if opts.Server == "" || resolvedToken == "" {
		return errors.New("--server and --token are required")
	}
	if opts.Connections < 1 || opts.Connections > 64 {
		return errors.New("--connections must be between 1 and 64")
	}
	if opts.AddressFamily != "auto" && opts.AddressFamily != "ipv4" && opts.AddressFamily != "ipv6" {
		return errors.New("--address-family must be auto, ipv4, or ipv6")
	}
	if opts.Transport == "quic" && !opts.InsecureSkipVerify && normalizeFingerprint(opts.ServerCertSHA256) == "" {
		return errors.New("--server-cert-sha256 is required for quic transport")
	}
	if pin := normalizeFingerprint(opts.ServerCertSHA256); pin != "" && len(pin) != sha256.Size*2 {
		return errors.New("--server-cert-sha256 must be a 64-character SHA-256 hex fingerprint")
	}

	var wg sync.WaitGroup
	for i := 0; i < opts.Connections; i++ {
		connID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if err := clientOnce(opts.Transport, opts.Server, resolvedToken, opts); err != nil {
					log.Printf("client connection %d/%d disconnected: %v", connID, opts.Connections, err)
				}
				time.Sleep(opts.Reconnect)
			}
		}()
	}
	wg.Wait()
	return nil
}

func clientOnce(transport string, serverAddr string, token string, opts clientOptions) error {
	switch transport {
	case "tcp":
		return tcpClientOnce(serverAddr, token, opts)
	case "quic":
		return quicClientOnce(serverAddr, token, opts)
	default:
		return fmt.Errorf("unknown transport: %s", transport)
	}
}

func tcpClientOnce(serverAddr string, token string, opts clientOptions) error {
	conn, err := net.DialTimeout("tcp", serverAddr, 15*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "%s %s\n", protocolHello, token); err != nil {
		return err
	}

	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 10 * time.Second
	cfg.MaxStreamWindowSize = 4 * 1024 * 1024
	cfg.ConnectionWriteTimeout = 30 * time.Second
	session, err := yamux.Client(conn, cfg)
	if err != nil {
		return err
	}
	defer session.Close()
	log.Printf("connected to reverse tcp server %s", serverAddr)

	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		go handleClientStream(stream, opts)
	}
}

func quicClientOnce(serverAddr string, token string, opts clientOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn, err := quic.DialAddr(ctx, serverAddr, clientTLSConfig(opts), quicConfig())
	cancel()
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "client closed")

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	authStream, err := conn.OpenStreamSync(ctx)
	cancel()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(authStream, "%s %s\n", protocolHello, token); err != nil {
		_ = authStream.Close()
		return err
	}
	log.Printf("connected to reverse quic server %s", serverAddr)

	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return err
		}
		go handleClientStream(&quicStreamConn{stream: stream, local: conn.LocalAddr(), remote: conn.RemoteAddr()}, opts)
	}
}

func handleClientStream(stream net.Conn, opts clientOptions) {
	defer stream.Close()
	reader := bufio.NewReader(stream)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "CONNECT ")
	if ok {
		handleConnectStream(stream, reader, target, opts)
		return
	}
	fetchURL, ok := strings.CutPrefix(strings.TrimSpace(line), "FETCH ")
	if ok {
		handleFetchStream(stream, fetchURL, opts)
		return
	}
	stripedConnect, ok := strings.CutPrefix(strings.TrimSpace(line), "STRIPED_CONNECT ")
	if ok {
		handleStripedConnectStream(stream, stripedConnect, opts)
		return
	}
	benchBytes, ok := strings.CutPrefix(strings.TrimSpace(line), "BENCH ")
	if ok {
		handleBenchStream(stream, benchBytes)
		return
	}
	_, _ = io.WriteString(stream, "ERR invalid command\n")
}

func handleConnectStream(stream net.Conn, reader *bufio.Reader, target string, opts clientOptions) {
	if target == "" {
		_, _ = io.WriteString(stream, "ERR invalid command\n")
		return
	}
	targetConn, err := dialTarget(target, opts.AddressFamily)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	if _, err := io.WriteString(stream, "OK\n"); err != nil {
		_ = targetConn.Close()
		return
	}
	relayWithHandshakeRetry(&bufferedConn{Conn: stream, reader: reader}, targetConn, target, func() (net.Conn, error) {
		return dialTarget(target, opts.AddressFamily)
	})
}

var stripedClientGroups sync.Map

type stripedClientGroup struct {
	id        string
	target    string
	laneCount int

	mu                sync.Mutex
	lanes             []net.Conn
	laneReady         []bool
	readyCount        int
	targetConn        net.Conn
	err               error
	targetReadyCh     chan struct{}
	allReadyCh        chan struct{}
	done              chan struct{}
	targetReadyClosed bool
	allReadyClosed    bool
	doneClosed        bool
}

type stripedOutboundFrame struct {
	seq  uint64
	data []byte
}

func newStripedClientGroup(id string, target string, laneCount int) *stripedClientGroup {
	return &stripedClientGroup{
		id:            id,
		target:        target,
		laneCount:     laneCount,
		lanes:         make([]net.Conn, laneCount),
		laneReady:     make([]bool, laneCount),
		targetReadyCh: make(chan struct{}),
		allReadyCh:    make(chan struct{}),
		done:          make(chan struct{}),
	}
}

func getStripedClientGroup(id string, target string, laneCount int) (*stripedClientGroup, error) {
	actual, _ := stripedClientGroups.LoadOrStore(id, newStripedClientGroup(id, target, laneCount))
	group := actual.(*stripedClientGroup)
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.target != target || group.laneCount != laneCount {
		return nil, errors.New("striped connect group mismatch")
	}
	return group, nil
}

func (g *stripedClientGroup) attachLane(index int, conn net.Conn) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index < 0 || index >= g.laneCount {
		return errors.New("striped lane index out of range")
	}
	if g.lanes[index] != nil {
		return errors.New("striped lane already attached")
	}
	g.lanes[index] = conn
	return nil
}

func (g *stripedClientGroup) setTarget(conn net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.targetConn = conn
	g.closeTargetReadyLocked()
}

func (g *stripedClientGroup) fail(err error) {
	stripedClientGroups.Delete(g.id)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err == nil {
		g.err = err
	}
	g.closeTargetReadyLocked()
	g.closeAllReadyLocked()
	g.closeDoneLocked()
	for _, lane := range g.lanes {
		if lane != nil {
			_ = lane.Close()
		}
	}
	if g.targetConn != nil {
		_ = g.targetConn.Close()
	}
}

func (g *stripedClientGroup) markLaneReady(index int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index >= 0 && index < len(g.laneReady) && !g.laneReady[index] {
		g.laneReady[index] = true
		g.readyCount++
	}
	if g.readyCount == g.laneCount {
		g.closeAllReadyLocked()
	}
}

func (g *stripedClientGroup) error() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

func (g *stripedClientGroup) closeTargetReadyLocked() {
	if !g.targetReadyClosed {
		close(g.targetReadyCh)
		g.targetReadyClosed = true
	}
}

func (g *stripedClientGroup) closeAllReadyLocked() {
	if !g.allReadyClosed {
		close(g.allReadyCh)
		g.allReadyClosed = true
	}
}

func (g *stripedClientGroup) closeDoneLocked() {
	if !g.doneClosed {
		close(g.done)
		g.doneClosed = true
	}
}

func waitForSignal(ch <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

func handleStripedConnectStream(stream net.Conn, args string, opts clientOptions) {
	fields := strings.Fields(args)
	if len(fields) != 4 {
		_, _ = io.WriteString(stream, "ERR invalid striped connect command\n")
		return
	}
	id := fields[0]
	laneIndex, err := strconv.Atoi(fields[1])
	if err != nil {
		_, _ = io.WriteString(stream, "ERR invalid striped lane index\n")
		return
	}
	laneCount, err := strconv.Atoi(fields[2])
	if err != nil || laneCount != maxStripedConnectStreams {
		_, _ = io.WriteString(stream, "ERR invalid striped lane count\n")
		return
	}
	target := fields[3]
	group, err := getStripedClientGroup(id, target, laneCount)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	if err := group.attachLane(laneIndex, stream); err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}

	if laneIndex == 0 {
		handleStripedPrimaryLane(stream, group, target, opts)
		return
	}
	handleStripedExtraLane(stream, group, laneIndex)
}

func handleStripedPrimaryLane(stream net.Conn, group *stripedClientGroup, target string, opts clientOptions) {
	defer stripedClientGroups.Delete(group.id)
	targetConn, err := dialTarget(target, opts.AddressFamily)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		group.fail(err)
		return
	}
	group.setTarget(targetConn)
	if _, err := io.WriteString(stream, "OK\n"); err != nil {
		group.fail(err)
		return
	}
	group.markLaneReady(0)
	if !waitForSignal(group.allReadyCh, stripedLaneAttachTimeout) {
		group.fail(errors.New("striped lanes did not attach in time"))
		return
	}
	if err := group.error(); err != nil {
		group.fail(err)
		return
	}
	group.run()
}

func handleStripedExtraLane(stream net.Conn, group *stripedClientGroup, laneIndex int) {
	if !waitForSignal(group.targetReadyCh, stripedTargetReadyTimeout) {
		_, _ = io.WriteString(stream, "ERR striped target not ready\n")
		group.fail(errors.New("striped target not ready in time"))
		return
	}
	if err := group.error(); err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	if _, err := io.WriteString(stream, "OK\n"); err != nil {
		group.fail(err)
		return
	}
	group.markLaneReady(laneIndex)
	<-group.done
}

func (g *stripedClientGroup) run() {
	g.mu.Lock()
	targetConn := g.targetConn
	lanes := append([]net.Conn(nil), g.lanes...)
	g.mu.Unlock()

	done := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			close(done)
			for _, lane := range lanes {
				if lane != nil {
					_ = lane.Close()
				}
			}
			if targetConn != nil {
				_ = targetConn.Close()
			}
			g.mu.Lock()
			g.closeDoneLocked()
			g.mu.Unlock()
		})
	}
	defer closeAll()

	laneChans := make([]chan stripedOutboundFrame, len(lanes))
	writerErrs := make(chan error, len(lanes))
	var writers sync.WaitGroup
	var closeFrameChansOnce sync.Once
	closeFrameChans := func() {
		closeFrameChansOnce.Do(func() {
			for _, ch := range laneChans {
				if ch != nil {
					close(ch)
				}
			}
		})
	}
	for i, lane := range lanes {
		laneChans[i] = make(chan stripedOutboundFrame, 8)
		writers.Add(1)
		go func(conn net.Conn, frames <-chan stripedOutboundFrame) {
			defer writers.Done()
			for frame := range frames {
				if err := writeStripedFrame(conn, frame.seq, frame.data); err != nil {
					select {
					case writerErrs <- err:
					default:
					}
					return
				}
				if len(frame.data) == 0 {
					return
				}
			}
		}(lane, laneChans[i])
	}
	defer func() {
		closeFrameChans()
		writers.Wait()
	}()

	sendFrame := func(index int, frame stripedOutboundFrame) bool {
		select {
		case laneChans[index] <- frame:
			return true
		case err := <-writerErrs:
			if err != nil {
				log.Printf("striped connect writer failed: %v", err)
			}
			closeAll()
			return false
		case <-done:
			return false
		}
	}

	go func() {
		_, _ = io.Copy(targetConn, lanes[0])
		closeAll()
	}()

	buf := make([]byte, stripedChunkSize)
	seq := uint64(0)
	laneIndex := 0
	for {
		n, readErr := targetConn.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if !sendFrame(laneIndex, stripedOutboundFrame{seq: seq, data: data}) {
				return
			}
			seq++
			laneIndex = (laneIndex + 1) % len(lanes)
		}
		if readErr != nil {
			_ = sendFrame(laneIndex%len(lanes), stripedOutboundFrame{seq: seq})
			closeFrameChans()
			writers.Wait()
			return
		}
	}
}

// 乐天 F5 的 v4 侧会非确定性地黑洞新建连接:TCP 握手成功、ClientHello 被
// ACK,但一个响应字节都等不到(约 15s 后才 RST)。故障按单条连接随机,换
// 一条新连接重发同样的首包通常就能成功(见 worklog 2026-06-10/11)。看门
// 狗只在「首个客户端载荷是 TLS ClientHello 且目标还没回过任何字节」时启
// 用,此时重放在协议上等价于客户端自己断开重连。
var handshakeFirstByteTimeout = 3 * time.Second

const (
	handshakeMaxDials    = 3
	handshakeReplayLimit = 16 * 1024
)

type handshakeRelay struct {
	mu         sync.Mutex
	target     net.Conn
	replay     []byte // 已发给目标的客户端字节,重拨后整体重放
	replayOK   bool   // 重放仍可行;目标回过字节或缓冲超限后永久关闭
	seenClient bool
	armed      bool // 首个客户端载荷是 TLS ClientHello,看门狗生效
	settled    bool // 目标已回字节,退化为普通双向中继
	closed     bool
}

func looksLikeTLSClientHello(p []byte) bool {
	// TLS record 头:content type handshake(22) + 版本主号 3,record 头后
	// 紧跟的握手消息类型必须是 client_hello(1)。
	return len(p) >= 6 && p[0] == 0x16 && p[1] == 0x03 && p[5] == 0x01
}

func (r *handshakeRelay) writeToTarget(p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replayOK && !r.settled {
		if !r.seenClient {
			r.seenClient = true
			if looksLikeTLSClientHello(p) {
				r.armed = true
				_ = r.target.SetReadDeadline(time.Now().Add(handshakeFirstByteTimeout))
			} else {
				// 非 TLS 首包(如明文 HTTP 请求)重放可能重复执行副作用,
				// 不启用看门狗。
				r.replayOK = false
				r.replay = nil
			}
		}
		if r.replayOK {
			if len(r.replay)+len(p) > handshakeReplayLimit {
				r.replayOK = false
				r.replay = nil
			} else {
				r.replay = append(r.replay, p...)
			}
		}
	}
	if _, err := r.target.Write(p); err != nil {
		if r.armed && r.replayOK && !r.settled && !r.closed {
			// 写失败的字节都在 replay 缓冲里,看门狗随后会重拨重放。
			return nil
		}
		return err
	}
	return nil
}

func (r *handshakeRelay) settle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settled = true
	r.replayOK = false
	r.replay = nil
	_ = r.target.SetReadDeadline(time.Time{})
}

func (r *handshakeRelay) retryable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.armed && r.replayOK && !r.settled && !r.closed && len(r.replay) > 0
}

// disarm 在重试额度耗尽或重放不可行时清掉首字节死线,让中继退回与旧实现
// 一致的阻塞行为;返回是否真的解除了已布防的看门狗。
func (r *handshakeRelay) disarm() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.armed || r.settled || r.closed {
		return false
	}
	r.armed = false
	r.replayOK = false
	r.replay = nil
	_ = r.target.SetReadDeadline(time.Time{})
	return true
}

func (r *handshakeRelay) swapTarget(newConn net.Conn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.replayOK || r.settled {
		_ = newConn.Close()
		return errors.New("handshake replay no longer possible")
	}
	_ = r.target.Close()
	r.target = newConn
	_ = newConn.SetReadDeadline(time.Now().Add(handshakeFirstByteTimeout))
	if _, err := newConn.Write(r.replay); err != nil {
		return err
	}
	return nil
}

// relayWithHandshakeRetry 等价于 pipeBoth(client, targetConn, 0),但对 TLS
// 首飞加看门狗:目标在 handshakeFirstByteTimeout 内零响应(或直接被 RST)
// 时重拨并重放已缓冲的客户端字节,总共最多 handshakeMaxDials 次拨号;额度
// 用完退回普通阻塞中继。目标回过第一个字节后重放窗口永久关闭。
func relayWithHandshakeRetry(client net.Conn, targetConn net.Conn, target string, redial func() (net.Conn, error)) {
	relay := &handshakeRelay{target: targetConn, replayOK: true}

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			relay.mu.Lock()
			relay.closed = true
			tc := relay.target
			relay.mu.Unlock()
			_ = client.Close()
			_ = tc.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := client.Read(buf)
			if n > 0 {
				if err := relay.writeToTarget(buf[:n]); err != nil {
					break
				}
			}
			if readErr != nil {
				break
			}
		}
		closeBoth()
	}()

	tc := targetConn
	dials := 1
	settled := false
	buf := make([]byte, 32*1024)
	for {
		n, readErr := tc.Read(buf)
		if n > 0 {
			if !settled {
				settled = true
				relay.settle()
				if dials > 1 {
					log.Printf("connect %s: target answered on dial %d/%d", target, dials, handshakeMaxDials)
				}
			}
			if _, err := client.Write(buf[:n]); err != nil {
				break
			}
		}
		if readErr == nil {
			continue
		}
		if settled || !relay.retryable() || dials >= handshakeMaxDials {
			var netErr net.Error
			if !settled && errors.As(readErr, &netErr) && netErr.Timeout() && relay.disarm() {
				// 看门狗死线到了但已无法重试:解除死线继续阻塞等待,
				// 行为与旧实现一致(由对端或上游空闲回收兜底)。
				continue
			}
			break
		}
		log.Printf("connect %s: no server bytes (%v), redialing %d/%d", target, readErr, dials+1, handshakeMaxDials)
		newConn, dialErr := redial()
		if dialErr != nil {
			log.Printf("connect %s: redial failed: %v", target, dialErr)
			break
		}
		dials++
		if err := relay.swapTarget(newConn); err != nil {
			break
		}
		tc = newConn
	}
	closeBoth()
	wg.Wait()
}

func handleFetchStream(stream net.Conn, encodedURL string, opts clientOptions) {
	rawURL, err := base64.RawURLEncoding.DecodeString(encodedURL)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	req, err := http.NewRequest(http.MethodGet, string(rawURL), nil)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	req.Header.Set("User-Agent", "zhreverse-fetch/0")
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: fetchRootCAs()},
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			return dialTarget(addr, opts.AddressFamily)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	defer resp.Body.Close()
	if _, err := fmt.Fprintf(stream, "STATUS %d\n", resp.StatusCode); err != nil {
		return
	}
	for name, values := range resp.Header {
		for _, value := range values {
			encodedName := base64.RawURLEncoding.EncodeToString([]byte(name))
			encodedValue := base64.RawURLEncoding.EncodeToString([]byte(value))
			if _, err := fmt.Fprintf(stream, "HEADER %s %s\n", encodedName, encodedValue); err != nil {
				return
			}
		}
	}
	if _, err := io.WriteString(stream, "ENDHDR\n"); err != nil {
		return
	}
	_, _ = io.Copy(stream, resp.Body)
}

func handleBenchStream(stream net.Conn, rawBytes string) {
	totalBytes, err := strconv.ParseInt(strings.TrimSpace(rawBytes), 10, 64)
	if err != nil || totalBytes <= 0 {
		_, _ = io.WriteString(stream, "ERR invalid bench bytes\n")
		return
	}
	if totalBytes > maxTunnelBenchBytes {
		_, _ = fmt.Fprintf(stream, "ERR bench bytes must be <= %d\n", maxTunnelBenchBytes)
		return
	}
	if _, err := io.WriteString(stream, "OK\n"); err != nil {
		return
	}

	buf := make([]byte, 32*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	remaining := totalBytes
	for remaining > 0 {
		chunk := int64(len(buf))
		if remaining < chunk {
			chunk = remaining
		}
		if _, err := stream.Write(buf[:chunk]); err != nil {
			return
		}
		remaining -= chunk
	}
}

func fetchRootCAs() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, dir := range []string{"/system/etc/security/cacerts", "/apex/com.android.conscrypt/cacerts"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			pemData, err := os.ReadFile(dir + "/" + entry.Name())
			if err == nil {
				pool.AppendCertsFromPEM(pemData)
			}
		}
	}
	return pool
}

// resolve: client 后每个 CONNECT 都要做一次 DNS(v6 UDP 约 20-60ms,偶发更
// 慢),浏览器并发开页时放大明显;进程内缓存 60s 抹平这部分延迟。缓存存原
// 始解析结果,orderIPs 仍在拨号时按地址族偏好排序。
const (
	dnsCacheTTL        = 60 * time.Second
	dnsCacheMaxEntries = 1024
)

type dnsCacheEntry struct {
	ips     []net.IPAddr
	expires time.Time
}

var (
	dnsCacheMu sync.Mutex
	dnsCache   = map[string]dnsCacheEntry{}
)

func dnsCacheGet(host string, now time.Time) ([]net.IPAddr, bool) {
	dnsCacheMu.Lock()
	defer dnsCacheMu.Unlock()
	entry, ok := dnsCache[host]
	if !ok || now.After(entry.expires) {
		return nil, false
	}
	return entry.ips, true
}

func dnsCachePut(host string, ips []net.IPAddr, now time.Time) {
	dnsCacheMu.Lock()
	defer dnsCacheMu.Unlock()
	// 条目封顶,满了整体清空,不值得为此上 LRU。
	if len(dnsCache) >= dnsCacheMaxEntries {
		clear(dnsCache)
	}
	dnsCache[host] = dnsCacheEntry{ips: ips, expires: now.Add(dnsCacheTTL)}
}

func dialTarget(target string, addressFamily string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		return net.DialTimeout("tcp", target, 15*time.Second)
	}
	ips, cached := dnsCacheGet(host, time.Now())
	if !cached {
		// Hub-side openCommand gives the whole CONNECT 20s, so keep DNS and the
		// dial attempts on separate budgets instead of sharing one context.
		dnsCtx, dnsCancel := context.WithTimeout(context.Background(), 6*time.Second)
		resolver := publicResolver()
		resolved, err := resolver.LookupIPAddr(dnsCtx, host)
		dnsCancel()
		if err != nil {
			// 解析失败不缓存,下次连接重试。
			return nil, err
		}
		ips = resolved
		dnsCachePut(host, ips, time.Now())
	}
	var lastErr error
	for attempt, ip := range orderIPs(ips, addressFamily) {
		if attempt >= 2 {
			break
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.IP.String(), port), 6*time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no addresses for %s", host)
}

func publicResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network string, address string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			// Rakuten 蜂窝网上 IPv4 UDP/53 经 CGNAT 间歇丢包,IPv6 路径实测
			// 健康得多,因此 v6 解析器优先。
			var lastErr error
			for _, server := range []string{"[2606:4700:4700::1111]:53", "1.1.1.1:53", "[2001:4860:4860::8888]:53", "8.8.8.8:53"} {
				conn, err := dialer.DialContext(ctx, "udp", server)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

func orderIPs(ips []net.IPAddr, addressFamily string) []net.IPAddr {
	if len(ips) < 2 {
		return ips
	}
	if addressFamily == "ipv6" {
		return preferIPFamily(ips, false)
	}
	return preferIPFamily(ips, true)
}

func preferIPFamily(ips []net.IPAddr, preferIPv4 bool) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		if (ip.IP.To4() != nil) == preferIPv4 {
			out = append(out, ip)
		}
	}
	for _, ip := range ips {
		if (ip.IP.To4() != nil) != preferIPv4 {
			out = append(out, ip)
		}
	}
	return out
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func pipeBoth(a net.Conn, b net.Conn, idleTimeout time.Duration) {
	var wg sync.WaitGroup
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}

	activity := make(chan struct{}, 1)
	done := make(chan struct{})
	if idleTimeout > 0 {
		go reapIdleConnections(idleTimeout, activity, done, closeBoth)
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		copyWithActivity(a, b, activity)
		closeBoth()
	}()
	go func() {
		defer wg.Done()
		copyWithActivity(b, a, activity)
		closeBoth()
	}()
	wg.Wait()
	close(done)
}

func copyWithActivity(dst net.Conn, src net.Conn, activity chan<- struct{}) {
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, writeErr := dst.Write(buffer[:n]); writeErr != nil {
				return
			}
			noteActivity(activity)
		}
		if readErr != nil {
			return
		}
	}
}

func noteActivity(activity chan<- struct{}) {
	if activity == nil {
		return
	}
	select {
	case activity <- struct{}{}:
	default:
	}
}

func reapIdleConnections(idleTimeout time.Duration, activity <-chan struct{}, done <-chan struct{}, closeBoth func()) {
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	for {
		select {
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)
		case <-timer.C:
			closeBoth()
			return
		case <-done:
			return
		}
	}
}

func readHello(conn net.Conn, token string) error {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := readLineBytewise(conn, 256)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
	if len(parts) != 2 || parts[0] != protocolHello {
		return errors.New("invalid hello")
	}
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
		return errors.New("invalid token")
	}
	return nil
}

type quicStreamConn struct {
	stream *quic.Stream
	local  net.Addr
	remote net.Addr
}

func (c *quicStreamConn) Read(p []byte) (int, error) {
	return c.stream.Read(p)
}

func (c *quicStreamConn) Write(p []byte) (int, error) {
	return c.stream.Write(p)
}

func (c *quicStreamConn) Close() error {
	return c.stream.Close()
}

func (c *quicStreamConn) LocalAddr() net.Addr {
	return c.local
}

func (c *quicStreamConn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *quicStreamConn) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *quicStreamConn) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

func quicConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout:           10 * time.Second,
		MaxIdleTimeout:                 30 * time.Second,
		KeepAlivePeriod:                5 * time.Second,
		InitialStreamReceiveWindow:     4 * 1024 * 1024,
		MaxStreamReceiveWindow:         16 * 1024 * 1024,
		InitialConnectionReceiveWindow: 8 * 1024 * 1024,
		MaxConnectionReceiveWindow:     32 * 1024 * 1024,
		MaxIncomingStreams:             256,
		InitialPacketSize:              1200,
		EnableDatagrams:                true,
	}
}

func serverTLSConfig(certFile string, keyFile string) (*tls.Config, error) {
	cert, err := loadOrCreateServerCert(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{quicALPN},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func clientTLSConfig(opts clientOptions) *tls.Config {
	pin := normalizeFingerprint(opts.ServerCertSHA256)
	return &tls.Config{
		InsecureSkipVerify: opts.InsecureSkipVerify || pin != "",
		NextProtos:         []string{quicALPN},
		MinVersion:         tls.VersionTLS13,
		VerifyConnection: func(state tls.ConnectionState) error {
			if opts.InsecureSkipVerify {
				return nil
			}
			if pin == "" {
				return errors.New("missing server certificate pin")
			}
			if len(state.PeerCertificates) == 0 {
				return errors.New("server certificate missing")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			actual := hex.EncodeToString(sum[:])
			if subtle.ConstantTimeCompare([]byte(actual), []byte(pin)) != 1 {
				return fmt.Errorf("server certificate pin mismatch: got %s", actual)
			}
			return nil
		},
	}
}

func loadOrCreateServerCert(certFile string, keyFile string) (tls.Certificate, error) {
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return tls.Certificate{}, errors.New("tls_cert_file and tls_key_file must be set together")
		}
		return tls.LoadX509KeyPair(certFile, keyFile)
	}
	return selfSignedCert()
}

func normalizeFingerprint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.ReplaceAll(value, ":", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func selfSignedCert() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "zhreverse"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func readLineBytewise(conn net.Conn, limit int) (string, error) {
	var b strings.Builder
	for b.Len() < limit {
		buf := []byte{0}
		n, err := conn.Read(buf)
		if err != nil {
			return "", err
		}
		if n == 1 {
			b.WriteByte(buf[0])
			if buf[0] == '\n' {
				return b.String(), nil
			}
		}
	}
	return "", errors.New("line too long")
}
