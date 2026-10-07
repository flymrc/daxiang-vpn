package deviceauth

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"zongheng-vpn/shared/devicecontract"
)

func resolveProof(t *testing.T, s *Store, cr Credential, q ResolveRequest, priv, owner ed25519.PrivateKey) (SignedRequest, string, error) {
	t.Helper()
	body, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	challenge := ChallengeRequest{Purpose: "request.resolve", Method: "POST", Path: "/api/v2/requests/resolve", BodyDigest: BodyDigest(body), DeviceID: cr.DeviceID, CredentialID: cr.ID, ActivationCredential: q.ActivationCredential, PublicKey: q.PublicKey, ReceiptRequestID: q.RequestID, ReceiptKind: q.Kind, ReceiptIdempotencyKey: q.IdempotencyKey}
	challenge = testChallengeProof(t, s, priv, challenge)
	if len(owner) != 0 {
		challenge.ResolveOwnerProof = base64.StdEncoding.EncodeToString(ed25519.Sign(owner, devicecontract.ChallengeProofBytes(challenge.ProofDigest, challenge.ClientNonce, challenge.ClientExpires)))
	}
	ch, err := s.Challenge(context.Background(), challenge)
	if err != nil {
		return SignedRequest{}, "", err
	}
	id, err := opaque()
	if err != nil {
		t.Fatal(err)
	}
	r := SignedRequest{Purpose: challenge.Purpose, Method: challenge.Method, Path: challenge.Path, BodyDigest: challenge.BodyDigest, DeviceID: cr.DeviceID, CredentialID: cr.ID, ChallengeID: ch.ID, Nonce: ch.Nonce, Expires: ch.Expires, RequestID: id}
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(r)))
	var ownerProof string
	if len(owner) != 0 {
		ownerProof = base64.StdEncoding.EncodeToString(ed25519.Sign(owner, SigningBytes(r)))
	}
	return r, ownerProof, nil
}

