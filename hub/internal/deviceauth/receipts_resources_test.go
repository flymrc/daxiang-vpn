package deviceauth

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestChallengeActivationKeyChurnCannotBypassPrincipalBudget(t *testing.T) {
	s, db, _ := newTestStore(t)
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 33; i++ {
		key, priv := authPair(t)
		r := ChallengeRequest{Purpose: "activate", Method: "POST", Path: "/api/v2/activate", BodyDigest: BodyDigest(nil), ActivationCredential: a.Credential, PublicKey: key}
		_, err := testChallenge(t, s, priv, r)
		if i < 32 && err != nil {
			t.Fatal(err)
		}
		if i == 32 && !errors.Is(err, ErrQuota) {
			t.Fatal("new public keys expanded one activation's nonce budget")
		}
	}
	// Explicit negative resolution has four reserved nonce slots after the
	// regular budget fills; its new key cannot exceed that stable principal cap.
	for i := 0; i < 5; i++ {
		key, priv := authPair(t)
		r := ChallengeRequest{Purpose: "request.resolve", Method: "POST", Path: "/api/v2/requests/resolve", BodyDigest: BodyDigest(nil), ActivationCredential: a.Credential, PublicKey: key, ReceiptKind: "activate", ReceiptRequestID: fmt.Sprintf("cancel-%d", i)}
		_, err := testChallenge(t, s, priv, r)
		if i < 4 && err != nil {
			t.Fatal("regular nonce quota consumed negative reserve")
		}
		if i == 4 && !errors.Is(err, ErrQuota) {
			t.Fatal("negative nonce reserve unbounded")
		}
	}
	if tableCount(t, db, "deviceauth_challenges") != 36 || tableCount(t, db, "deviceauth_devices") != 0 {
		t.Fatal("key churn or challenge issuance altered permanent authority")
	}
}

func receiptRequest(t *testing.T, s *Store, cr Credential, priv ed25519.PrivateKey, purpose, originalID, kind, idempotency, queryID string) (SignedRequest, error) {
	t.Helper()
	r := ChallengeRequest{Purpose: purpose, Method: "POST", Path: "/api/v2/credentials/receipt", BodyDigest: BodyDigest([]byte("synthetic receipt body")), ReceiptRequestID: originalID, ReceiptKind: kind, PublicKey: cr.PublicKey}
	if purpose == "operation.receipt" {
		r.Path = "/api/v2/operations/receipt"
		r.DeviceID = cr.DeviceID
		r.CredentialID = cr.ID
		r.PublicKey = ""
		r.ReceiptKind = ""
		r.ReceiptIdempotencyKey = idempotency
	}
	ch, err := testChallenge(t, s, priv, r)
	if err != nil {
		return SignedRequest{}, err
	}
	q := SignedRequest{Purpose: purpose, Method: r.Method, Path: r.Path, BodyDigest: r.BodyDigest, DeviceID: r.DeviceID, CredentialID: r.CredentialID, ChallengeID: ch.ID, Nonce: ch.Nonce, Expires: ch.Expires, RequestID: queryID}
	q.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(q)))
	return q, nil
}

func TestChallengePoPReplayExpiryAndBoundedRetention(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	base := ChallengeRequest{Purpose: "command.apply", Method: "POST", Path: "/api/v2/commands", BodyDigest: BodyDigest(nil), DeviceID: cr.DeviceID, CredentialID: cr.ID}
	if _, err := s.Challenge(context.Background(), base); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("public identifiers issued unsigned challenge")
	}
	for cycle := 0; cycle < 3; cycle++ {
		now := testNow.Add(time.Duration(cycle+1) * 61 * time.Second)
		s.opts.Now = func() time.Time { return now }
		for i := 0; i < 32; i++ {
			r := testChallengeProof(t, s, priv, base)
			first, err := s.Challenge(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := s.Challenge(context.Background(), r)
			if err != nil || replayed != first {
				t.Fatal("same proof created different nonce or row")
			}
		}
		var retained int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deviceauth_challenges`).Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if retained != 32 {
			t.Fatalf("expired proof history accumulated: %d", retained)
		}
		if _, err := testChallenge(t, s, priv, base); !errors.Is(err, ErrQuota) {
			t.Fatalf("principal retained challenge limit bypass: %v", err)
		}
	}
	r := testChallengeProof(t, s, priv, base)
	_, wrong := authPair(t)
	r.Proof = base64.StdEncoding.EncodeToString(ed25519.Sign(wrong, []byte("wrong proof")))
	if _, err := s.Challenge(context.Background(), r); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong key consumed legitimate nonce budget")
	}
	r = testChallengeProof(t, s, priv, base)
	r.ClientExpires = s.opts.Now().Add(61 * time.Second).Unix()
	if _, err := s.Challenge(context.Background(), r); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("long-lived proof accepted")
	}
	s.opts.Now = func() time.Time { return testNow.Add(10 * time.Minute) }
	if err := s.SweepChallenges(context.Background()); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deviceauth_challenges`).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("scheduler nonce sweep did not reclaim expired state")
	}
}

