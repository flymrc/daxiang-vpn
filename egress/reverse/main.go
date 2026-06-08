package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
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

const protocolHello = "DXREV1"
const quicALPN = "dxreverse/1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: dxreverse server|client [flags]")
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
	Listen    string `json:"listen" yaml:"listen"`
	Proxy     string `json:"proxy" yaml:"proxy"`
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	TokenFile string `json:"token_file,omitempty" yaml:"token_file,omitempty"`
	Transport string `json:"transport" yaml:"transport"`
	Resolve   string `json:"resolve" yaml:"resolve"`
}

type clientOptions struct {
	Server      string        `json:"server" yaml:"server"`
	Token       string        `json:"token,omitempty" yaml:"token,omitempty"`
	TokenFile   string        `json:"token_file,omitempty" yaml:"token_file,omitempty"`
	Reconnect   time.Duration `json:"reconnect" yaml:"reconnect"`
	Transport   string        `json:"transport" yaml:"transport"`
	Connections int           `json:"connections" yaml:"connections"`
}

func defaultServerOptions() serverOptions {
	return serverOptions{
		Listen:    ":39093",
		Proxy:     "127.0.0.1:18081",
		Transport: "quic",
		Resolve:   "server",
	}
}

func defaultClientOptions() clientOptions {
	return clientOptions{
		Reconnect:   3 * time.Second,
		Transport:   "quic",
		Connections: 4,
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

	manager := &sessionManager{resolve: opts.Resolve}
	go func() {
		if err := serveTunnel(opts.Transport, opts.Listen, resolvedToken, manager); err != nil {
			log.Printf("tunnel listener stopped: %v", err)
		}
	}()

	server := &http.Server{
		Addr:              opts.Proxy,
		Handler:           http.HandlerFunc(manager.handleProxy),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("reverse server listening transport=%s resolve=%s tunnel=%s proxy=%s", opts.Transport, opts.Resolve, opts.Listen, opts.Proxy)
	return server.ListenAndServe()
}

func serveTunnel(transport string, addr string, token string, manager *sessionManager) error {
	switch transport {
	case "tcp":
		return serveTCPTunnel(addr, token, manager)
	case "quic":
		return serveQUICTunnel(addr, token, manager)
	default:
		return fmt.Errorf("unknown transport: %s", transport)
	}
}

func serveTCPTunnel(addr string, token string, manager *sessionManager) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
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

func serveQUICTunnel(addr string, token string, manager *sessionManager) error {
	listener, err := quic.ListenAddr(addr, serverTLSConfig(), quicConfig())
	if err != nil {
		return err
	}
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
	mu       sync.RWMutex
	sessions []tunnelSession
	next     int
	resolve  string
}

func (m *sessionManager) set(session tunnelSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = append(m.sessions, session)
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
	for attempt := 0; attempt < 2; attempt++ {
		session := m.pickSession()
		if session == nil {
			return nil, errors.New("reverse client is not connected")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := session.OpenStream(ctx)
		cancel()
		if err == nil {
			return stream, nil
		}
		log.Printf("open stream via %s failed: %v", session.RemoteAddr(), err)
		m.clearCurrent(session)
	}
	return nil, errors.New("no usable reverse client session")
}

func (m *sessionManager) pickSession() tunnelSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) == 0 {
		return nil
	}
	for i := 0; i < len(m.sessions); i++ {
		idx := (m.next + i) % len(m.sessions)
		session := m.sessions[idx]
		if session.IsClosed() {
			continue
		}
		m.next = (idx + 1) % len(m.sessions)
		return session
	}
	m.sessions = nil
	m.next = 0
	return nil
}

func (m *sessionManager) handleProxy(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet && req.URL.Path == "/fetch" {
		m.handleFetch(w, req)
		return
	}
	if req.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	stream, err := m.openStream()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer stream.Close()

	target := req.Host
	if m.resolve == "server" {
		resolvedTarget, err := resolveTarget(req.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		target = resolvedTarget
	}
	if _, err := fmt.Fprintf(stream, "CONNECT %s\n", target); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	streamReader := bufio.NewReader(stream)
	status, err := streamReader.ReadString('\n')
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
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
	pipeBoth(clientConn, &bufferedConn{Conn: stream, reader: streamReader})
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
	stream, err := m.openStream()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer stream.Close()

	encodedURL := base64.RawURLEncoding.EncodeToString([]byte(rawURL))
	if _, err := fmt.Fprintf(stream, "FETCH %s\n", encodedURL); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	reader := bufio.NewReader(stream)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	statusLine = strings.TrimSpace(statusLine)
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

	var wg sync.WaitGroup
	for i := 0; i < opts.Connections; i++ {
		connID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if err := clientOnce(opts.Transport, opts.Server, resolvedToken); err != nil {
					log.Printf("client connection %d/%d disconnected: %v", connID, opts.Connections, err)
				}
				time.Sleep(opts.Reconnect)
			}
		}()
	}
	wg.Wait()
	return nil
}

