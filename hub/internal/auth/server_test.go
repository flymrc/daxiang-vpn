package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestClaimTokenLease(t *testing.T) {
	server := &Server{
		tokenLeases:   map[string]tokenLease{},
		tokenLeaseTTL: 30 * time.Second,
	}
	now := time.Unix(1000, 0)

	if !server.claimToken("token-a", "198.51.100.10", now) {
		t.Fatal("initial claim rejected")
	}
	if !server.claimToken("token-a", "198.51.100.10", now.Add(5*time.Second)) {
		t.Fatal("same source refresh rejected")
	}
	if server.claimToken("token-a", "203.0.113.20", now.Add(10*time.Second)) {
		t.Fatal("different source within lease accepted")
	}
	if !server.claimToken("token-a", "203.0.113.20", now.Add(36*time.Second)) {
		t.Fatal("different source after lease rejected")
	}
}

func TestClaimTokenLeaseDisabled(t *testing.T) {
	server := &Server{
		tokenLeases:   map[string]tokenLease{},
		tokenLeaseTTL: 0,
	}

	if !server.claimToken("token-a", "198.51.100.10", time.Unix(1000, 0)) {
		t.Fatal("initial claim rejected")
	}
	if !server.claimToken("token-a", "203.0.113.20", time.Unix(1001, 0)) {
		t.Fatal("different source rejected with disabled lease")
	}
}

func TestClientIPIgnoresSpoofedForwardedForFromPublicClient(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "198.51.100.10:54321",
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.20"}},
	}

	if got := clientIP(req); got != "198.51.100.10" {
		t.Fatalf("clientIP() = %q, want tcp source", got)
	}
}

func TestClientIPTrustsForwardedForFromLocalProxy(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "127.0.0.1:54321",
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.20, 10.0.0.1"}},
	}

	if got := clientIP(req); got != "203.0.113.20" {
		t.Fatalf("clientIP() = %q, want forwarded source", got)
	}
}
