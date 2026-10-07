package auth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/httpboundary"
	"zongheng-vpn/hub/internal/processbudget"
)

func TestMain(m *testing.M) {
	if code, handled := processbudget.SupervisorMain(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "set" && os.Getenv("ZHVPN_AUTH_NATIVE_MODE") != "" {
		if marker := os.Getenv("ZHVPN_AUTH_NATIVE_MARKER"); marker != "" {
			_ = os.WriteFile(marker, []byte("owned native fixture started"), 0600)
		}
		switch os.Getenv("ZHVPN_AUTH_NATIVE_MODE") {
		case "fail":
			_, _ = os.Stderr.WriteString("SYNTHETIC_SECRET_NATIVE_STDERR")
			os.Exit(7)
		case "sleep":
			time.Sleep(20 * time.Second)
		case "flood":
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("SYNTHETIC_SECRET_OUTPUT"), 4096))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func nativeBootstrap(t *testing.T, s *Server, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	body := `{"token":"ZH-OK","wireguard_public_key":"` + key + `"}`
	r := httptest.NewRequest("POST", "/api/client/bootstrap", strings.NewReader(body)).WithContext(ctx)
	w := httptest.NewRecorder()
	s.BootstrapHandler(ClientIngressCompat)(w, r)
	return w
}
func TestBootstrapPreStartFailureRestoresLeaseCAS(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "expired"}[prior], func(t *testing.T) {
			s := testBootstrapServer()
			var before tokenLease
			if prior {
				s.claimToken("ZH-OK", "older-source", time.Now().Add(-time.Hour))
				before = s.tokenLeases["ZH-OK"]
			}
			t.Setenv("ZHHUB_WG_BIN", filepath.Join(t.TempDir(), "missing-owned-fixture"))
			w := nativeBootstrap(t, s, context.Background())
			if w.Code != 502 {
				t.Fatalf("pre-start status=%d", w.Code)
			}
			current, exists := s.tokenLeases["ZH-OK"]
			if exists != prior || (prior && !reflect.DeepEqual(current, before)) {
				t.Fatal("pre-start refusal polluted prior lease")
			}
		})
	}
	s := testBootstrapServer()
	_, first := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
	_, later := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
	s.rollbackTokenClaim(first)
	if s.tokenLeases["ZH-OK"].version != later.current.version {
		t.Fatal("compensation overwrote later claim")
	}
}
func TestWireGuardInvalidConfiguredAddressIsBeforeStart(t *testing.T) {
	s := testBootstrapServer()
	err := s.applyWireGuardPeer(context.Background(), "synthetic-public-key", "SYNTHETIC_SECRET_INVALID_ADDRESS")
	if !processbudget.BeforeStart(err) || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
		t.Fatal("invalid configured address did not return private pre-start rejection")
	}
}
func TestTokenClaimRejectedPredecessorsNeverReturn(t *testing.T) {
	for _, order := range [][]int{{0, 1}, {1, 0}, {0, 2, 1}, {1, 0, 2}, {2, 1, 0}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			s := testBootstrapServer()
			claims := make([]tokenClaim, len(order))
			for i := range claims {
				ok, claim := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
				if !ok {
					t.Fatal("same source reservation rejected")
				}
				claims[i] = claim
			}
			for _, i := range order {
				s.rollbackTokenClaim(claims[i])
			}
			if _, exists := s.tokenLeases["ZH-OK"]; exists {
				t.Fatal("rejected predecessor resurrected")
			}
		})
	}
}
func TestTokenClaimCommittedFactSurvivesOtherRejection(t *testing.T) {
	for _, committed := range []int{0, 1} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			s := testBootstrapServer()
			_, first := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
			_, second := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
			claims := []tokenClaim{first, second}
			s.commitTokenClaim(claims[committed])
			s.rollbackTokenClaim(claims[1-committed])
			current, exists := s.tokenLeases["ZH-OK"]
			if !exists || current.version != claims[committed].current.version || current.node != nil {
				t.Fatal("compensation lost committed fact or retained pending history")
			}
		})
	}
	s := testBootstrapServer()
	s.claimToken("ZH-OK", "expired-source", time.Now().Add(-time.Hour))
	prior := s.tokenLeases["ZH-OK"]
	_, first := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
	_, second := s.claimTokenReceipt("ZH-OK", "same-source", time.Now())
	s.rollbackTokenClaim(first)
	s.rollbackTokenClaim(second)
	if !reflect.DeepEqual(s.tokenLeases["ZH-OK"], prior) {
		t.Fatal("rejections did not restore exact prior expired fact")
	}
}
func TestLegacyDurationConfigurationBoundsAndPrivacy(t *testing.T) {
	for _, config := range []struct {
		key      string
		get      func() time.Duration
		fallback time.Duration
	}{
		{"ZHHUB_TOKEN_LEASE_SECONDS", tokenLeaseTTLFromEnv, 30 * time.Second},
		{"ZHHUB_ROTATE_LOCK_EXTRA_SECONDS", rotateLockExtraFromEnv, 45 * time.Second},
		{"ZHHUB_ANDROID_CARRIER_CACHE_SECONDS", carrierCacheTTLFromEnv, 5 * time.Minute},
	} {
		t.Run(config.key, func(t *testing.T) {
			for _, input := range []struct {
				value string
				want  time.Duration
			}{
				{"", config.fallback}, {"0", 0}, {"1", time.Second}, {"86400", 24 * time.Hour},
				{"86401", config.fallback}, {"9223372036", config.fallback}, {"9223372037", config.fallback},
				{"9223372036854775807", config.fallback}, {"9223372036854775808", config.fallback},
				{"-1", config.fallback}, {"SYNTHETIC_SECRET_CONFIG", config.fallback},
			} {
				t.Setenv(config.key, input.value)
				var output bytes.Buffer
				old := log.Writer()
				log.SetOutput(&output)
				got := config.get()
				log.SetOutput(old)
				if got != input.want {
					t.Fatalf("duration got %v want %v", got, input.want)
				}
				if strings.Contains(output.String(), "SYNTHETIC_SECRET") {
					t.Fatal("invalid config leaked")
				}
			}
		})
	}
}
func TestBootstrapPreCancelledHasNoLeaseOrNativeLaunch(t *testing.T) {
	s := testBootstrapServer()
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("ZHHUB_WG_BIN", os.Args[0])
	t.Setenv("ZHVPN_AUTH_NATIVE_MODE", "fail")
	t.Setenv("ZHVPN_AUTH_NATIVE_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := nativeBootstrap(t, s, ctx)
	if w.Code != 503 || len(s.tokenLeases) != 0 {
		t.Fatal("pre-cancelled bootstrap changed state")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("pre-cancelled request spawned helper")
	}
}
func TestBootstrapRealNativeFailuresDoNotLeak(t *testing.T) {
	for _, mode := range []string{"fail", "flood", "sleep"} {
		t.Run(mode, func(t *testing.T) {
			s := testBootstrapServer()
			marker := filepath.Join(t.TempDir(), "started")
			t.Setenv("ZHHUB_WG_BIN", os.Args[0])
			t.Setenv("ZHVPN_AUTH_NATIVE_MODE", mode)
			t.Setenv("ZHVPN_AUTH_NATIVE_MARKER", marker)
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			var events []AuditEvent
			s.SetAuditSink(func(e AuditEvent) { events = append(events, e) })
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			started := time.Now()
			w := nativeBootstrap(t, s, ctx)
			if w.Code != 502 || time.Since(started) > 3*time.Second {
				t.Fatal("native failure had wrong status or exceeded owned cleanup budget")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("native child was not actually exercised")
			}
			encoded, _ := json.Marshal(events)
			for _, value := range []string{w.Body.String(), logs.String(), string(encoded)} {
				if strings.Contains(value, "SYNTHETIC_SECRET") {
					t.Fatal("native output leaked")
				}
			}
			if _, ok := s.tokenLeases["ZH-OK"]; !ok {
				t.Fatal("started uncertain effect was compensated as not started")
			}
		})
	}
}
func TestRotateStartedFailureStaysUnknownAfterCooldown(t *testing.T) {
	for _, err := range []error{&processbudget.Failure{Kind: processbudget.Failed, Started: true}, &processbudget.Failure{Kind: processbudget.TimedOut, Started: true}, errors.New("unclassified mock failure")} {
		s := testRotateServer()
		calls := 0
		s.triggerRotateIP = func(context.Context, string, int) error { calls++; return err }
		egress := Egress{Name: "jp-android-01", ManagementAddr: "synthetic-offline"}
		if _, e := s.RotateEgress(context.Background(), egress, 8); e == nil {
			t.Fatal("started failure claimed success")
		}
		s.rotateLocksMu.Lock()
		lock := s.rotateLocks[egress.Name]
		lock.until = time.Now().Add(-time.Hour)
		s.rotateLocks[egress.Name] = lock
		s.rotateLocksMu.Unlock()
		if _, e := s.RotateEgress(context.Background(), egress, 8); !errors.Is(e, ErrRotateBusy) || calls != 1 {
			t.Fatal("unknown outcome retriggered after cooldown")
		}
		if got := s.RotateLocksSnapshot(time.Now()); len(got) != 1 || !got[0].Unknown {
			t.Fatal("unknown lock disappeared from snapshot")
		}
	}
}
func TestCarrierSingleflightCancellationAndOtherKey(t *testing.T) {
	s := &Server{carrierCacheTTL: time.Minute}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.carrierProbe = func(ctx context.Context, addr string) string {
		calls.Add(1)
		if addr == "first" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return "Carrier"
		}
		return "Other"
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.cachedAndroidCarrier(context.Background(), "first", time.Now()) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := s.cachedAndroidCarrier(ctx, "first", time.Now()); got != "" {
		t.Fatal("cancelled follower returned carrier")
	}
	if got := s.cachedAndroidCarrier(context.Background(), "second", time.Now()); got != "Other" {
		t.Fatal("one external probe held global cache mutex")
	}
	close(release)
	wg.Wait()
	if got := s.cachedAndroidCarrier(context.Background(), "first", time.Now()); got != "Carrier" || calls.Load() != 2 {
		t.Fatal("singleflight cache missed")
	}
}

func TestBootstrapOwnedTCPSlowBodyCannotLaunch(t *testing.T) {
	s := testBootstrapServer()
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("ZHHUB_WG_BIN", os.Args[0])
	t.Setenv("ZHVPN_AUTH_NATIVE_MODE", "fail")
	t.Setenv("ZHVPN_AUTH_NATIVE_MARKER", marker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httpboundary.NewServer(listener.Addr().String(), s.BootstrapHandler(ClientIngressCompat))
	done := make(chan struct{})
	go func() { _ = server.Serve(listener); close(done) }()
	defer func() { _ = server.Close(); <-done }()
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	body := `{"token":"ZH-OK","wireguard_public_key":"` + key + `"}`
	start := time.Now()
	_, err = fmt.Fprintf(connection, "POST /api/client/bootstrap HTTP/1.1\r\nHost: fixture\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body[:1])
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(8 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal("owned slow-body request did not receive bounded refusal", err)
	}
	response.Body.Close()
	if response.StatusCode != 400 || time.Since(start) > 7*time.Second || len(s.tokenLeases) != 0 {
		t.Fatal("slow body changed authorization state or exceeded body read budget")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("slow incomplete request launched native helper")
	}
}
