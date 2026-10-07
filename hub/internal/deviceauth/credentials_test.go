package deviceauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
	"zongheng-vpn/shared/devicecontract"
)

func authPair(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}
func request(t *testing.T, s *Store, cr Credential, priv ed25519.PrivateKey, purpose, method, path string, body []byte, requestID string) SignedRequest {
	t.Helper()
	ch, err := testChallenge(t, s, priv, ChallengeRequest{Purpose: purpose, Method: method, Path: path, BodyDigest: BodyDigest(body), DeviceID: cr.DeviceID, CredentialID: cr.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := SignedRequest{Purpose: purpose, Method: method, Path: path, BodyDigest: BodyDigest(body), DeviceID: cr.DeviceID, CredentialID: cr.ID, ChallengeID: ch.ID, Nonce: ch.Nonce, Expires: ch.Expires, RequestID: requestID}
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(r)))
	return r
}
func activationRequest(t *testing.T, s *Store, material, key string, priv ed25519.PrivateKey) SignedRequest {
	t.Helper()
	body := []byte("synthetic activation exact HTTP bytes")
	ch, err := testChallenge(t, s, priv, ChallengeRequest{Purpose: "activate", Method: "POST", Path: "/api/v2/activate", BodyDigest: BodyDigest(body), ActivationCredential: material, PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	r := SignedRequest{Purpose: "activate", Method: "POST", Path: "/api/v2/activate", BodyDigest: BodyDigest(body), ChallengeID: ch.ID, Nonce: ch.Nonce, Expires: ch.Expires, RequestID: "activation-request"}
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(r)))
	return r
}

func testChallenge(t *testing.T, s *Store, priv ed25519.PrivateKey, r ChallengeRequest) (Challenge, error) {
	t.Helper()
	return s.Challenge(context.Background(), testChallengeProof(t, s, priv, r))
}

