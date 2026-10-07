package httpboundary

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListenerDefaults(t *testing.T) {
	s := NewServer("127.0.0.1:0", http.NotFoundHandler())
	if s.ReadHeaderTimeout != 5*time.Second || s.ReadTimeout != 10*time.Second || s.WriteTimeout != 30*time.Second || s.IdleTimeout != 30*time.Second || s.MaxHeaderBytes != 16<<10 || s.Handler == nil {
		t.Fatalf("unexpected listener boundary: %+v", s)
	}
}

func TestClientSourceBoundaries(t *testing.T) {
	tests := []struct {
		name, remote, xff, want string
		policy                  SourcePolicy
		bad                     bool
	}{
		{name: "public compat", remote: "198.51.100.30:1", xff: "203.0.113.2", want: "198.51.100.30"},
		{name: "private compat", remote: "10.66.0.30:1", xff: "203.0.113.2", want: "10.66.0.30"},
		{name: "loopback compat", remote: "127.0.0.1:1", xff: "203.0.113.2", want: "127.0.0.1"},
		{name: "IPv6 compat", remote: "[2001:db8::30]:1", xff: "203.0.113.2", want: "2001:db8::30"},
		{name: "invalid compat XFF ignored", remote: "10.66.0.30:1", xff: "not-ip", want: "10.66.0.30"},
		{name: "trusted Caddy", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "203.0.113.2", want: "203.0.113.2"},
		{name: "IPv6 Caddy", policy: LoopbackProxy, remote: "[::1]:1", xff: "2001:0db8::2", want: "2001:db8::2"},
		{name: "mapped Caddy", policy: LoopbackProxy, remote: "[::ffff:127.0.0.1]:1", xff: "::ffff:203.0.113.2", want: "203.0.113.2"},
		{name: "trusted with no header", policy: LoopbackProxy, remote: "127.0.0.1:1", want: "127.0.0.1"},
		{name: "other loopback not proxy", policy: LoopbackProxy, remote: "127.0.0.2:1", xff: "203.0.113.2", want: "127.0.0.2"},
		{name: "private not proxy", policy: LoopbackProxy, remote: "10.66.0.30:1", xff: "203.0.113.2", want: "10.66.0.30"},
		{name: "public not proxy", policy: LoopbackProxy, remote: "198.51.100.30:1", xff: "203.0.113.2", want: "198.51.100.30"},
		{name: "skip exact proxy hops", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "203.0.113.2, 127.0.0.1, ::1", want: "203.0.113.2"},
		{name: "stop at untrusted hop", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "203.0.113.2, 10.66.0.30, 127.0.0.1", want: "10.66.0.30"},
		{name: "all proxies", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "::1, 127.0.0.1", want: "::1"},
		{name: "invalid source", remote: "not-ip", bad: true},
		{name: "source zone", remote: "[fe80::1%zone]:1", bad: true},
		{name: "invalid XFF", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "not-ip", bad: true},
		{name: "invalid earlier hop", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "not-ip, 203.0.113.2", bad: true},
		{name: "XFF port", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "203.0.113.2:443", bad: true},
		{name: "XFF zone", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "fe80::1%zone", bad: true},
		{name: "empty hop", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: "203.0.113.2,", bad: true},
		{name: "too many hops", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: strings.Repeat("127.0.0.1,", MaxXFFHops) + "203.0.113.2", bad: true},
		{name: "too many bytes", policy: LoopbackProxy, remote: "127.0.0.1:1", xff: strings.Repeat(" ", MaxXFFBytes) + "203.0.113.2", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			got, err := ClientSource(r, tt.policy)
			if (err != nil) != tt.bad || got != tt.want {
				t.Fatalf("source = %q, err = %v; want %q, bad=%v", got, err, tt.want, tt.bad)
			}
		})
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Add("X-Forwarded-For", "203.0.113.2")
	r.Header.Add("X-Forwarded-For", "198.51.100.30")
	verified, err := RequestWithSource(r, LoopbackProxy)
	if err != nil || SourceFromContext(verified) != "198.51.100.30" {
		t.Fatalf("multiple header lines were not traversed as one chain: %v", err)
	}
	if SourceFromContext(r) != "" {
		t.Fatal("request source was not scoped to the verified context")
	}
}

