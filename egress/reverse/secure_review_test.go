package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type secureObservedWriteConn struct {
	net.Conn
	entered chan struct{}
	once    sync.Once
}

func (c *secureObservedWriteConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	return c.Conn.Write(p)
}

func TestSecureRelayCancellationClosesBlockedTargetWrite(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "initial-target"
		if replacement {
			name = "replay-replacement-target"
		}
		t.Run(name, func(t *testing.T) {
			client, sender := net.Pipe()
			target, receiver := net.Pipe()
			defer client.Close()
			defer sender.Close()
			defer target.Close()
			defer receiver.Close()
			blocked := &secureObservedWriteConn{Conn: target, entered: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			redial := func() (net.Conn, error) {
				t.Error("non-TLS payload must not retry a target")
				return nil, net.ErrClosed
			}
			payload := []byte("non-TLS payload")
			if replacement {
				newTarget, newReceiver := net.Pipe()
				defer newTarget.Close()
				defer newReceiver.Close()
				blocked = &secureObservedWriteConn{Conn: newTarget, entered: make(chan struct{})}
				redial = func() (net.Conn, error) { return blocked, nil }
				payload = []byte{0x16, 0x03, 0x01, 0, 1, 0x01}
			}
			initial := net.Conn(blocked)
			if replacement {
				initial = target
			}
			go func() {
				defer close(done)
				relayWithHandshakeRetryContext(ctx, client, initial, "synthetic", redial)
			}()
			go func() { _, _ = sender.Write(payload) }()
			if replacement {
				if _, err := io.ReadFull(receiver, make([]byte, len(payload))); err != nil {
					t.Fatal(err)
				}
				// A reset before the first target response enters the replay write.
				_ = receiver.Close()
			}
			select {
			case <-blocked.entered:
			case <-time.After(time.Second):
				t.Fatal("target write did not enter")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancellation did not release non-reading target")
			}
		})
	}
}

func TestSecureRegistryRejectsNonCanonicalFieldNames(t *testing.T) {
	for _, data := range []string{
		`{"schema_version":1,"generation":99,"Generation":1,"hub_id":"test-hub","egresses":[]}`,
		`{"schema_version":1,"Generation":1,"hub_id":"test-hub","egresses":[]}`,
		`{"schema_version":1,"generation":1,"hub_id":"test-hub","egresses":[{"Egress_id":"phone","state":"active","credentials":[]}]}`,
		`{"schema_version":1,"generation":1,"hub_id":"test-hub","egresses":[{"egress_id":"phone","state":"active","credentials":[{"Credential_id":"key"}]}]}`,
	} {
		var p secureRegistry
		if err := decodeSecureJSON([]byte(data), &p); err == nil {
			encoded, _ := json.Marshal(p)
			t.Fatalf("non-canonical field accepted as authority: %s", encoded)
		}
	}
	for _, data := range []string{
		`{"schema_version":1,"generation":99,"Generation":1,"sha256":"a"}`,
		`{"schema_version":1,"generation":1,"SHA256":"a"}`,
	} {
		var p securePolicyWatermark
		if err := decodeSecureJSON([]byte(data), &p); err == nil {
			t.Fatal("non-canonical durable watermark field accepted")
		}
	}
}

func TestSecureTCPRejectsTLS12AndALPNMismatch(t *testing.T) {
	for _, scenario := range []string{"TLS-1.2", "missing-ALPN", "wrong-ALPN"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSecureTestFixture(t)
			s, m, addr := startSecureTestServer(t, f)
			config, _, err := secureClientTLSConfig(f.clientOptions)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "TLS-1.2":
				config.MinVersion, config.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			case "missing-ALPN":
				config.NextProtos = nil
			case "wrong-ALPN":
				config.NextProtos = []string{"zhreverse/1"}
			}
			// Remove the client callback to ensure the server enforces its own
			// protocol rule instead of relying on the cooperative client check.
			config.VerifyConnection = nil
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, config)
			if err == nil {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				_, _ = io.WriteString(conn, secureTCPHello)
				if ack, err := readLineBytewise(conn, 64); err == nil && ack == secureTCPAck {
					t.Fatal("server admitted an incompatible TLS/ALPN session")
				}
			}
			waitSecureCondition(t, time.Second, "server-side handshake rejection", func() bool { return s.health().RejectedHandshakes > 0 })
			if m.sessionCount() != 0 {
				t.Fatal("incompatible handshake reached scheduler")
			}
		})
	}
}
