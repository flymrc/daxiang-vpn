package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zongheng-vpn/hub/internal/auth"
	"zongheng-vpn/hub/internal/httpboundary"
)

func TestAdminListenerHasFixedValidatedSourcePolicy(t *testing.T) {
	for _, tt := range []struct {
		name, remote, xff, want string
		status                  int
	}{
		{"Caddy", "127.0.0.1:1", "203.0.113.20", "203.0.113.20", 200},
		{"IPv6 Caddy", "[::1]:1", "2001:db8::20", "2001:db8::20", 200},
		{"private direct", "10.66.0.30:1", "203.0.113.20", "10.66.0.30", 200},
		{"other loopback direct", "127.0.0.2:1", "203.0.113.20", "127.0.0.2", 200},
		{"public direct", "198.51.100.30:1", "203.0.113.20", "198.51.100.30", 200},
		{"spoofed left hop", "127.0.0.1:1", "203.0.113.20, 198.51.100.30", "198.51.100.30", 200},
		{"invalid proxy header", "127.0.0.1:1", "not-an-ip", "", 400},
		{"oversized proxy header", "127.0.0.1:1", strings.Repeat(" ", httpboundary.MaxXFFBytes+1), "", 400},
		{"invalid source", "not-an-ip", "203.0.113.20", "", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var got string
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				calls++
				got = requestIP(r)
				w.WriteHeader(http.StatusOK)
			})
			s := &Server{mux: mux}
			r := httptest.NewRequest(http.MethodGet, "/admin/api/health", nil)
			r.RemoteAddr = tt.remote
			r.Header.Set("X-Forwarded-For", tt.xff)
			// A client header cannot change the listener's fixed policy.
			r.Header.Set("X-ZHVPN-Ingress", "compat")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tt.status || got != tt.want || (tt.status == 400 && calls != 0) {
				t.Fatalf("status=%d source=%q calls=%d", w.Code, got, calls)
			}
		})
	}
}

func TestAdminJSONRejectionPrecedesAuthenticationAndRotate(t *testing.T) {
	for _, endpoint := range []string{"login", "rotate"} {
		for _, tt := range []struct {
			name, body string
			want       int
		}{
			{"oversized", `{"extra":"` + strings.Repeat("x", httpboundary.MaxBodyBytes) + `"}`, 413},
			{"trailing whitespace over cap", `{}` + strings.Repeat(" ", httpboundary.MaxBodyBytes), 413},
			{"second object", `{}{}`, 400},
			{"trailing garbage", `{}not-json`, 400},
		} {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				rotations := 0
				a := auth.NewServer(&auth.TokenStore{Tokens: map[string]auth.TokenRecord{
					"synthetic": {Enabled: true, Egress: auth.Egress{Name: "jp-android-01"}},
				}})
				a.SetRotateTrigger(func(context.Context, string, int) error { rotations++; return nil })
				// A nil DB makes any unintended authentication/audit execution fail
				// the test rather than silently accepting work after bad input.
				s := &Server{clientAuth: a, tokens: &auth.TokenStore{}}
				r := httptest.NewRequest(http.MethodPost, "/admin/api/auth/login", strings.NewReader(tt.body))
				r.ContentLength = -1
				w := httptest.NewRecorder()
				if endpoint == "login" {
					s.handleLogin(w, r)
				} else {
					r.URL.Path = "/admin/api/egress/jp-android-01/rotate-ip"
					s.handleRotateIP(w, r, sessionContext{})
				}
				if w.Code != tt.want || rotations != 0 {
					t.Fatalf("status=%d rotations=%d", w.Code, rotations)
				}
			})
		}
	}
}