func TestCredentialReceiptsRecoverResponseLossWithoutNewAuthority(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	q, err := receiptRequest(t, s, cr, priv, "credential.receipt", "activation-request", "activate", "", "activation-recovery")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.CredentialReceipt(context.Background(), q, "activate", "activation-request", cr.PublicKey)
	if err != nil || recovered != cr {
		t.Fatalf("activation receipt failed: %v", err)
	}
	if _, err = s.CredentialReceipt(context.Background(), q, "activate", "activation-request", cr.PublicKey); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("receipt nonce replay accepted")
	}
	newKey, newPriv := authPair(t)
	rotate := request(t, s, cr, priv, "credential.rotate", "POST", "/api/v2/credentials/rotate", []byte("new-key"), "lost-rotation")
	fresh, err := s.RotateCredential(context.Background(), rotate, newKey, base64.StdEncoding.EncodeToString(ed25519.Sign(newPriv, SigningBytes(rotate))))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		q, err = receiptRequest(t, s, fresh, newPriv, "credential.receipt", "lost-rotation", "credential.rotate", "", "rotation-recovery")
		if err != nil {
			t.Fatal(err)
		}
		recovered, err = s.CredentialReceipt(context.Background(), q, "credential.rotate", "lost-rotation", newKey)
		if err != nil || recovered != fresh || !recovered.ValidUntil.Equal(cr.ValidUntil) {
			t.Fatalf("rotation recovery altered authority: %v", err)
		}
	}
	if _, err = receiptRequest(t, s, cr, priv, "credential.receipt", "activation-request", "activate", "", "old-recovery"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("replaced credential receipt revived old credential")
	}
	q, err = receiptRequest(t, s, fresh, newPriv, "credential.receipt", "lost-rotation", "credential.rotate", "", "wrong-binding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CredentialReceipt(context.Background(), q, "credential.rotate", "other-request", newKey); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("receipt request binding missing")
	}
	wrongKey, wrongPriv := authPair(t)
	fake := Credential{PublicKey: wrongKey}
	if _, err = receiptRequest(t, s, fake, wrongPriv, "credential.receipt", "lost-rotation", "credential.rotate", "", "wrong-key"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong key disclosed receipt: %v", err)
	}
	var creds, requests, audits int
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_credentials`).Scan(&creds)
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_requests`).Scan(&requests)
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_audit`).Scan(&audits)
	if creds != 2 || requests != 2 || audits != 2 {
		t.Fatalf("reads accumulated permanent authority records %d/%d/%d", creds, requests, audits)
	}
}

func TestQuotaBlocksGrantsButRetainsSelfRevokeAndHistoricalReceipt(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	s.opts.Resources.Requests = 1
	grant := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("grant"), "quota-grant")
	if _, err := s.SubmitSigned(context.Background(), grant, command(cr.DeviceID, "apply", 0, 1, "10.66.0.30/32", "quota")); !errors.Is(err, ErrQuota) {
		t.Fatalf("grant high-water not enforced: %v", err)
	}
	var consumed int
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_requests WHERE request_id='quota-grant'`).Scan(&consumed)
	if consumed != 0 {
		t.Fatal("quota rejection consumed mutation receipt")
	}
	revoke := request(t, s, cr, priv, "command.revoke", "POST", "/api/v2/commands", []byte("revoke"), "lost-revoke")
	op, err := s.SubmitSigned(context.Background(), revoke, command(cr.DeviceID, "revoke", 0, 0, "", "revoke-idempotency"))
	if err != nil {
		t.Fatalf("quota prevented safety revoke: %v", err)
	}
	process(t, s, op, newFake())
	for i := 0; i < 2; i++ {
		q, err := receiptRequest(t, s, cr, priv, "operation.receipt", "lost-revoke", "", "revoke-idempotency", "recovery")
		if err != nil {
			t.Fatal(err)
		}
		observed, effective, err := s.OperationReceipt(context.Background(), q, "lost-revoke", "revoke-idempotency")
		if err != nil || observed.ID != op.ID || !effective || observed.State != "done" {
			t.Fatalf("self-revoke response loss not recoverable: %v", err)
		}
	}
	if _, err := testChallenge(t, s, priv, ChallengeRequest{Purpose: "command.apply", Method: "POST", Path: "/api/v2/commands", BodyDigest: BodyDigest(nil), DeviceID: cr.DeviceID, CredentialID: cr.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("historical receipt enabled new grants")
	}
	if _, err := receiptRequest(t, s, cr, priv, "operation.receipt", "wrong-request", "", "revoke-idempotency", "wrong-request"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("wrong mutation request received historical operation")
	}
	if _, err := receiptRequest(t, s, cr, priv, "operation.receipt", "lost-revoke", "", "wrong-idempotency", "wrong-key"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("wrong idempotency received historical operation")
	}
	var operations, requests, audits int
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_operations`).Scan(&operations)
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_requests`).Scan(&requests)
	db.QueryRow(`SELECT COUNT(*) FROM deviceauth_audit`).Scan(&audits)
	if operations != 1 || requests != 2 || audits != 2 {
		t.Fatal("historical recovery wrote new mutation history")
	}
}