func resolve(t *testing.T, s *Store, cr Credential, q ResolveRequest, priv, owner ed25519.PrivateKey) ResolveReceipt {
	t.Helper()
	r, proof, err := resolveProof(t, s, cr, q, priv, owner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ResolvePending(context.Background(), r, q, proof)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func tableCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCancelledActivationRejectsDelayedOriginalAndSurvivesReopen(t *testing.T) {
	s, db, path := newTestStore(t)
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, priv := authPair(t)
	original := activationRequest(t, s, a.Credential, key, priv)
	q := ResolveRequest{Kind: "activate", RequestID: original.RequestID, PublicKey: key, ActivationCredential: a.Credential}
	for i := 0; i < 3; i++ {
		got := resolve(t, s, Credential{}, q, priv, nil)
		if got.State != "cancelled" || got.Credential != nil || got.Operation != nil || got.PublicKey != key {
			t.Fatal("uncommitted activation returned incorrect negative receipt")
		}
	}
	if tableCount(t, db, "deviceauth_cancellations") != 1 || tableCount(t, db, "deviceauth_audit") != 1 || tableCount(t, db, "deviceauth_requests") != 0 || tableCount(t, db, "deviceauth_devices") != 0 {
		t.Fatal("cancel replay grew permanent history or created authority")
	}
	s2 := reopen(t, path)
	if _, err := s2.Activate(context.Background(), original, a.Credential, key); !errors.Is(err, ErrConflict) {
		t.Fatalf("late activation crossed durable cancel: %v", err)
	}
	var consumed sql.NullInt64
	if err := db.QueryRow(`SELECT consumed_at FROM deviceauth_challenges WHERE challenge_id=?`, original.ChallengeID).Scan(&consumed); err != nil || consumed.Valid {
		t.Fatal("rejected late activation consumed nonce")
	}
	// The RID is reserved across prospective-key substitutions, not only the
	// exact public key in the cancelled request.
	otherKey, otherPriv := authPair(t)
	altered := activationRequest(t, s2, a.Credential, otherKey, otherPriv)
	if _, err := s2.Activate(context.Background(), altered, a.Credential, otherKey); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled principal RID accepted another key")
	}
	wrong := q
	wrong.ActivationCredential = "invalid"
	if _, _, err := resolveProof(t, s, Credential{}, wrong, priv, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("activation cancellation allowed without original secret")
	}
	// Even after its admission deadline, negative recovery is still permitted
	// and cannot create or extend any credential.
	s.opts.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if got := resolve(t, s, Credential{}, q, priv, nil); got.State != "cancelled" {
		t.Fatal("expired activation prevented negative recovery")
	}
}

func TestCancelledRotationRequiresBothKeysAndOriginalTarget(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, oldPriv := activated(t, s)
	key, priv := authPair(t)
	original := request(t, s, cr, oldPriv, "credential.rotate", "POST", "/api/v2/credentials/rotate", []byte("rotation"), "rotation-unknown")
	newProof := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(original)))
	q := ResolveRequest{Kind: "credential.rotate", RequestID: original.RequestID, PublicKey: key}
	if _, _, err := resolveProof(t, s, cr, q, priv, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("new key alone could reserve another owner's rotation RID")
	}
	r, ownerProof, err := resolveProof(t, s, cr, q, priv, oldPriv)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPriv := authPair(t)
	badOwner := base64.StdEncoding.EncodeToString(ed25519.Sign(wrongPriv, SigningBytes(r)))
	if _, err := s.ResolvePending(context.Background(), r, q, badOwner); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong old key authorized cancellation")
	}
	wrongTarget := q
	wrongTarget.RequestID = "different-original"
	if _, err := s.ResolvePending(context.Background(), r, wrongTarget, ownerProof); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("resolve ignored nonce's original request binding")
	}
	got, err := s.ResolvePending(context.Background(), r, q, ownerProof)
	if err != nil || got.State != "cancelled" {
		t.Fatalf("correct proof could not reuse nonce after failed checks: %v", err)
	}
	if _, err := s.RotateCredential(context.Background(), original, key, newProof); !errors.Is(err, ErrConflict) {
		t.Fatal("late rotation crossed cancellation")
	}
	if tableCount(t, db, "deviceauth_credentials") != 1 {
		t.Fatal("cancel created prospective credential")
	}
	// A distinct subsequent rotation can commit; resolution returns exactly its
	// historical receipt, even after replacement/expiry, without extending it.
	key2, priv2 := authPair(t)
	second := request(t, s, cr, oldPriv, "credential.rotate", "POST", "/api/v2/credentials/rotate", []byte("second"), "rotation-committed")
	proof2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, SigningBytes(second)))
	rotated, err := s.RotateCredential(context.Background(), second, key2, proof2)
	if err != nil {
		t.Fatal(err)
	}
	s.opts.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	q2 := ResolveRequest{Kind: "credential.rotate", RequestID: second.RequestID, PublicKey: key2}
	for i := 0; i < 2; i++ {
		got := resolve(t, s, cr, q2, priv2, oldPriv)
		if got.State != "committed" || got.Credential == nil || *got.Credential != rotated || got.Operation != nil {
			t.Fatal("committed rotation resolved as a different grant")
		}
	}
	if tableCount(t, db, "deviceauth_cancellations") != 1 || tableCount(t, db, "deviceauth_credentials") != 2 {
		t.Fatal("committed resolution altered authority")
	}
}

