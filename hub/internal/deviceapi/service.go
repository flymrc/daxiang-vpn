package deviceapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"zongheng-vpn/hub/internal/deviceauth"
	"zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/proxygate"
)

type Config struct{ ListenAddr, DBPath, PolicyPath, WGExecutable, SupervisorExecutable, WGInterface, TLSCert, TLSKey, ProxyProfilePath, ProxyGatePolicyPath, ProxyGateSocketPath string }
type Service struct {
	DB         *sql.DB
	Store      *deviceauth.Store
	Scheduler  *deviceauth.Scheduler
	HTTP       *http.Server
	config     Config
	gatePolicy *proxygate.Policy
	gateDial   func(context.Context, string, proxygate.Policy) (proxyController, error)
}

// Open is explicit hosting: it never initializes production merely by importing
// this package. The private Unix service directory must already be installed.
func Open(ctx context.Context, c Config) (*Service, error) {
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fmt.Errorf("device authority listener requires a loopback IP literal")
	}
	if !filepath.IsAbs(c.DBPath) || c.PolicyPath == "" || c.TLSCert == "" || c.TLSKey == "" {
		return nil, deviceauth.ErrInvalid
	}
	if err = privateHostingDirectory(filepath.Dir(c.DBPath)); err != nil {
		return nil, err
	}
	var policy deviceauth.Policy
	if c.ProxyProfilePath != "" {
		// Profile-enabled hosting binds the protected source, rather than a
		// mutable local/offline policy pathname. No source ACL repair is done.
		var raw []byte
		raw, err = readProtectedSource(c.PolicyPath, 65536)
		if err == nil {
			policy, err = decodePolicy(raw)
		}
	} else {
		policy, err = ReadPolicy(c.PolicyPath)
	}
	if err != nil {
		return nil, err
	}
	var profile *devicecontract.ProxyRouteProfile
	var gatePolicy *proxygate.Policy
	if (c.ProxyProfilePath == "") != (c.ProxyGatePolicyPath == "" && c.ProxyGateSocketPath == "") {
		return nil, deviceauth.ErrPolicy
	}
	if c.ProxyProfilePath != "" {
		p, err := ReadRouteProfile(c.ProxyProfilePath)
		if err != nil || !profileMatchesHosting(p, policy, c.WGInterface) {
			return nil, deviceauth.ErrPolicy
		}
		profile = &p
		if c.ProxyGatePolicyPath == "" || c.ProxyGateSocketPath == "" {
			return nil, deviceauth.ErrPolicy
		}
		gate, err := proxygate.LoadPolicy(c.ProxyGatePolicyPath)
		if err != nil || gate == nil || ValidateProxyGatePolicy(policy, p, c.WGInterface, *gate) != nil {
			return nil, deviceauth.ErrPolicy
		}
		gatePolicy = gate
	}
	adapter, err := deviceauth.NewSupervisedWGExecutor(c.WGExecutable, c.SupervisorExecutable, c.WGInterface, policy, 5*time.Second)
	if err != nil {
		return nil, err
	}
	// Load certificate eagerly; TLS files never appear in errors/logs or responses.
	pair, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
	if err != nil {
		return nil, errors.New("device authority TLS material invalid")
	}
	// Refused hosting must not leave an initialized-looking authority database.
	// Linux requires the explicit supervisor that retains the execution fence
	// through a parent crash and child-tree reap.
	file, err := os.OpenFile(c.DBPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		err = file.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", c.DBPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	store, err := deviceauth.New(ctx, db, deviceauth.Options{Policy: policy})
	if err != nil {
		db.Close()
		return nil, err
	}
	var handler *Server
	if profile == nil {
		handler, err = NewServer(store)
	} else {
		handler, err = NewServerWithProfile(store, *profile, c.WGInterface)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	scheduler, err := deviceauth.NewScheduler(store, adapter)
	if err != nil {
		db.Close()
		return nil, err
	}
	server := &http.Server{Addr: c.ListenAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}}
	return &Service{DB: db, Store: store, Scheduler: scheduler, HTTP: server, config: c, gatePolicy: gatePolicy, gateDial: func(ctx context.Context, path string, p proxygate.Policy) (proxyController, error) {
		return proxygate.DialControl(ctx, path, p)
	}}, nil
}
func ReadPolicy(path string) (deviceauth.Policy, error) {
	var p deviceauth.Policy
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return p, deviceauth.ErrInvalid
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, deviceauth.ErrInvalid
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return p, deviceauth.ErrInvalid
	}
	return p, nil
}