func testChallengeProof(t *testing.T, s *Store, priv ed25519.PrivateKey, r ChallengeRequest) ChallengeRequest {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ProofDigest = BodyDigest(body)
	r.ClientNonce, err = opaque()
	if err != nil {
		t.Fatal(err)
	}
	r.ClientExpires = s.opts.Now().Add(45 * time.Second).Unix()
	r.Proof = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, devicecontract.ChallengeProofBytes(r.ProofDigest, r.ClientNonce, r.ClientExpires)))
	return r
}
func activated(t *testing.T, s *Store) (Credential, ed25519.PrivateKey) {
	t.Helper()
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, priv := authPair(t)
	cr, err := s.Activate(context.Background(), activationRequest(t, s, a.Credential, key, priv), a.Credential, key)
	if err != nil {
		t.Fatal(err)
	}
	return cr, priv
}
func TestActivationAtomicConcurrentConsumeAndNoPlaintextPersistence(t *testing.T) {
	s, db, path := newTestStore(t)
	s2 := reopen(t, path)
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, priv := authPair(t)
	r := activationRequest(t, s, a.Credential, key, priv)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, store := range []*Store{s, s2} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			_, err := s.Activate(context.Background(), r, a.Credential, key)
			results <- err
		}(store)
	}
	wg.Wait()
	close(results)
	ok, rejected := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrUnauthorized) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || rejected != 1 {
		t.Fatalf("not atomic single use: %d/%d", ok, rejected)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_devices`).Scan(&count)
	if count != 1 {
		t.Fatal("concurrent enrollment duplication")
	}
	var verifier string
	db.QueryRow(`SELECT verifier FROM deviceauth_activations`).Scan(&verifier)
	if verifier == a.Credential || len(verifier) != 64 {
		t.Fatal("plaintext activation persisted")
	}
	if _, err = s.Activate(context.Background(), r, a.Credential, key); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("activation replay allowed")
	}
}
func TestSignedPurposeBodyIdentityAndReplayRejected(t *testing.T) {
	s, _, _ := newTestStore(t)
	cr, priv := activated(t, s)
	r := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("exact apply"), "apply-1")
	cmd := command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "first")
	for _, mutate := range []func(*SignedRequest){func(r *SignedRequest) { r.BodyDigest = BodyDigest([]byte("tampered")) }, func(r *SignedRequest) { r.Purpose = "command.revoke" }, func(r *SignedRequest) { r.DeviceID = "other" }, func(r *SignedRequest) { r.Path = "/api/v2/credentials/rotate" }, func(r *SignedRequest) { r.Expires++ }} {
		bad := r
		mutate(&bad)
		if _, err := s.SubmitSigned(context.Background(), bad, cmd); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("tampered signature accepted: %v", err)
		}
	}
	op, err := s.SubmitSigned(context.Background(), r, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if op.State != "pending" {
		t.Fatal("premature effective")
	}
	if _, err = s.SubmitSigned(context.Background(), r, cmd); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("nonce replay accepted")
	}
	fresh := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("exact apply"), "apply-2")
	retry, err := s.SubmitSigned(context.Background(), fresh, cmd)
	if err != nil || retry.ID != op.ID {
		t.Fatalf("valid fresh-nonce idempotency failed: %v", err)
	}
	fresh = request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("exact apply"), "apply-2")
	if _, err = s.SubmitSigned(context.Background(), fresh, cmd); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("request ID replay with fresh nonce accepted")
	}
}
func TestRotationRevokeAndSchedulerExpiryRestart(t *testing.T) {
	s, _, _ := newTestStore(t)
	cr, priv := activated(t, s)
	newKey, newPriv := authPair(t)
	r := request(t, s, cr, priv, "credential.rotate", "POST", "/api/v2/credentials/rotate", []byte("new key"), "rotate")
	if _, err := s.RotateCredential(context.Background(), r, newKey, r.Signature); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("new key possession not checked")
	}
	fresh, err := s.RotateCredential(context.Background(), r, newKey, base64.StdEncoding.EncodeToString(ed25519.Sign(newPriv, SigningBytes(r))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Challenge(context.Background(), ChallengeRequest{Purpose: "command.apply", Method: "POST", Path: "/api/v2/commands", BodyDigest: BodyDigest(nil), DeviceID: cr.DeviceID, CredentialID: cr.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("replaced credential still authorized")
	}
	r = request(t, s, fresh, newPriv, "command.apply", "POST", "/api/v2/commands", []byte("apply"), "new-apply")
	op, err := s.SubmitSigned(context.Background(), r, command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "apply"))
	if err != nil {
		t.Fatal(err)
	}
	wg := newFake()
	wg.peers[testKey(90000)] = []string{"10.66.0.11/32"}
	scheduler, _ := NewScheduler(s, wg)
	if err = scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := request(t, s, fresh, newPriv, "operation.status", "GET", "/api/v2/operations/"+op.ID, nil, "query")
	_, effective, err := s.OperationSigned(context.Background(), q, op.ID)
	if err != nil || !effective {
		t.Fatalf("verified current generation absent: %v", err)
	}
	s.opts.Now = func() time.Time { return testNow.Add(time.Hour) }
	if err = scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Device(context.Background(), cr.DeviceID)
	if d.State != "expired" || d.AppliedGeneration != 2 {
		t.Fatalf("request-free expiry failed: %+v", d)
	}
	if len(wg.state()) != 1 {
		t.Fatal("expired peer retained or protected peer removed")
	}
	wg.mu.Lock()
	wg.peers[testKey(1)] = []string{"10.66.0.30/32"}
	wg.mu.Unlock()
	restart, _ := NewScheduler(s, wg)
	if err = restart.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(wg.state()) != 1 {
		t.Fatal("restart resurrected revoked key")
	}
	if _, err = s.Challenge(context.Background(), ChallengeRequest{Purpose: "command.apply", Method: "POST", Path: "/api/v2/commands", BodyDigest: BodyDigest(nil), DeviceID: fresh.DeviceID, CredentialID: fresh.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired device reused credential")
	}
}
func TestSignedRevokeInvalidatesPendingChallenge(t *testing.T) {
	s, _, _ := newTestStore(t)
	cr, priv := activated(t, s)
	pending := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("apply"), "stale")
	revoke := request(t, s, cr, priv, "command.revoke", "POST", "/api/v2/commands", []byte("revoke"), "revoke")
	if _, err := s.SubmitSigned(context.Background(), revoke, command(cr.DeviceID, "revoke", 0, 0, "", "revoke")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitSigned(context.Background(), pending, command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "stale")); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old challenge resurrected revoke")
	}
}

func TestExactNonceExpiryOtherOwnerStatusAndPersistenceFailure(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	requestA := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("apply"), "expired-nonce")
	s.opts.Now = func() time.Time { return time.Unix(requestA.Expires, 0) }
	if _, err := s.SubmitSigned(context.Background(), requestA, command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "expired")); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("exact nonce expiry accepted")
	}
	s.opts.Now = func() time.Time { return testNow }
	requestA = request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("apply"), "real-apply")
	op, err := s.SubmitSigned(context.Background(), requestA, command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "apply"))
	if err != nil {
		t.Fatal(err)
	}
	other, otherPriv := activated(t, s)
	q := request(t, s, other, otherPriv, "operation.status", "GET", "/api/v2/operations/"+op.ID, nil, "other-status")
	if _, _, err = s.OperationSigned(context.Background(), q, op.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("other device operation leaked")
	}
	s.opts.Now = func() time.Time { return testNow.Add(time.Hour) }
	if _, err = db.Exec(`UPDATE deviceauth_meta SET epoch='unexpected-policy'`); err != nil {
		t.Fatal(err)
	}
	scheduler, _ := NewScheduler(s, newFake())
	if err = scheduler.Tick(context.Background()); !errors.Is(err, ErrExecutionUnknown) {
		t.Fatalf("expiry persistence failure hidden: %v", err)
	}
	if scheduler.Status().ErrorCode != "reconciliation_degraded" {
		t.Fatal("scheduler did not expose failure")
	}
}

func TestSignedRejectedOverduePreservesNonceRequestIDAndAudit(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	q := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("first"), "first")
	first, err := s.SubmitSigned(context.Background(), q, command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "first"))
	if err != nil {
		t.Fatal(err)
	}
	s.opts.Now = func() time.Time { return testNow.Add(31 * time.Second) }
	q = request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("blocked"), "blocked")
	cmd := command(cr.DeviceID, "apply", 1, 2, "10.66.0.30/32", "blocked")
	if _, err = s.SubmitSigned(context.Background(), q, cmd); !errors.Is(err, ErrOverdue) {
		t.Fatal(err)
	}
	var requests, audits, consumed int
	if err = db.QueryRow(`SELECT COUNT(*) FROM deviceauth_requests WHERE request_id='blocked'`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM deviceauth_audit WHERE request_id='blocked'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM deviceauth_challenges WHERE challenge_id=? AND consumed_at IS NOT NULL`, q.ChallengeID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || audits != 0 || consumed != 0 {
		t.Fatal("rejected grant consumed authority proof or wrote accepted audit")
	}
	old, err := s.Operation(context.Background(), first.ID)
	if err != nil || old.State != "degraded" || old.LastError != "deadline_exceeded" {
		t.Fatalf("rejected grant failed to persist overdue signal: %+v %v", old, err)
	}
	process(t, s, first, newFake())
	// The exact signed request, nonce and request ID remain usable after the
	// underlying old operation converges; rejection has not spent acceptance.
	if _, err = s.SubmitSigned(context.Background(), q, cmd); err != nil {
		t.Fatalf("same proof could not retry after recovery: %v", err)
	}
}