func TestBoundedCompleteJSON(t *testing.T) {
	const object = `{"token":"synthetic","future_field":true}`
	for _, tt := range []struct {
		name, body string
		want       int
	}{
		{"unknown fields", object, 200},
		{"at cap", object + strings.Repeat(" ", MaxBodyBytes-len(object)), 200},
		{"one byte over cap", object + strings.Repeat(" ", MaxBodyBytes-len(object)+1), 413},
		{"large first value", `{"token":"` + strings.Repeat("x", MaxBodyBytes) + `"}`, 413},
		{"second object", object + `{}`, 400},
		{"second null", object + `null`, 400},
		{"garbage", object + `garbage`, 400},
		{"empty body", "", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			// An unknown/chunked length cannot bypass the actual reader limit.
			r.ContentLength = -1
			var v struct{ Token string }
			err := DecodeJSON(httptest.NewRecorder(), r, &v)
			status := 200
			if err != nil {
				status, _ = DecodeError(err)
			}
			if status != tt.want || (status == 200 && v.Token != "synthetic") {
				t.Fatalf("status = %d, token=%q, err=%v", status, v.Token, err)
			}
		})
	}
}

func TestLoopbackSocketBoundaries(t *testing.T) {
	const budget = 250 * time.Millisecond
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/body":
			var v struct{ Token string }
			if err := DecodeJSON(w, r, &v); err != nil {
				status, _ := DecodeError(err)
				w.WriteHeader(status)
				return
			}
		case "/write":
			// A write deadline closes the response, but does not cancel work.
			time.Sleep(3 * budget)
		}
		_, _ = fmt.Fprint(w, "ok")
	})
	s := NewServer("127.0.0.1:0", handler)
	s.ReadHeaderTimeout, s.ReadTimeout = budget, budget
	s.WriteTimeout, s.IdleTimeout = budget, budget
	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	t.Cleanup(func() {
		_ = s.Close()
		<-done
	})
	dial := func() net.Conn {
		t.Helper()
		c, err := net.DialTimeout("tcp", l.Addr().String(), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(4 * time.Second))
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	for _, tt := range []struct{ name, request string }{
		{"slow header", "GET / HTTP/1.1\r\nHost:"},
		{"slow body", "POST /body HTTP/1.1\r\nHost: localhost\r\nContent-Length: 20\r\n\r\n{"},
		{"expired write", "GET /write HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := dial()
			if _, err := io.WriteString(c, tt.request); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err == nil {
				defer response.Body.Close()
				if response.StatusCode == 200 {
					t.Fatal("incomplete/expired request completed successfully")
				}
			} else if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				t.Fatal("listener failed to terminate before the client deadline")
			}
			_ = c.Close()
		})
	}
	t.Run("idle connection", func(t *testing.T) {
		c := dial()
		reader := bufio.NewReader(c)
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
		response, err := http.ReadResponse(reader, nil)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("initial response: %v", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		// Reading the next response must observe the server's idle close.
		if _, err := http.ReadResponse(reader, nil); err == nil {
			t.Fatal("idle socket remained open")
		} else if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			t.Fatal("idle timeout was not enforced")
		}
		_ = c.Close()
	})
	t.Run("large header", func(t *testing.T) {
		c := dial()
		_, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\nX-Large: "+strings.Repeat("x", 3*MaxHeaderBytes)+"\r\nConnection: close\r\n\r\n")
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("large header status = %d", response.StatusCode)
		}
		_ = c.Close()
	})
	t.Run("listener still healthy", func(t *testing.T) {
		c := dial()
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		response, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("healthy response: %v", err)
		}
		defer response.Body.Close()
		_ = c.Close()
	})
}