func decodePolicy(raw []byte) (deviceauth.Policy, error) {
	var p deviceauth.Policy
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return p, deviceauth.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return p, deviceauth.ErrInvalid
	}
	canonical, _ := json.Marshal(p)
	if !bytes.Equal(raw, canonical) {
		return p, deviceauth.ErrInvalid
	}
	return p, nil
}
func (s *Service) Run(ctx context.Context) error {
	if s.config.ProxyProfilePath != "" {
		return s.runGated(ctx)
	}
	schedulerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Scheduler.Run(schedulerCtx, time.Second) }()
	result := make(chan error, 1)
	go func() { result <- s.HTTP.ListenAndServeTLS("", "") }()
	var err error
	select {
	case err = <-result:
	case <-ctx.Done():
		err = ctx.Err()
	}
	cancel()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 6*time.Second)
	_ = s.HTTP.Shutdown(shutdown)
	cancelShutdown()
	<-done
	return err
}

func (s *Service) runGated(ctx context.Context) error {
	if s.gatePolicy == nil || s.gateDial == nil {
		return errors.New("device authority proxy gate configuration required")
	}
	control, err := s.gateDial(ctx, s.config.ProxyGateSocketPath, s.gatePolicy.Clone())
	if err != nil {
		return errors.New("device authority proxy gate closed acknowledgement failed")
	}
	defer control.Close()
	if control.Policy().SHA256() != s.gatePolicy.SHA256() {
		return errors.New("device authority proxy gate policy acknowledgement failed")
	}
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = control.Close()
		case <-watchDone:
		}
	}()
	initial, cancelInitial := context.WithTimeout(ctx, 30*time.Second)
	err = s.Scheduler.Tick(initial)
	cancelled := initial.Err()
	cancelInitial()
	if err != nil || cancelled != nil || ctx.Err() != nil {
		return errors.New("device authority initial reconciliation failed")
	}
	initialProof, cancelProof := context.WithTimeout(ctx, 5*time.Second)
	err = s.grantProxy(initialProof, control)
	cancelProof()
	if err != nil || ctx.Err() != nil {
		return errors.New("device authority initial proxy convergence failed")
	}
	live, cancel := context.WithCancel(ctx)
	defer cancel()
	cycleResult := make(chan error, 1)
	cycleDone := make(chan struct{})
	go func() { defer close(cycleDone); cycleResult <- s.runGatedCycles(live, control) }()
	httpResult := make(chan error, 1)
	httpDone := make(chan struct{})
	go func() { defer close(httpDone); httpResult <- s.HTTP.ListenAndServeTLS("", "") }()
	select {
	case err = <-httpResult:
	case err = <-cycleResult:
	case <-ctx.Done():
		err = ctx.Err()
	}
	cancel()
	_ = control.Close()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 6*time.Second)
	_ = s.HTTP.Shutdown(shutdown)
	cancelShutdown()
	// Each cycle and subprocess is cancellation bounded. Control EOF already
	// closes admissions while the external executor reaps its owned children.
	<-cycleDone
	<-httpDone
	return err
}

func (s *Service) runGatedCycles(ctx context.Context, control proxyController) error {
	for {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		cycle, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.Scheduler.Tick(cycle)
		if err == nil && cycle.Err() == nil {
			err = s.grantProxy(cycle, control)
		}
		if err == nil && cycle.Err() != nil {
			err = cycle.Err()
		}
		cancel()
		if err != nil || ctx.Err() != nil {
			_ = control.Close()
			return errors.New("device authority proxy convergence failed")
		}
	}
}
func (s *Service) Close() error { return s.DB.Close() }
