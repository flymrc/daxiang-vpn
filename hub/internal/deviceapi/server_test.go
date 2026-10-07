package deviceapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	generated "zongheng-vpn/shared/devicecontract"
)

type wireClient struct {
	t          *testing.T
	url        string
	client     *http.Client
	credential generated.Credential
	pub        string
	priv       ed25519.PrivateKey
	sequence   int
}

func apiStore(t *testing.T) (*deviceauth.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authority.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := deviceauth.New(context.Background(), db, deviceauth.Options{Policy: deviceauth.Policy{Epoch: "test-api-epoch", ManagedBy: "test-api-executor", AddressPools: []string{"10.250.0.0/24"}, Protected: []deviceauth.Protection{{Prefix: "10.250.0.1/32"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}
func (c *wireClient) call(method, path string, body []byte, q *deviceauth.SignedRequest, extra map[string]string) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.url+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	if q != nil {
		req.Header.Set("X-ZH-Challenge", q.ChallengeID)
		req.Header.Set("X-ZH-Nonce", q.Nonce)
		req.Header.Set("X-ZH-Expires", strconv.FormatInt(q.Expires, 10))
		req.Header.Set("X-ZH-Request-ID", q.RequestID)
		req.Header.Set("X-ZH-Signature", q.Signature)
		if q.DeviceID != "" {
			req.Header.Set("X-ZH-Device", q.DeviceID)
			req.Header.Set("X-ZH-Credential", q.CredentialID)
		}
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	response, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return response.StatusCode, data
}
func encoded(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func (c *wireClient) proof(purpose, method, path string, body []byte, activation string) deviceauth.SignedRequest {
	c.t.Helper()
	request := map[string]any{"purpose": purpose, "method": method, "path": path, "body_sha256": deviceauth.BodyDigest(body)}
	if activation != "" {
		request["activation_credential"] = activation
		request["auth_public_key"] = c.pub
	} else {
		request["device_id"] = c.credential.DeviceId
		request["credential_id"] = c.credential.CredentialId
	}
	challengeBody := encoded(c.t, request)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		c.t.Fatal(err)
	}
	expires := time.Now().Add(45 * time.Second).Unix()
	clientNonce := fmt.Sprintf("%x", nonce)
	proof := base64.StdEncoding.EncodeToString(ed25519.Sign(c.priv, generated.ChallengeProofBytes(deviceauth.BodyDigest(challengeBody), clientNonce, expires)))
	code, data := c.call("POST", "/api/v2/challenges", challengeBody, nil, map[string]string{"X-ZH-Challenge-Proof": proof, "X-ZH-Client-Nonce": clientNonce, "X-ZH-Client-Expires": strconv.FormatInt(expires, 10)})
	if code != 200 {
		c.t.Fatalf("challenge %d %s", code, data)
	}
	var ch generated.Challenge
	if err := json.Unmarshal(data, &ch); err != nil {
		c.t.Fatal(err)
	}
	c.sequence++
	q := deviceauth.SignedRequest{Purpose: purpose, Method: method, Path: path, BodyDigest: deviceauth.BodyDigest(body), ChallengeID: ch.ChallengeId, Nonce: ch.Nonce, Expires: ch.ExpiresUnixSeconds, RequestID: "wire-request-" + strconv.Itoa(c.sequence)}
	if activation == "" {
		q.DeviceID = c.credential.DeviceId
		q.CredentialID = c.credential.CredentialId
	}
	q.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(c.priv, deviceauth.SigningBytes(q)))
	return q
}

type memoryWG struct{ peers map[string][]string }

func (m *memoryWG) Snapshot(context.Context, deviceauth.Fence) ([]deviceauth.Peer, error) {
	var result []deviceauth.Peer
	for k, v := range m.peers {
		result = append(result, deviceauth.Peer{PublicKey: k, AllowedIPs: append([]string(nil), v...)})
	}
	return result, nil
}
func (m *memoryWG) Apply(_ context.Context, _ deviceauth.Fence, p deviceauth.Peer) error {
	m.peers[p.PublicKey] = append([]string(nil), p.AllowedIPs...)
	return nil
}
func (m *memoryWG) Remove(_ context.Context, _ deviceauth.Fence, key string) error {
	delete(m.peers, key)
	return nil
}
func TestTLSActivationSignedApplyQueryRotationAndRevoke(t *testing.T) {
	store, db := apiStore(t)
	handler, _ := NewServer(store)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	c := &wireClient{t: t, url: server.URL, client: server.Client(), pub: base64.StdEncoding.EncodeToString(pub), priv: priv}
	activation, err := store.IssueActivation(context.Background(), "owner-a", time.Now().Add(time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	activationBody := encoded(t, generated.ActivationRequest{ActivationCredential: activation.Credential, AuthPublicKey: c.pub})
	q := c.proof("activate", "POST", "/api/v2/activate", activationBody, activation.Credential)
	code, data := c.call("POST", "/api/v2/activate", activationBody, &q, nil)
	if code != 201 {
		t.Fatalf("activate %d %s", code, data)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), activation.Credential) {
		t.Fatal("private activation material returned")
	}
	if err = json.Unmarshal(data, &c.credential); err != nil {
		t.Fatal(err)
	}
	if code, _ = c.call("POST", "/api/v2/activate", activationBody, &q, nil); code != 401 {
		t.Fatal("activation replay accepted")
	}
	wgKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32))
	address := "10.250.0.30/32"
	body := encoded(t, generated.Command{Action: generated.Apply, IdempotencyKey: "apply-1", ExpectedGeneration: 0, WgPublicKey: &wgKey, Address: &address})
	q = c.proof("command.apply", "POST", "/api/v2/commands", body, "")
	tampered := bytes.Replace(body, []byte("10.250.0.30"), []byte("10.250.0.31"), 1)
	if code, _ = c.call("POST", "/api/v2/commands", tampered, &q, nil); code != 401 {
		t.Fatal("raw-body binding missing")
	}
	code, data = c.call("POST", "/api/v2/commands", body, &q, nil)
	if code != 202 {
		t.Fatalf("apply %d %s", code, data)
	}
	var op generated.Operation
	json.Unmarshal(data, &op)
	if !op.Accepted || op.Effective || op.State != generated.Pending {
		t.Fatalf("premature effective: %+v", op)
	}
	if code, _ = c.call("POST", "/api/v2/commands", body, &q, nil); code != 401 {
		t.Fatal("command nonce replay accepted")
	}
	wg := &memoryWG{peers: map[string][]string{base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{99}, 32)): {"10.250.0.11/32"}}}
	scheduler, _ := deviceauth.NewScheduler(store, wg)
	if err = scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/operations/" + op.OperationId
	q = c.proof("operation.status", "GET", path, nil, "")
	code, data = c.call("GET", path, nil, &q, nil)
	if code != 200 {
		t.Fatalf("status %d %s", code, data)
	}
	json.Unmarshal(data, &op)
	if !op.Effective || op.State != generated.Done {
		t.Fatalf("verified generation absent: %+v", op)
	}
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	newKey := base64.StdEncoding.EncodeToString(newPub)
	rotation := encoded(t, generated.RotationRequest{AuthPublicKey: newKey})
	q = c.proof("credential.rotate", "POST", "/api/v2/credentials/rotate", rotation, "")
	if code, _ = c.call("POST", "/api/v2/credentials/rotate", rotation, &q, map[string]string{"X-ZH-New-Key-Signature": q.Signature}); code != 401 {
		t.Fatal("new key possession not required")
	}
	newProof := base64.StdEncoding.EncodeToString(ed25519.Sign(newPriv, deviceauth.SigningBytes(q)))
	code, data = c.call("POST", "/api/v2/credentials/rotate", rotation, &q, map[string]string{"X-ZH-New-Key-Signature": newProof})
	if code != 201 {
		t.Fatalf("rotate %d %s", code, data)
	}
	old := c.credential
	json.Unmarshal(data, &c.credential)
	c.pub = newKey
	c.priv = newPriv
	challenge := encoded(t, map[string]any{"purpose": "command.apply", "method": "POST", "path": "/api/v2/commands", "body_sha256": deviceauth.BodyDigest(body), "device_id": old.DeviceId, "credential_id": old.CredentialId})
	if code, _ = c.call("POST", "/api/v2/challenges", challenge, nil, nil); code != 401 {
		t.Fatal("rotated old credential authorized")
	}
	revoke := encoded(t, generated.Command{Action: generated.Revoke, IdempotencyKey: "revoke-1", ExpectedGeneration: 1})
	q = c.proof("command.revoke", "POST", "/api/v2/commands", revoke, "")
	code, data = c.call("POST", "/api/v2/commands", revoke, &q, nil)
	if code != 202 {
		t.Fatalf("revoke %d %s", code, data)
	}
	if err = scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(wg.peers) != 1 {
		t.Fatal("revoke retained customer or deleted unknown")
	}
	var audits int
	if err = db.QueryRow(`SELECT COUNT(*) FROM deviceauth_audit WHERE device_id=?`, c.credential.DeviceId).Scan(&audits); err != nil || audits < 4 {
		t.Fatalf("atomic audit absent: %d %v", audits, err)
	}
	challenge = encoded(t, map[string]any{"purpose": "operation.status", "method": "GET", "path": path, "body_sha256": deviceauth.BodyDigest(nil), "device_id": c.credential.DeviceId, "credential_id": c.credential.CredentialId})
	if code, _ = c.call("POST", "/api/v2/challenges", challenge, nil, nil); code != 401 {
		t.Fatal("revoked credential allowed query")
	}
}
func TestPlaintextAndContractViolationsFailClosed(t *testing.T) {
	store, _ := apiStore(t)
	handler, _ := NewServer(store)
	plain := httptest.NewServer(handler)
	defer plain.Close()
	c := &wireClient{t: t, url: plain.URL, client: plain.Client()}
	if code, _ := c.call("POST", "/api/v2/challenges", []byte(`{}`), nil, nil); code != 401 {
		t.Fatal("plaintext accepted")
	}
	tlsServer := httptest.NewTLSServer(handler)
	defer tlsServer.Close()
	c.url = tlsServer.URL
	c.client = tlsServer.Client()
	for _, body := range []string{`{"action":"revoke","idempotency_key":"a"}`, `{"action":"revoke","action":"apply","idempotency_key":"a","expected_generation":0}`, `{"action":"revoke","idempotency_key":"a","expected_generation":0,"reason":null}`, `{"action":"revoke","idempotency_key":"a","expected_generation":0,"extra":true}`, `{"action":"revoke","idempotency_key":"a","expected_generation":0} {}`} {
		if code, data := c.call("POST", "/api/v2/commands", []byte(body), nil, nil); code != 400 {
			t.Fatalf("contract violation %d %s", code, data)
		}
	}
	if code, _ := c.call("POST", "/api/v2/commands", bytes.Repeat([]byte{'x'}, 8193), nil, nil); code != 400 {
		t.Fatal("oversized body accepted")
	}
}