func clientOnce(transport string, serverAddr string, token string) error {
	switch transport {
	case "tcp":
		return tcpClientOnce(serverAddr, token)
	case "quic":
		return quicClientOnce(serverAddr, token)
	default:
		return fmt.Errorf("unknown transport: %s", transport)
	}
}

func tcpClientOnce(serverAddr string, token string) error {
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
		go handleClientStream(stream)
	}
}

func quicClientOnce(serverAddr string, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn, err := quic.DialAddr(ctx, serverAddr, clientTLSConfig(), quicConfig())
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
		go handleClientStream(&quicStreamConn{stream: stream, local: conn.LocalAddr(), remote: conn.RemoteAddr()})
	}
}

func handleClientStream(stream net.Conn) {
	defer stream.Close()
	reader := bufio.NewReader(stream)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "CONNECT ")
	if ok {
		handleConnectStream(stream, reader, target)
		return
	}
	fetchURL, ok := strings.CutPrefix(strings.TrimSpace(line), "FETCH ")
	if ok {
		handleFetchStream(stream, fetchURL)
		return
	}
	_, _ = io.WriteString(stream, "ERR invalid command\n")
}

func handleConnectStream(stream net.Conn, reader *bufio.Reader, target string) {
	if target == "" {
		_, _ = io.WriteString(stream, "ERR invalid command\n")
		return
	}
	targetConn, err := dialTarget(target)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "ERR %v\n", err)
		return
	}
	defer targetConn.Close()
	if _, err := io.WriteString(stream, "OK\n"); err != nil {
		return
	}
	pipeBoth(&bufferedConn{Conn: stream, reader: reader}, targetConn)
}

func handleFetchStream(stream net.Conn, encodedURL string) {
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
	req.Header.Set("User-Agent", "dxreverse-fetch/0")
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: fetchRootCAs()},
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			return dialTarget(addr)
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

func dialTarget(target string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		return net.DialTimeout("tcp", target, 15*time.Second)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resolver := publicResolver()
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	dialer := net.Dialer{Timeout: 15 * time.Second}
	for _, ip := range preferIPv4(ips) {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.IP.String(), port))
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
			conn, err := dialer.DialContext(ctx, "udp", "1.1.1.1:53")
			if err == nil {
				return conn, nil
			}
			return dialer.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
}

func preferIPv4(ips []net.IPAddr) []net.IPAddr {
	if len(ips) < 2 {
		return ips
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			out = append(out, ip)
		}
	}
	for _, ip := range ips {
		if ip.IP.To4() == nil {
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

func pipeBoth(a net.Conn, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
}

func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
		return
	}
	if quicConn, ok := conn.(*quicStreamConn); ok {
		_ = quicConn.Close()
		return
	}
	_ = conn.SetDeadline(time.Now())
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

func serverTLSConfig() *tls.Config {
	cert, err := selfSignedCert()
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{quicALPN},
		MinVersion:   tls.VersionTLS13,
	}
}

func clientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{quicALPN},
		MinVersion:         tls.VersionTLS13,
	}
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
		Subject:      pkix.Name{CommonName: "dxreverse"},
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