func TestCommittedActivationResolveReturnsOnlyHistoricalReceipt(t *testing.T) {
	s, db, _ := newTestStore(t)
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, priv := authPair(t)
	original := activationRequest(t, s, a.Credential, key, priv)
	cr, err := s.Activate(context.Background(), original, a.Credential, key)
	if err != nil {
		t.Fatal(err)
	}
	s.opts.Now = func() time.Time { return testNow.Add(48 * time.Hour) }
	q := ResolveRequest{Kind: "activate", RequestID: original.RequestID, PublicKey: key, ActivationCredential: a.Credential}
	for i := 0; i < 2; i++ {
		got := resolve(t, s, Credential{}, q, priv, nil)
		if got.State != "committed" || got.Credential == nil || *got.Credential != cr || got.Operation != nil {
			t.Fatal("historical activation receipt granted a different authority")
		}
	}
	wrongKey, wrongPriv := authPair(t)
	wrong := q
	wrong.PublicKey = wrongKey
	r, proof, err := resolveProof(t, s, Credential{}, wrong, wrongPriv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePending(context.Background(), r, wrong, proof); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong prospective key exposed accepted activation or cancelled it")
	}
	if tableCount(t, db, "deviceauth_credentials") != 1 || tableCount(t, db, "deviceauth_cancellations") != 0 || tableCount(t, db, "deviceauth_audit") != 1 || tableCount(t, db, "deviceauth_requests") != 1 {
		t.Fatal("historical receipt mutated permanent authority history")
	}
}

func TestCancelledCommandCannotLaterApplyOrChangeKind(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	cmd := command(cr.DeviceID, "apply", 0, 100, "10.66.0.60/32", "cancelled-idem")
	original := request(t, s, cr, priv, "command.apply", "POST", "/api/v2/commands", []byte("apply"), "command-unknown")
	q := ResolveRequest{Kind: "command.apply", RequestID: original.RequestID, IdempotencyKey: cmd.IdempotencyKey}
	for i := 0; i < 2; i++ {
		if got := resolve(t, s, cr, q, priv, nil); got.State != "cancelled" || got.IdempotencyKey != cmd.IdempotencyKey || got.Operation != nil || got.Credential != nil {
			t.Fatal("command cancel receipt mismatch")
		}
	}
	if _, err := s.SubmitSigned(context.Background(), original, cmd); !errors.Is(err, ErrConflict) {
		t.Fatal("late command crossed cancellation")
	}
	changed := request(t, s, cr, priv, "command.revoke", "POST", "/api/v2/commands", []byte("revoke"), original.RequestID)
	if _, err := s.SubmitSigned(context.Background(), changed, Command{Action: "revoke", IdempotencyKey: "altered-kind", ExpectedGeneration: 0}); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled principal RID accepted altered command kind")
	}
	if tableCount(t, db, "deviceauth_operations") != 0 || tableCount(t, db, "deviceauth_cancellations") != 1 {
		t.Fatal("cancellation mutated WG desired state")
	}
	// Self revoke can still use the independent safety reserve. Its original
	// receipt can then be resolved by the revoked key, never falsely cancelled.
	revoke := request(t, s, cr, priv, "command.revoke", "POST", "/api/v2/commands", []byte("actual revoke"), "actual-revoke")
	op, err := s.SubmitSigned(context.Background(), revoke, Command{Action: "revoke", IdempotencyKey: "actual-revoke-idem", ExpectedGeneration: 0})
	if err != nil {
		t.Fatal(err)
	}
	q2 := ResolveRequest{Kind: "command.revoke", RequestID: revoke.RequestID, IdempotencyKey: "actual-revoke-idem"}
	got := resolve(t, s, cr, q2, priv, nil)
	if got.State != "committed" || got.Operation == nil || got.Operation.ID != op.ID || got.Credential != nil {
		t.Fatal("revoked historical key lost accepted receipt")
	}
	q2.IdempotencyKey = "wrong-idempotency"
	r, proof, err := resolveProof(t, s, cr, q2, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePending(context.Background(), r, q2, proof); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong idem for an accepted RID produced false cancellation")
	}
}

