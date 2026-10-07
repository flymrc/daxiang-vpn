package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func observerServer() *Server {
	return NewServer(&TokenStore{Tokens: map[string]TokenRecord{"owned-token": {Enabled: true}}})
}
func TestObserverWriterGateActualHTTPRejectsWithoutWaitingOrMutation(t *testing.T) {
	s := observerServer()
	var applied atomic.Int32
	s.applyClientPeer = func(context.Context, string, string) error { applied.Add(1); return nil }
	server := httptest.NewServer(s.BootstrapHandler(ClientIngressCompat))
	defer server.Close()
	s.auditGate.Lock()
	defer s.auditGate.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{"token":"owned-token"}`))
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal("gate waited until HTTP deadline")
	}
	defer res.Body.Close()
	if res.StatusCode != 503 || applied.Load() != 0 || len(s.tokenLeases) != 0 {
		t.Fatal("closed capture queued or mutated request")
	}
}
func TestObserverOwnerSwapDrainsCallbackAndStaleCloseKeepsSuccessor(t *testing.T) {
	s := observerServer()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	detached := make(chan string, 1)
	old := s.RegisterAuditSink(func(AuditEvent) { once.Do(func() { close(entered) }); <-release }, func(reason string) { detached <- reason })
	done := make(chan struct{})
	go func() { s.audit(AuditEvent{EventType: "owned-fixture"}); close(done) }()
	<-entered
	attached := make(chan func() bool, 1)
	var current atomic.Int32
	go func() { attached <- s.RegisterAuditSink(func(AuditEvent) { current.Add(1) }, nil) }()
	select {
	case <-attached:
		t.Fatal("successor attached before prior callback drained")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-done
	next := <-attached
	if reason := <-detached; reason != "sink_replaced" {
		t.Fatal("prior owner replaced without gap")
	}
	if old() {
		t.Fatal("stale owner detached successor")
	}
	s.audit(AuditEvent{})
	if current.Load() != 1 {
		t.Fatal("stale close cleared successor")
	}
	if !next() {
		t.Fatal("current owner did not close")
	}
	req := httptest.NewRequest("POST", "http://owned/bootstrap", strings.NewReader(`{"token":"owned-token"}`))
	req.RemoteAddr = "192.0.2.40:1234"
	out := httptest.NewRecorder()
	s.BootstrapHandler(ClientIngressCompat)(out, req)
	if out.Code != 503 || len(s.tokenLeases) != 0 {
		t.Fatal("clean detached observer accepted later mutation")
	}
}

type cancellationBody struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (b *cancellationBody) Read(p []byte) (int, error) {
	n, e := b.reader.Read(p)
	b.cancel()
	return n, e
}
func (b *cancellationBody) Close() error { return nil }
func TestObserverKnownTokenCancellationRecordsNegativeBeforeClaim(t *testing.T) {
	s := observerServer()
	var facts []AuditEvent
	s.SetAuditSink(func(e AuditEvent) {
		if e.MigrationObservation != nil {
			facts = append(facts, e)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "http://owned/bootstrap", nil).WithContext(ctx)
	req.Body = &cancellationBody{reader: strings.NewReader(`{"token":"owned-token"}`), cancel: cancel}
	req.RemoteAddr = "192.0.2.40:1234"
	out := httptest.NewRecorder()
	s.BootstrapHandler(ClientIngressCompat)(out, req)
	if out.Code != 503 || len(s.tokenLeases) != 0 || len(facts) != 1 || facts[0].Result != "error" || facts[0].MigrationObservation.TokenID != TokenID("owned-token") {
		t.Fatal("known token cancellation omitted negative fact or consumed lease")
	}
}
func TestMetadataStrictSemverNeverApprovesSelfReportedBuild(t *testing.T) {
	for _, v := range []string{"not-a-semver", "unsigned-build", "DEV", "v1.2.3", " 1.2.3", "1.2.3 ", "01.2.3", "1.2.3-dev", "1.2.3+DEV", "1.2.3-01", "1.2.3-rc..1", "1.2.3\n", "SECRET_CANARY"} {
		if validReleaseVersion(v) {
			t.Fatal("invalid/development version accepted")
		}
	}
	for _, v := range []string{"0.0.0", "1.2.3", "1.2.3-rc.1", "1.2.3+build.7"} {
		if !validReleaseVersion(v) {
			t.Fatal("valid semver rejected")
		}
	}
}
