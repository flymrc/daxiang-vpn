package deviceclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

// This fixture independently checks the documented wire bytes, not the client
// signing helper. It is a real TLS socket with normal CA/hostname verification;
// its WG evidence is synthetic and is never presented as real tunnel traffic.
type testAuthority struct {
	t                *testing.T
	mu               sync.Mutex
	server           *httptest.Server
	caFile           string
	serial           int
	challenges       map[string]testChallenge
	credential       *dc.Credential
	keys             map[string]string
	active           bool
	generation       int64
	operations       map[string]dc.Operation
	mutationReceipts map[string]testMutation
	mutationCount    int
	challengeCount   int
	dropKind         string
	dropOnce         bool
	malformedKind    string
	blockKind        string
	failBeforeKind   string
	cancelled        map[string]dc.ResolveReceipt
	cancelCount      int
}
type testChallenge struct {
	request dc.ChallengeRequest
	raw     map[string]any
	result  dc.Challenge
	public  string
}
type testMutation struct {
	kind, public, credentialID, idempotency string
	credential                              *dc.Credential
	operation                               *dc.Operation
}

func authority(t *testing.T) *testAuthority {
	t.Helper()
	a := &testAuthority{t: t, challenges: map[string]testChallenge{}, keys: map[string]string{}, operations: map[string]dc.Operation{}, mutationReceipts: map[string]testMutation{}, cancelled: map[string]dc.ResolveReceipt{}}
	s := httptest.NewUnstartedServer(http.HandlerFunc(a.handle))
	s.Config.ErrorLog = nil
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	s.StartTLS()
	a.server = s
	a.caFile = filepath.Join(t.TempDir(), "fixture-ca.pem")
	if e := os.WriteFile(a.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return a
}
func (a *testAuthority) id() string { a.serial++; return fmt.Sprintf("%032x", a.serial) }
func (a *testAuthority) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *testAuthority) unauthorized(w http.ResponseWriter) {
	a.reply(w, 401, dc.Error{Code: dc.Unauthorized})
}
func stringField(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func pointer(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func fixtureSignature(public string, payload []byte, sig string) bool {
	p, e := base64.StdEncoding.DecodeString(public)
	s, se := base64.StdEncoding.DecodeString(sig)
	return e == nil && se == nil && len(p) == 32 && len(s) == 64 && ed25519.Verify(p, payload, s)
}
func (a *testAuthority) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.TLS == nil {
		a.unauthorized(w)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, 8193))
	if e != nil || len(body) > 8192 {
		a.reply(w, 400, dc.Error{Code: dc.InvalidRequest})
		return
	}
	if r.URL.Path == "/api/v2/challenges" {
		a.challenge(w, r, body)
		return
	}
	ch, ok := a.challenges[r.Header.Get("X-ZH-Challenge")]
	if !ok {
		a.unauthorized(w)
		return
	}
	q := ch.request
	payload, _ := json.Marshal([]string{"zhvpn-device-request", "v2", string(q.Purpose), r.Method, r.URL.Path, bodyDigest(body), r.Header.Get("X-ZH-Device"), r.Header.Get("X-ZH-Credential"), ch.result.ChallengeId, ch.result.Nonce, r.Header.Get("X-ZH-Request-ID"), strconv.FormatInt(ch.result.ExpiresUnixSeconds, 10)})
	if q.Method != dc.ChallengeRequestMethod(r.Method) || q.Path != r.URL.Path || q.BodySha256 != bodyDigest(body) || pointer(q.DeviceId) != r.Header.Get("X-ZH-Device") || pointer(q.CredentialId) != r.Header.Get("X-ZH-Credential") || ch.result.Nonce != r.Header.Get("X-ZH-Nonce") || strconv.FormatInt(ch.result.ExpiresUnixSeconds, 10) != r.Header.Get("X-ZH-Expires") || !hexID(r.Header.Get("X-ZH-Request-ID")) || !fixtureSignature(ch.public, payload, r.Header.Get("X-ZH-Signature")) {
		a.unauthorized(w)
		return
	}
	delete(a.challenges, ch.result.ChallengeId)
	rid := r.Header.Get("X-ZH-Request-ID")
	if r.URL.Path != "/api/v2/requests/resolve" {
		if _, cancelled := a.cancelled[rid]; cancelled {
			a.unauthorized(w)
			return
		}
		if a.failBeforeKind == r.URL.Path {
			a.failBeforeKind = ""
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				_ = conn.Close()
			}
			return
		}
	}
	var result any
	status := 200
	switch r.URL.Path {
	case "/api/v2/activate":
		var b dc.ActivationRequest
		if json.Unmarshal(body, &b) != nil || b.ActivationCredential != "synthetic-activation" || b.AuthPublicKey != ch.public || a.credential != nil {
			a.unauthorized(w)
			return
		}
		a.credential = &dc.Credential{DeviceId: a.id(), CredentialId: a.id(), AuthPublicKey: b.AuthPublicKey, ExpiresUnixSeconds: time.Now().Add(time.Hour).Unix()}
		a.keys[a.credential.CredentialId] = b.AuthPublicKey
		a.active = true
		cr := *a.credential
		a.mutationReceipts[rid] = testMutation{kind: "activate", public: b.AuthPublicKey, credential: &cr}
		a.mutationCount++
		result = cr
		status = 201
	case "/api/v2/credentials/rotate":
		var b dc.RotationRequest
		if !a.active || json.Unmarshal(body, &b) != nil || !fixtureSignature(b.AuthPublicKey, payload, r.Header.Get("X-ZH-New-Key-Signature")) {
			a.unauthorized(w)
			return
		}
		cr := dc.Credential{DeviceId: a.credential.DeviceId, CredentialId: a.id(), AuthPublicKey: b.AuthPublicKey, ExpiresUnixSeconds: a.credential.ExpiresUnixSeconds}
		a.keys[cr.CredentialId] = b.AuthPublicKey
		a.credential = &cr
		a.mutationReceipts[rid] = testMutation{kind: "credential.rotate", public: b.AuthPublicKey, credential: &cr}
		a.mutationCount++
		result = cr
		status = 201
	case "/api/v2/commands":
		var b dc.Command
		if json.Unmarshal(body, &b) != nil || !a.active {
			a.unauthorized(w)
			return
		}
		if b.ExpectedGeneration != a.generation {
			a.reply(w, 409, dc.Error{Code: dc.Conflict})
			return
		}
		a.generation++
		op := dc.Operation{OperationId: a.id(), DeviceId: a.credential.DeviceId, Action: string(b.Action), Generation: a.generation, State: dc.Pending, Accepted: true, Effective: false, DeadlineUnixSeconds: time.Now().Add(time.Minute).Unix()}
		a.operations[op.OperationId] = op
		a.mutationReceipts[rid] = testMutation{kind: "command." + string(b.Action), public: ch.public, credentialID: r.Header.Get("X-ZH-Credential"), idempotency: b.IdempotencyKey, operation: &op}
		if b.Action == dc.Revoke {
			a.active = false
		}
		a.mutationCount++
		result = op
		status = 202
	case "/api/v2/credentials/receipt":
		var b map[string]any
		if json.Unmarshal(body, &b) != nil {
			a.unauthorized(w)
			return
		}
		saved, ok := a.mutationReceipts[stringField(b, "request_id")]
		if !ok || saved.credential == nil || saved.kind != stringField(b, "kind") || saved.public != stringField(b, "auth_public_key") || saved.public != ch.public || a.credential == nil || saved.credential.CredentialId != a.credential.CredentialId || !a.active {
			a.unauthorized(w)
			return
		}
		result = *saved.credential
	case "/api/v2/operations/receipt":
		var b map[string]any
		if json.Unmarshal(body, &b) != nil {
			a.unauthorized(w)
			return
		}
		saved, ok := a.mutationReceipts[stringField(b, "request_id")]
		if !ok || saved.operation == nil || saved.public != ch.public || saved.credentialID != r.Header.Get("X-ZH-Credential") || saved.idempotency != stringField(b, "idempotency_key") {
			a.unauthorized(w)
			return
		}
		result = *saved.operation
	case "/api/v2/requests/resolve":
		var b dc.ResolveRequest
		if json.Unmarshal(body, &b) != nil {
			a.unauthorized(w)
			return
		}
		kind := string(b.Kind)
		if kind == "activate" && pointer(b.ActivationCredential) != "synthetic-activation" {
			a.unauthorized(w)
			return
		}
		if kind == "credential.rotate" && !fixtureSignature(a.keys[r.Header.Get("X-ZH-Credential")], payload, r.Header.Get("X-ZH-Resolve-Owner-Proof")) {
			a.unauthorized(w)
			return
		}
		resolved := dc.ResolveReceipt{Kind: dc.ResolveReceiptKind(kind), RequestId: b.RequestId, AuthPublicKey: b.AuthPublicKey, IdempotencyKey: b.IdempotencyKey}
		if saved, ok := a.mutationReceipts[b.RequestId]; ok {
			if saved.kind != kind || saved.public != ch.public || (saved.operation != nil && saved.idempotency != pointer(b.IdempotencyKey)) {
				a.unauthorized(w)
				return
			}
			resolved.State = dc.Committed
			resolved.Credential = saved.credential
			resolved.Operation = saved.operation
		} else if previous, ok := a.cancelled[b.RequestId]; ok {
			resolved = previous
		} else {
			resolved.State = dc.Cancelled
			a.cancelled[b.RequestId] = resolved
			a.cancelCount++
		}
		result = resolved
	default:
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/operations/")
		op, ok := a.operations[id]
		if !ok || !a.active || r.Method != "GET" || len(body) != 0 {
			a.unauthorized(w)
			return
		}
		op.State = dc.Done
		op.Effective = true
		a.operations[id] = op
		result = op
	}
	if a.dropOnce && a.dropKind == r.URL.Path {
		a.dropOnce = false
		h, ok := w.(http.Hijacker)
		if !ok {
			a.t.Error("fixture TLS hijack unavailable")
			return
		}
		conn, _, e := h.Hijack()
		if e == nil {
			_ = conn.Close()
		}
		return
	}
	if a.malformedKind == r.URL.Path {
		a.malformedKind = ""
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"operation_id":"malformed"}`))
		return
	}
	if a.blockKind == r.URL.Path {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		return
	}
	a.reply(w, status, result)
}
func (a *testAuthority) challenge(w http.ResponseWriter, r *http.Request, body []byte) {
	var q dc.ChallengeRequest
	var raw map[string]any
	if json.Unmarshal(body, &q) != nil || json.Unmarshal(body, &raw) != nil {
		a.unauthorized(w)
		return
	}
	pub := ""
	purpose := string(q.Purpose)
	switch purpose {
	case "activate":
		if pointer(q.ActivationCredential) != "synthetic-activation" {
			a.unauthorized(w)
			return
		}
		pub = pointer(q.AuthPublicKey)
	case "credential.receipt":
		saved, ok := a.mutationReceipts[stringField(raw, "receipt_request_id")]
		if !ok || saved.credential == nil || saved.kind != stringField(raw, "receipt_kind") || saved.public != pointer(q.AuthPublicKey) {
			a.unauthorized(w)
			return
		}
		pub = saved.public
	case "operation.receipt":
		saved, ok := a.mutationReceipts[stringField(raw, "receipt_request_id")]
		if !ok || saved.operation == nil || saved.credentialID != pointer(q.CredentialId) || saved.idempotency != stringField(raw, "receipt_idempotency_key") {
			a.unauthorized(w)
			return
		}
		pub = saved.public
	default:
		if purpose == "request.resolve" {
			kind := stringField(raw, "receipt_kind")
			if kind == "activate" {
				if pointer(q.ActivationCredential) != "synthetic-activation" {
					a.unauthorized(w)
					return
				}
				pub = pointer(q.AuthPublicKey)
			} else if kind == "credential.rotate" {
				if a.credential == nil || a.credential.DeviceId != pointer(q.DeviceId) || a.keys[pointer(q.CredentialId)] == "" {
					a.unauthorized(w)
					return
				}
				pub = pointer(q.AuthPublicKey)
			} else {
				if a.credential == nil || a.credential.DeviceId != pointer(q.DeviceId) {
					a.unauthorized(w)
					return
				}
				pub = a.keys[pointer(q.CredentialId)]
			}
			break
		}
		if !a.active || a.credential == nil || a.credential.DeviceId != pointer(q.DeviceId) || a.credential.CredentialId != pointer(q.CredentialId) {
			a.unauthorized(w)
			return
		}
		pub = a.credential.AuthPublicKey
	}
	exp, e := strconv.ParseInt(r.Header.Get("X-ZH-Client-Expires"), 10, 64)
	nonce := r.Header.Get("X-ZH-Client-Nonce")
	payload, _ := json.Marshal([]string{"zhvpn-device-challenge", "v2", "POST", "/api/v2/challenges", bodyDigest(body), nonce, strconv.FormatInt(exp, 10)})
	if e != nil || !hexID(nonce) || exp <= time.Now().Unix() || exp > time.Now().Add(60*time.Second).Unix() || !fixtureSignature(pub, payload, r.Header.Get("X-ZH-Challenge-Proof")) {
		a.unauthorized(w)
		return
	}
	ch := dc.Challenge{ChallengeId: a.id(), Nonce: a.id(), ExpiresUnixSeconds: time.Now().Add(60 * time.Second).Unix()}
	if purpose == "request.resolve" && stringField(raw, "receipt_kind") == "credential.rotate" && !fixtureSignature(a.keys[pointer(q.CredentialId)], payload, r.Header.Get("X-ZH-Resolve-Owner-Proof")) {
		a.unauthorized(w)
		return
	}
	a.challenges[ch.ChallengeId] = testChallenge{request: q, raw: raw, result: ch, public: pub}
	a.challengeCount++
	a.reply(w, 200, ch)
}

func invoke(t *testing.T, a *testAuthority, home paths.Context, input string, args ...string) (Receipt, error, string) {
	t.Helper()
	args = append(args, "--ca-file", a.caFile)
	var out, errOut bytes.Buffer
	e := Run(context.Background(), home, args, strings.NewReader(input), &out, &errOut)
	if errOut.Len() != 0 {
		t.Fatal("unexpected stderr")
	}
	if strings.Contains(out.String(), "synthetic-activation") {
		t.Fatal("activation secret leaked")
	}
	r, de := DecodeReceipt(out.Bytes())
	if de != nil {
		t.Fatalf("invalid public receipt, length=%d", out.Len())
	}
	return r, e, out.String()
}
func activateFixture(t *testing.T, a *testAuthority, home paths.Context) dc.Credential {
	t.Helper()
	r, e, _ := invoke(t, a, home, "synthetic-activation\n", "activate", "--server", a.server.URL, "--activation-stdin")
	if e != nil || !r.OK || r.Credential == nil {
		t.Fatal("activation failed")
	}
	return *r.Credential
}
func wgFixture() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)) }

func (a *testAuthority) counts() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mutationCount, a.challengeCount
}
func (a *testAuthority) loseResponse(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropKind = path
	a.dropOnce = true
}

func TestLostActivationRotationApplyAndSelfRevokeRecoverWithoutNewMutation(t *testing.T) {
	for _, kind := range []string{"activate", "rotate-credential", "apply", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			if kind != "activate" {
				activateFixture(t, a, home)
			}
			path := "/api/v2/commands"
			args := []string{kind, "--expected-generation", "0", "--idempotency-key", "recover-original"}
			input := ""
			if kind == "activate" {
				path = "/api/v2/activate"
				input = "synthetic-activation\n"
				args = []string{"activate", "--server", a.server.URL, "--activation-stdin"}
			}
			if kind == "rotate-credential" {
				path = "/api/v2/credentials/rotate"
				args = []string{kind}
			}
			if kind == "apply" {
				args = append(args, "--wg-public-key", wgFixture(), "--address", "10.66.0.20/32")
			}
			a.loseResponse(path)
			r, e, output := invoke(t, a, home, input, args...)
			if e == nil || r.Outcome != "result_unknown" || !r.Pending {
				t.Fatal("response loss was treated as definite failure")
			}
			before, _ := a.counts()
			st, se := privateStore{home}.load()
			if se != nil || st.Pending == nil {
				t.Fatal("missing durable pending key/request")
			}
			if strings.Contains(output, st.PrivateKey) || (st.Pending.NewPrivateKey != "" && strings.Contains(output, st.Pending.NewPrivateKey)) {
				t.Fatal("pending secret leaked")
			}
			original := *st.Pending
			// A different original request ID cannot read an unrelated receipt.
			st.Pending.RequestID = strings.Repeat("f", 32)
			if (privateStore{home}).save(st) != nil {
				t.Fatal("fixture write failed")
			}
			r, e, _ = invoke(t, a, home, "", "recover")
			if e == nil || r.Code != "unauthorized" || !r.Pending {
				t.Fatal("wrong request ID recovered receipt")
			}
			st.Pending = &original
			if (privateStore{home}).save(st) != nil {
				t.Fatal("fixture write failed")
			}
			r, e, _ = invoke(t, a, home, "", "recover")
			if e != nil || !r.OK || r.Pending || r.RequestID != original.RequestID {
				t.Fatal("read-only receipt recovery failed")
			}
			if kind == "activate" || kind == "rotate-credential" {
				if r.Outcome != "credential_recovered" || r.Credential == nil {
					t.Fatal("credential recovery omitted public identity")
				}
			} else if r.Outcome != "operation_recovered" || r.Operation == nil || r.Operation.Effective {
				t.Fatal("operation receipt recovery confused accepted/effective")
			}
			after, _ := a.counts()
			if before != after {
				t.Fatal("recovery created a new mutation")
			}
			r, e, _ = invoke(t, a, home, "", "recover")
			if e == nil || r.Code != "no_pending_intent" {
				t.Fatal("completed intent was reused")
			}
			after, _ = a.counts()
			if before != after {
				t.Fatal("repeated recover mutated authority")
			}
		})
	}
}

// newPrivate is synthetic material held only in local memory/protected fixture
// state. Tests never log it or put it in command arguments.
func newPrivate(t *testing.T) string {
	t.Helper()
	_, key, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal("key generation failed")
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestRecoveryRejectsWrongNewKeyAndWrongCommandIdempotency(t *testing.T) {
	for _, kind := range []string{"rotate-credential", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			activateFixture(t, a, home)
			args := []string{kind}
			path := "/api/v2/credentials/rotate"
			if kind == "revoke" {
				path = "/api/v2/commands"
				args = append(args, "--expected-generation", "0", "--idempotency-key", "original-revoke")
			}
			a.loseResponse(path)
			_, e, _ := invoke(t, a, home, "", args...)
			if e == nil {
				t.Fatal("fixture did not lose mutation response")
			}
			st, e := privateStore{home}.load()
			if e != nil || st.Pending == nil {
				t.Fatal("pending state absent")
			}
			original := *st.Pending
			if kind == "rotate-credential" {
				st.Pending.NewPrivateKey = newPrivate(t)
			} else {
				st.Pending.IdempotencyKey = "different-command"
			}
			if (privateStore{home}).save(st) != nil {
				t.Fatal("fixture write failed")
			}
			r, e, _ := invoke(t, a, home, "", "recover")
			if e == nil || r.Code != "unauthorized" || !r.Pending {
				t.Fatal("wrong proof binding recovered receipt")
			}
			st.Pending = &original
			if (privateStore{home}).save(st) != nil {
				t.Fatal("fixture write failed")
			}
			r, e, _ = invoke(t, a, home, "", "recover")
			if e != nil || !r.OK {
				t.Fatal("original bound proof could not recover")
			}
		})
	}
}

type failingStore struct {
	base           privateStore
	writes, failAt int
}

func (s *failingStore) load() (state, error) { return s.base.load() }
func (s *failingStore) save(st state) error {
	s.writes++
	if s.writes == s.failAt {
		return fmt.Errorf("synthetic storage failure")
	}
	return s.base.save(st)
}

func TestIntentWriteFailurePreventsMutationAndReceiptWriteFailureRecovers(t *testing.T) {
	for _, failAt := range []int{2, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			roots := a.server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
			c, e := New(a.server.URL, roots)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			store := &failingStore{base: privateStore{home}, failAt: failAt}
			r := c.execute(context.Background(), store, state{}, options{command: "activate"}, strings.NewReader("synthetic-activation"))
			mutations, _ := a.counts()
			if r.OK || r.Code != "local_storage_failure" {
				t.Fatal("storage failure accepted")
			}
			if failAt == 2 && mutations != 0 {
				t.Fatal("mutation sent before durable intent")
			}
			if failAt == 3 {
				if mutations != 1 || !r.Pending {
					t.Fatal("receipt failure lost committed mutation intent")
				}
				r, e, _ = invoke(t, a, home, "", "recover")
				if e != nil || !r.OK {
					t.Fatal("receipt persistence recovery failed")
				}
				if n, _ := a.counts(); n != 1 {
					t.Fatal("receipt persistence recovery repeated mutation")
				}
			}
		})
	}
}

func TestTLSActivateApplyStatusRotateAndRevoke(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	old := activateFixture(t, a, home)
	r, e, _ := invoke(t, a, home, "", "apply", "--wg-public-key", wgFixture(), "--address", "10.66.0.20/32", "--expected-generation", "0", "--idempotency-key", "synthetic-apply")
	if e != nil || r.Outcome != "accepted" || r.Operation == nil || r.Operation.Effective || r.Operation.State != dc.Pending {
		t.Fatal("accepted was confused with effective")
	}
	id := r.Operation.OperationId
	r, e, _ = invoke(t, a, home, "", "status", "--operation-id", id)
	if e != nil || r.Operation == nil || !r.Operation.Effective {
		t.Fatal("signed operation status failed")
	}
	r, e, output := invoke(t, a, home, "", "rotate-credential")
	if e != nil || r.Credential == nil || r.Credential.CredentialId == old.CredentialId || r.Credential.ExpiresUnixSeconds > old.ExpiresUnixSeconds {
		t.Fatal("credential rotation failed")
	}
	st, se := privateStore{home}.load()
	if se != nil {
		t.Fatal("protected state unreadable")
	}
	if strings.Contains(output, st.PrivateKey) {
		t.Fatal("private key leaked")
	}
	r, e, _ = invoke(t, a, home, "", "status", "--operation-id", id)
	if e != nil || !r.OK {
		t.Fatal("new credential status failed")
	}
	r, e, _ = invoke(t, a, home, "", "revoke", "--expected-generation", "1", "--idempotency-key", "synthetic-revoke")
	if e != nil || r.Operation == nil || r.Operation.Effective {
		t.Fatal("self revoke receipt failed")
	}
	r, e, _ = invoke(t, a, home, "", "status", "--operation-id", id)
	if e == nil || r.Code != "unauthorized" {
		t.Fatal("self revoked credential still authorized")
	}
	if a.mutationCount != 4 {
		t.Fatal("unexpected mutation retry")
	}
}

func TestMutationResponseLossPersistsIntentAndNeverRetries(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	activateFixture(t, a, home)
	a.dropKind = "/api/v2/commands"
	a.dropOnce = true
	r, e, _ := invoke(t, a, home, "", "apply", "--wg-public-key", wgFixture(), "--address", "10.66.0.20/32", "--expected-generation", "0", "--idempotency-key", "lost-apply")
	if e == nil || r.Outcome != "result_unknown" || !r.Pending || r.RequestID == "" || r.IdempotencyKey != "lost-apply" {
		t.Fatal("lost response was not marked unknown")
	}
	st, e := privateStore{home}.load()
	if e != nil || st.Pending == nil || st.Pending.RequestID != r.RequestID {
		t.Fatal("lost mutation intent was not persisted")
	}
	_, e, _ = invoke(t, a, home, "", "apply", "--wg-public-key", wgFixture(), "--address", "10.66.0.20/32", "--expected-generation", "0", "--idempotency-key", "new-operation")
	if e == nil || a.mutationCount != 2 {
		t.Fatal("pending mutation was automatically retried")
	}
}

func TestNormalTLSRejectsUntrustedServerAndHTTPAndRedirect(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	var out bytes.Buffer
	e := Run(context.Background(), home, []string{"activate", "--server", a.server.URL, "--activation-stdin"}, strings.NewReader("synthetic-activation\n"), &out, io.Discard)
	r, de := DecodeReceipt(out.Bytes())
	if e == nil || de != nil || r.Code != "connection_failure" || a.mutationCount != 0 {
		t.Fatal("untrusted TLS was accepted")
	}
	if _, e = New(strings.Replace(a.server.URL, "https:", "http:", 1), nil); e == nil {
		t.Fatal("plaintext accepted")
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, a.server.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	roots := x509.NewCertPool()
	roots.AddCert(redirect.Certificate())
	c, e := New(redirect.URL, roots)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects can forward secrets")
	}
	_, key, ke := ed25519.GenerateKey(nil)
	if ke != nil {
		t.Fatal("fixture key failed")
	}
	_, e = c.challenge(context.Background(), "activate", "POST", "/api/v2/activate", []byte(`{}`), nil, "synthetic-activation", key)
	if e == nil {
		t.Fatal("redirect response accepted")
	}
	if _, challenges := a.counts(); challenges != 0 {
		t.Fatal("redirect followed with activation secret")
	}
}

func TestCommandTimeoutHasUnknownReceiptAndNoAutomaticRetry(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	activateFixture(t, a, home)
	a.mu.Lock()
	a.blockKind = "/api/v2/commands"
	a.mu.Unlock()
	r, e, _ := invoke(t, a, home, "", "revoke", "--expected-generation", "0", "--idempotency-key", "timeout-revoke", "--timeout", "1s")
	if e == nil || r.Outcome != "result_unknown" || !r.Pending {
		t.Fatal("mutation timeout was treated as rejected")
	}
	if n, _ := a.counts(); n != 2 {
		t.Fatal("timeout repeated mutation")
	}
	a.mu.Lock()
	a.blockKind = ""
	a.mu.Unlock()
	r, e, _ = invoke(t, a, home, "", "recover")
	if e != nil || !r.OK {
		t.Fatal("self revoke timeout could not recover original receipt")
	}
}

func TestMalformedSuccessRetainsRecoveryIntent(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	a.mu.Lock()
	a.malformedKind = "/api/v2/activate"
	a.mu.Unlock()
	r, e, _ := invoke(t, a, home, "synthetic-activation\n", "activate", "--server", a.server.URL, "--activation-stdin")
	if e == nil || r.Outcome != "result_unknown" || !r.Pending {
		t.Fatal("malformed 201 discarded activation key")
	}
	r, e, _ = invoke(t, a, home, "", "recover")
	if e != nil || r.Credential == nil {
		t.Fatal("malformed credential response could not recover")
	}
	if n, _ := a.counts(); n != 1 {
		t.Fatal("malformed receipt caused duplicate activation")
	}
}

func TestStrictReceiptRejectsAliasesDuplicatesMissingAndEffectiveWithoutDone(t *testing.T) {
	r := Receipt{ContractVersion: Version, Command: "apply", OK: true, Outcome: "accepted", Operation: &dc.Operation{OperationId: strings.Repeat("a", 32), DeviceId: strings.Repeat("b", 32), Action: "apply", Generation: 1, State: dc.Pending, Accepted: true, DeadlineUnixSeconds: 1}}
	b, _ := json.Marshal(r)
	if _, e := DecodeReceipt(b); e != nil {
		t.Fatal("valid receipt rejected")
	}
	bad := []string{
		strings.Replace(string(b), `"effective":false,`, "", 1),
		strings.Replace(string(b), `"ok":true`, `"ok":true,"OK":false`, 1),
		strings.Replace(string(b), `"ok":true`, `"ok":true,"ok":false`, 1),
		strings.Replace(string(b), `"effective":false`, `"effective":true`, 1),
		strings.Replace(string(b), `"contract_version":2`, `"contract_version":1`, 1),
		strings.Replace(string(b), `"pending":false`, `"pending":null`, 1),
	}
	for _, v := range bad {
		if _, e := DecodeReceipt([]byte(v)); e == nil {
			t.Fatal("invalid receipt accepted")
		}
	}
}

func TestArgumentsAndServerBindingRejectSecretsInArgv(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	activateFixture(t, a, home)
	for _, args := range [][]string{{"activate", "--server", a.server.URL, "synthetic-activation"}, {"status", "--operation-id", strings.Repeat("a", 32), "--server", "https://example.invalid"}, {"apply", "--wg-public-key", wgFixture(), "--address", "10.66.0.20/24", "--expected-generation", "0", "--idempotency-key", "x"}, {"rotate-credential", "--expected-generation", "0"}} {
		r, e, output := invoke(t, a, home, "", args...)
		if e == nil || r.OK || strings.Contains(output, "synthetic-activation") {
			t.Fatal("unsafe arguments accepted or secret echoed")
		}
	}
	if a.mutationCount != 1 {
		t.Fatal("rejected local arguments reached mutation")
	}
}

func TestConcurrentCommandsHoldOneHomeTransactionAcrossNetworkAndReceipt(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	activateFixture(t, a, home)
	start := make(chan struct{})
	results := make(chan Receipt, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			var out bytes.Buffer
			e := Run(context.Background(), home, []string{"apply", "--ca-file", a.caFile, "--wg-public-key", wgFixture(), "--address", "10.66.0.20/32", "--expected-generation", "0", "--idempotency-key", fmt.Sprintf("concurrent-%d", i)}, strings.NewReader(""), &out, io.Discard)
			r, de := DecodeReceipt(out.Bytes())
			if de != nil {
				errs <- de
				return
			}
			if e != nil && e != ErrReported {
				errs <- e
				return
			}
			results <- r
		}(i)
	}
	close(start)
	accepted, rejected := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case e := <-errs:
			t.Fatal(e)
		case r := <-results:
			if r.OK {
				accepted++
			} else if r.Code == "conflict" && !r.Pending {
				rejected++
			} else {
				t.Fatal("unexpected concurrent result")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("home transaction did not finish")
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatal("generation fence did not serialize competing commands")
	}
	st, e := privateStore{home}.load()
	if e != nil || st.Pending != nil {
		t.Fatal("successful/rejected commands corrupted private state")
	}
	if n, _ := a.counts(); n != 2 {
		t.Fatal("competing generation created extra mutation")
	}
}

func TestCorruptPrivateStateRejectsBeforeNetworkAndDoesNotEchoContents(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	store, e := proxy.NewPrivateState(home, "device-v2-state.json")
	if e != nil {
		t.Fatal(e)
	}
	secret := "sensitive-fixture-never-return"
	if e = store.Write([]byte(`{"version":2,"server":"` + a.server.URL + `","private_key":"` + secret + `"}`)); e != nil {
		t.Fatal(e)
	}
	r, e, output := invoke(t, a, home, "", "status", "--operation-id", strings.Repeat("a", 32))
	if e == nil || r.Code != "local_storage_failure" || strings.Contains(output, secret) {
		t.Fatal("corrupt private state was consumed or leaked")
	}
	if n, ch := a.counts(); n != 0 || ch != 0 {
		t.Fatal("corrupt local key reached network")
	}
}

func TestReceiptSchemaMatchesPublicPropertiesAndCodes(t *testing.T) {
	b, e := os.ReadFile("receipt.schema.json")
	if e != nil {
		t.Fatal(e)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if json.Unmarshal(b, &schema) != nil {
		t.Fatal("schema invalid JSON")
	}
	check := func(typ reflect.Type, props map[string]json.RawMessage) {
		t.Helper()
		if len(props) != typ.NumField() {
			t.Fatal("schema field count drift")
		}
		for i := 0; i < typ.NumField(); i++ {
			tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
			if props[tag] == nil {
				t.Fatal("schema property drift")
			}
		}
	}
	check(reflect.TypeOf(Receipt{}), schema.Properties)
	check(reflect.TypeOf(dc.Credential{}), schema.Defs["credential"].Properties)
	check(reflect.TypeOf(dc.Operation{}), schema.Defs["operation"].Properties)
	var codes struct {
		Enum []string `json:"enum"`
	}
	if json.Unmarshal(schema.Properties["code"], &codes) != nil {
		t.Fatal("schema codes missing")
	}
	for _, c := range codes.Enum {
		if !validReceiptCode(c) {
			t.Fatal("schema unknown error code")
		}
	}
}

func TestExplicitCancelUnsubmittedActivationRotationAndCommands(t *testing.T) {
	for _, kind := range []string{"activate", "rotate-credential", "apply", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			if kind != "activate" {
				activateFixture(t, a, home)
			}
			beforeState, _ := privateStore{home}.load()
			path := "/api/v2/commands"
			args := []string{kind, "--expected-generation", "0", "--idempotency-key", "cancel-target"}
			input := ""
			if kind == "activate" {
				path = "/api/v2/activate"
				input = "synthetic-activation"
				args = []string{kind, "--server", a.server.URL, "--activation-stdin"}
			}
			if kind == "rotate-credential" {
				path = "/api/v2/credentials/rotate"
				args = []string{kind}
			}
			if kind == "apply" {
				args = append(args, "--wg-public-key", wgFixture(), "--address", "10.66.0.20/32")
			}
			a.mu.Lock()
			a.failBeforeKind = path
			a.mu.Unlock()
			r, e, _ := invoke(t, a, home, input, args...)
			if e == nil || r.Outcome != "result_unknown" || !r.Pending {
				t.Fatal("unsubmitted original intent missing")
			}
			count, _ := a.counts()
			cancelArgs := []string{"cancel-pending"}
			if kind == "activate" {
				cancelArgs = append(cancelArgs, "--activation-stdin")
			}
			r, e, _ = invoke(t, a, home, input, cancelArgs...)
			if e != nil || !r.OK || r.Outcome != "cancelled" || r.Pending || r.Operation != nil || r.Credential != nil {
				t.Fatal("definitive cancellation receipt invalid")
			}
			st, e := privateStore{home}.load()
			if e != nil || st.Pending != nil {
				t.Fatal("cancelled intent not committed locally")
			}
			if kind == "activate" {
				if st.PrivateKey != "" || st.Credential != nil {
					t.Fatal("unaccepted activation key kept")
				}
			} else if st.PrivateKey != beforeState.PrivateKey || st.Credential == nil || st.Credential.CredentialId != beforeState.Credential.CredentialId {
				t.Fatal("cancelled mutation changed old credential")
			}
			if n, _ := a.counts(); n != count {
				t.Fatal("cancel created authorization mutation")
			}
			a.mu.Lock()
			cancelCount := a.cancelCount
			a.mu.Unlock()
			if cancelCount != 1 {
				t.Fatal("cancellation tombstone missing")
			}
		})
	}
}

func TestCancelCommittedResponsesRecoverOriginalAndNeverLieCancelled(t *testing.T) {
	for _, kind := range []string{"activate", "rotate-credential", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			a := authority(t)
			home := privateHome(t)
			if kind != "activate" {
				activateFixture(t, a, home)
			}
			path := "/api/v2/commands"
			args := []string{kind, "--expected-generation", "0", "--idempotency-key", "committed-revoke"}
			input := ""
			if kind == "activate" {
				path = "/api/v2/activate"
				input = "synthetic-activation"
				args = []string{kind, "--server", a.server.URL, "--activation-stdin"}
			}
			if kind == "rotate-credential" {
				path = "/api/v2/credentials/rotate"
				args = []string{kind}
			}
			a.loseResponse(path)
			_, e, _ := invoke(t, a, home, input, args...)
			if e == nil {
				t.Fatal("fixture response loss absent")
			}
			before, _ := a.counts()
			cancelArgs := []string{"cancel-pending"}
			if kind == "activate" {
				cancelArgs = append(cancelArgs, "--activation-stdin")
			}
			r, e, _ := invoke(t, a, home, input, cancelArgs...)
			if e != nil || !r.OK || r.Outcome == "cancelled" || r.Pending {
				t.Fatal("committed original was falsely cancelled")
			}
			if kind == "revoke" {
				if r.Outcome != "operation_recovered" || r.Operation == nil {
					t.Fatal("committed operation lost")
				}
			} else if r.Outcome != "credential_recovered" || r.Credential == nil {
				t.Fatal("committed credential lost")
			}
			if n, _ := a.counts(); n != before {
				t.Fatal("resolve resubmitted original")
			}
		})
	}
}

func TestLostCancelResponseKeepsIntentAndRepeatedResolveIsIdempotent(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	activateFixture(t, a, home)
	a.mu.Lock()
	a.failBeforeKind = "/api/v2/commands"
	a.mu.Unlock()
	_, e, _ := invoke(t, a, home, "", "revoke", "--expected-generation", "0", "--idempotency-key", "cancel-loss")
	if e == nil {
		t.Fatal("original request was not dropped")
	}
	a.loseResponse("/api/v2/requests/resolve")
	r, e, _ := invoke(t, a, home, "", "cancel-pending")
	if e == nil || r.Outcome != "result_unknown" || !r.Pending {
		t.Fatal("lost cancellation incorrectly cleared original intent")
	}
	st, se := privateStore{home}.load()
	if se != nil || st.Pending == nil {
		t.Fatal("lost cancel discarded original key")
	}
	r, e, _ = invoke(t, a, home, "", "cancel-pending")
	if e != nil || r.Outcome != "cancelled" || r.Pending {
		t.Fatal("repeated negative resolution failed")
	}
	a.mu.Lock()
	n := a.cancelCount
	a.mu.Unlock()
	if n != 1 {
		t.Fatal("cancel replay grew tombstone storage")
	}
	if n, _ := a.counts(); n != 1 {
		t.Fatal("cancel replay changed authorization")
	}
}

func TestCancelRequiresActivationSecretAndLeavesPendingOnMalformedProof(t *testing.T) {
	a := authority(t)
	home := privateHome(t)
	a.mu.Lock()
	a.failBeforeKind = "/api/v2/activate"
	a.mu.Unlock()
	_, e, _ := invoke(t, a, home, "synthetic-activation", "activate", "--server", a.server.URL, "--activation-stdin")
	if e == nil {
		t.Fatal("original fixture request not dropped")
	}
	r, e, _ := invoke(t, a, home, "", "cancel-pending")
	if e == nil || r.Code != "invalid_activation_input" || !r.Pending {
		t.Fatal("activation cancelled without original secret")
	}
	r, e, _ = invoke(t, a, home, "wrong-secret", "cancel-pending", "--activation-stdin")
	if e == nil || r.Code != "unauthorized" || !r.Pending {
		t.Fatal("wrong activation secret cancelled request")
	}
	a.mu.Lock()
	a.malformedKind = "/api/v2/requests/resolve"
	a.mu.Unlock()
	r, e, _ = invoke(t, a, home, "synthetic-activation", "cancel-pending", "--activation-stdin")
	if e == nil || r.Outcome != "result_unknown" || !r.Pending {
		t.Fatal("malformed cancellation cleared key")
	}
	r, e, _ = invoke(t, a, home, "synthetic-activation", "cancel-pending", "--activation-stdin")
	if e != nil || r.Outcome != "cancelled" {
		t.Fatal("valid cancellation could not settle pending")
	}
}

func TestRejectedMutationIntentClearFailureRetainsOriginalProof(t *testing.T) {
	a := authority(t)
	activateFixture(t, a, privateHome(t))
	// A second synthetic activation reaches a verified challenge but is
	// definitively rejected by this fixture's single-activation handler. The
	// clear-intent write then fails; this used to risk losing the pending pointer.
	home := privateHome(t)
	roots := a.server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	c, e := New(a.server.URL, roots)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	s := &failingStore{base: privateStore{home}, failAt: 3}
	r := c.execute(context.Background(), s, state{}, options{command: "activate"}, strings.NewReader("synthetic-activation"))
	if r.OK || !r.Pending || r.Code != "local_storage_failure" || r.RequestID == "" {
		t.Fatal("failed rejection cleanup discarded intent")
	}
	st, e := s.load()
	if e != nil || st.Pending == nil || st.Pending.RequestID != r.RequestID {
		t.Fatal("rejection cleanup lost durable proof")
	}
	if n, _ := a.counts(); n != 1 {
		t.Fatal("rejected activation created a device")
	}
}
