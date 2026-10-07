package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
	"zongheng-vpn/hub/internal/httpboundary"
	"zongheng-vpn/hub/internal/processbudget"
)

func TestAdminAndClientIngressShareExpensiveWorkAdmission(t *testing.T) {
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{}}
	client := auth.NewServer(tokens)
	s, err := NewServer(Config{DBPath: filepath.Join(t.TempDir(), "admin.sqlite"), MaintenanceInterval: -1}, tokens, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.httpAdmission != client.HTTPAdmission() {
		t.Fatal("admin allocated an independent client budget")
	}

	// Hold the same shared object with owned synthetic handlers. Rejections
	// below run the actual client/admin routing, before any auth or mutation.
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var wait sync.WaitGroup
	hold := s.httpAdmission.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), httpboundary.Direct)
	for i := 1; i <= 8; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			r := httptest.NewRequest(http.MethodPost, "/owned-hold", nil)
			r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1", i)
			hold.ServeHTTP(httptest.NewRecorder(), r)
		}(i)
	}
	defer func() { close(release); wait.Wait() }()
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("shared budget did not admit owned hold")
		}
	}

	for _, tt := range []struct {
		name, method, path string
		handler            http.Handler
	}{
		{"compat bootstrap", http.MethodPost, "/api/client/bootstrap", client.BootstrapHandler(auth.ClientIngressCompat)},
		{"trusted rotate", http.MethodPost, "/api/client/rotate-ip", client.RotateIPHandler(auth.ClientIngressTrustedProxy)},
		{"admin login", http.MethodPost, "/admin/api/auth/login", s},
		{"admin rotate", http.MethodPost, "/admin/api/egress/synthetic/rotate-ip", s},
		{"admin external probe", http.MethodGet, "/admin/api/egress/synthetic/exit-ip", s},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"token":"synthetic-private-marker"}`))
			r.RemoteAddr = "127.0.0.1:99"
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
			w := httptest.NewRecorder()
			tt.handler.ServeHTTP(w, r)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"error":"resource_exhausted"`) || w.Header().Get("Retry-After") == "" || strings.Contains(w.Body.String(), "synthetic-private-marker") {
				t.Fatalf("shared rejection failed: status=%d body=%q", w.Code, w.Body.String())
			}
		})
	}
	for _, path := range []string{"/admin/api/health", "/admin/api/auth/me", "/admin/api/overview"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "127.0.0.1:99"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code == http.StatusServiceUnavailable || w.Code == http.StatusTooManyRequests {
			t.Fatalf("snapshot/health starved: %s", path)
		}
	}
}

func TestAdminSelectedAdmissionUsesVerifiedSourceAndRefusesMissingBudget(t *testing.T) {
	budget, err := httpboundary.NewAdmission(httpboundary.DefaultAdmissionConfig())
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		called++
		if requestIP(r) != "198.51.100.20" {
			t.Errorf("unverified source %q", requestIP(r))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s := &Server{mux: mux, httpAdmission: budget}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		path := "/admin/api/auth/login"
		if method == http.MethodGet {
			path = "/admin/api/egress/synthetic/exit-ip"
		}
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("X-Forwarded-For", "203.0.113.40, 198.51.100.20")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status %d", w.Code)
		}
	}
	if called != 2 {
		t.Fatal("selected handler not called")
	}
	s.httpAdmission = nil
	r := httptest.NewRequest(http.MethodPost, "/admin/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || called != 2 {
		t.Fatal("missing budget allowed expensive work")
	}
}

func TestAdminUnknownRotateProjectionSurvivesFreshSnapshot(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"session_count":0,"active_proxy_connections":0}`))
	}))
	defer health.Close()
	node := auth.Egress{Name: "jp-android-01", DisplayName: "synthetic-egress", Type: "android-reverse"}
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"synthetic": {Enabled: true, Egress: node}}}
	client := auth.NewServer(tokens)
	calls := 0
	client.SetRotateTrigger(func(context.Context, string, int) error {
		calls++
		return &processbudget.Failure{Kind: processbudget.Failed, Started: true}
	})
	if _, err := client.RotateEgress(context.Background(), node, 8); err == nil {
		t.Fatal("synthetic dispatch failure not returned")
	}
	locks := client.RotateLocksSnapshot(time.Now().Add(365 * 24 * time.Hour))
	if len(locks) != 1 || !locks[0].Unknown {
		t.Fatal("unknown fell out of snapshot after cooldown")
	}
	if _, err := client.RotateEgress(context.Background(), node, 8); err != auth.ErrRotateBusy || calls != 1 {
		t.Fatal("unknown allowed redispatch")
	}
	s, err := NewServer(Config{DBPath: filepath.Join(t.TempDir(), "admin.sqlite"), MaintenanceInterval: -1, ReverseHealthURL: health.URL}, tokens, client)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 2; i++ {
		rows := s.egressSummaries(context.Background())
		if len(rows) != 1 || rows[0].RotateState == nil || *rows[0].RotateState != generated.EgressSummaryRotateStateUnknown {
			t.Fatal("fresh read concealed unknown")
		}
		body, err := json.Marshal(generated.EgressResponse{Egress: rows})
		if err != nil || !strings.Contains(string(body), `"rotate_state":"unknown"`) {
			t.Fatal("wire projection concealed unknown")
		}
	}
}
