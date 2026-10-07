package deviceapi

import (
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
)

type Config struct{ ListenAddr, DBPath, PolicyPath, WGExecutable, SupervisorExecutable, WGInterface, TLSCert, TLSKey string }
type Service struct {
	DB        *sql.DB
	Store     *deviceauth.Store
	Scheduler *deviceauth.Scheduler
	HTTP      *http.Server
	config    Config
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
	policy, err := ReadPolicy(c.PolicyPath)
	if err != nil {
		return nil, err
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
	handler, err := NewServer(store)
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
	return &Service{DB: db, Store: store, Scheduler: scheduler, HTTP: server, config: c}, nil
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
func (s *Service) Run(ctx context.Context) error {
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
func (s *Service) Close() error { return s.DB.Close() }