func TestResolveCancelAndCommitSerializeAcrossStores(t *testing.T) {
	for _, kind := range []string{"activate", "credential.rotate", "command.revoke"} {
		for trial := 0; trial < 4; trial++ {
			t.Run(fmt.Sprintf("%s/%d", kind, trial), func(t *testing.T) {
				s, db, path := newTestStore(t)
				s2 := reopen(t, path)
				var cr Credential
				var original SignedRequest
				var priv, owner ed25519.PrivateKey
				var commit func() error
				q := ResolveRequest{Kind: kind, RequestID: "race-original"}
				switch kind {
				case "activate":
					a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
					if err != nil {
						t.Fatal(err)
					}
					q.PublicKey, priv = authPair(t)
					q.ActivationCredential = a.Credential
					original = activationRequest(t, s, a.Credential, q.PublicKey, priv)
					original.RequestID = q.RequestID
					original.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(original)))
					commit = func() error {
						_, err := s.Activate(context.Background(), original, a.Credential, q.PublicKey)
						return err
					}
				case "credential.rotate":
					cr, owner = activated(t, s)
					q.PublicKey, priv = authPair(t)
					original = request(t, s, cr, owner, kind, "POST", "/api/v2/credentials/rotate", []byte("race"), q.RequestID)
					newProof := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SigningBytes(original)))
					commit = func() error {
						_, err := s.RotateCredential(context.Background(), original, q.PublicKey, newProof)
						return err
					}
				default:
					cr, priv = activated(t, s)
					q.IdempotencyKey = "race-idem"
					original = request(t, s, cr, priv, kind, "POST", "/api/v2/commands", []byte("race"), q.RequestID)
					commit = func() error {
						_, err := s.SubmitSigned(context.Background(), original, Command{Action: "revoke", IdempotencyKey: q.IdempotencyKey, ExpectedGeneration: 0})
						return err
					}
				}
				r, proof, err := resolveProof(t, s2, cr, q, priv, owner)
				if err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				var mutationErr, resolveErr error
				var got ResolveReceipt
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); <-start; mutationErr = commit() }()
				go func() {
					defer wg.Done()
					<-start
					got, resolveErr = s2.ResolvePending(context.Background(), r, q, proof)
				}()
				close(start)
				wg.Wait()
				if resolveErr != nil {
					t.Fatalf("resolution lost serialization: %v", resolveErr)
				}
				if mutationErr == nil {
					if got.State != "committed" || tableCount(t, db, "deviceauth_cancellations") != 0 {
						t.Fatal("committed request also received cancellation")
					}
				} else if !errors.Is(mutationErr, ErrConflict) || got.State != "cancelled" || tableCount(t, db, "deviceauth_cancellations") != 1 {
					t.Fatalf("cancelled request could still commit: %v", mutationErr)
				}
			})
		}
	}
}

func TestCancellationReserveBoundedAndRetryableWithoutGrantQuota(t *testing.T) {
	s, db, _ := newTestStore(t)
	cr, priv := activated(t, s)
	s.opts.Resources.Requests = 1 // activation already exhausted positive quota
	s.opts.Resources.Cancellations = 2
	q := ResolveRequest{Kind: "command.apply", RequestID: "cancel-0", IdempotencyKey: "idem-0"}
	for i := 0; i < 2; i++ {
		q.RequestID, q.IdempotencyKey = fmt.Sprintf("cancel-%d", i), fmt.Sprintf("idem-%d", i)
		if got := resolve(t, s, cr, q, priv, nil); got.State != "cancelled" {
			t.Fatal("grant quota blocked negative safety resolution")
		}
	}
	q.RequestID, q.IdempotencyKey = "cancel-exhausted", "idem-exhausted"
	r, proof, err := resolveProof(t, s, cr, q, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePending(context.Background(), r, q, proof); !errors.Is(err, ErrQuota) {
		t.Fatal("permanent cancellation history unbounded")
	}
	var consumed sql.NullInt64
	if err := db.QueryRow(`SELECT consumed_at FROM deviceauth_challenges WHERE challenge_id=?`, r.ChallengeID).Scan(&consumed); err != nil || consumed.Valid {
		t.Fatal("quota rejection lost retryable nonce")
	}
	qOld := ResolveRequest{Kind: "command.apply", RequestID: "cancel-0", IdempotencyKey: "idem-0"}
	if got := resolve(t, s, cr, qOld, priv, nil); got.State != "cancelled" {
		t.Fatal("full quota prevented recovery of committed negative receipt")
	}
	if tableCount(t, db, "deviceauth_cancellations") != 2 || tableCount(t, db, "deviceauth_audit") != 3 || tableCount(t, db, "deviceauth_requests") != 1 {
		t.Fatal("negative receipt replay consumed permanent reserve")
	}
	// Test-only ceiling adjustment demonstrates the exact rejected nonce can
	// retry; the real host exposes no API/environment that raises this ceiling.
	s.opts.Resources.Cancellations = 3
	if got, err := s.ResolvePending(context.Background(), r, q, proof); err != nil || got.State != "cancelled" {
		t.Fatal("quota rejection consumed original resolution proof")
	}
	revoke := request(t, s, cr, priv, "command.revoke", "POST", "/api/v2/commands", nil, "safety-after-quota")
	if _, err := s.SubmitSigned(context.Background(), revoke, Command{Action: "revoke", IdempotencyKey: "safety-after-quota", ExpectedGeneration: 0}); err != nil {
		t.Fatal("negative reserve exhaustion blocked safety revoke")
	}
}

func TestCancellationPrincipalReserveCannotBeExpandedWithNewKeys(t *testing.T) {
	s, db, _ := newTestStore(t)
	a, err := s.IssueActivation(context.Background(), "owner", testNow.Add(time.Hour), testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 17; i++ {
		key, priv := authPair(t)
		q := ResolveRequest{Kind: "activate", RequestID: fmt.Sprintf("new-key-cancel-%d", i), PublicKey: key, ActivationCredential: a.Credential}
		r, proof, err := resolveProof(t, s, Credential{}, q, priv, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ResolvePending(context.Background(), r, q, proof)
		if i < 16 && err != nil {
			t.Fatal(err)
		}
		if i == 16 && !errors.Is(err, ErrQuota) {
			t.Fatal("activation's private-key churn expanded permanent reserve")
		}
	}
	if tableCount(t, db, "deviceauth_cancellations") != 16 || tableCount(t, db, "deviceauth_devices") != 0 {
		t.Fatal("principal cap or negative-only contract failed")
	}
}

func TestDraftV1AuthorityRejectedBeforeSchemaMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	db := openTestDB(t, path)
	if _, err := db.Exec(`CREATE TABLE deviceauth_meta(singleton INTEGER PRIMARY KEY,schema_version INTEGER,epoch TEXT,managed_by TEXT,policy_json TEXT,fence_path TEXT); INSERT INTO deviceauth_meta VALUES(1,1,'old-epoch','old-manager','old-policy','old-fence'); CREATE TABLE v1_preserved(value TEXT); INSERT INTO v1_preserved VALUES('immutable-original-row')`); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), db, testOptions()); !errors.Is(err, ErrPolicy) {
		t.Fatalf("old draft was adopted: %v", err)
	}
	var version int
	var epoch, manager, policy, fence, value string
	if err := db.QueryRow(`SELECT schema_version,epoch,managed_by,policy_json,fence_path FROM deviceauth_meta`).Scan(&version, &epoch, &manager, &policy, &fence); err != nil || version != 1 || epoch != "old-epoch" || manager != "old-manager" || policy != "old-policy" || fence != "old-fence" {
		t.Fatal("v1 authority metadata changed on refusal")
	}
	if err := db.QueryRow(`SELECT value FROM v1_preserved`).Scan(&value); err != nil || value != "immutable-original-row" {
		t.Fatal("v1 original rows were altered")
	}
	if tableCount(t, db, "sqlite_master") != 2 {
		t.Fatal("schema refusal created new authority tables/indexes")
	}
}
