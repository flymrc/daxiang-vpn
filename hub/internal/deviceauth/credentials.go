package deviceauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/signingkey"
)

// Activation is returned only to a trusted local issuer. Its random secret is
// never persisted. The bounded lifetime and customer owner come from that issuer.
type Activation struct {
	Credential                  string
	ValidUntil, ActivationUntil time.Time
}
type Credential struct {
	DeviceID, ID, PublicKey string
	ValidUntil              time.Time
}
type ChallengeRequest struct {
	Purpose, Method, Path, BodyDigest, DeviceID, CredentialID, ActivationCredential, PublicKey string
	ClientNonce, ProofDigest, Proof                                                            string
	ClientExpires                                                                              int64
	ReceiptRequestID, ReceiptKind, ReceiptIdempotencyKey                                       string
	ResolveOwnerProof                                                                          string
}
type Challenge struct {
	ID, Nonce string
	Expires   int64
}
type SignedRequest struct {
	Purpose, Method, Path, BodyDigest, DeviceID, CredentialID, ChallengeID, Nonce, RequestID string
	Expires                                                                                  int64
	Signature                                                                                string
}

// SigningBytes is a versioned, unambiguous array, not concatenated attacker text.
// BodyDigest is SHA256 of exact raw HTTP bytes, including the empty GET body.
func SigningBytes(r SignedRequest) []byte {
	b, _ := json.Marshal([]string{"zhvpn-device-request", "v2", r.Purpose, r.Method, r.Path, r.BodyDigest, r.DeviceID, r.CredentialID, r.ChallengeID, r.Nonce, r.RequestID, fmt.Sprint(r.Expires)})
	return b
}
func BodyDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func opaque() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func authKey(key string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(b) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(b) != key {
		return nil, ErrInvalid
	}
	if !signingkey.ValidEd25519PublicKey(b) {
		return nil, ErrInvalid
	}
	return ed25519.PublicKey(b), nil
}
func signatureOK(key string, r SignedRequest, signature string) bool {
	return proofOK(key, SigningBytes(r), signature)
}
func proofOK(key string, payload []byte, signature string) bool {
	pub, err := authKey(key)
	sig, e := base64.StdEncoding.DecodeString(signature)
	return err == nil && e == nil && len(sig) == ed25519.SignatureSize && base64.StdEncoding.EncodeToString(sig) == signature && ed25519.Verify(pub, payload, sig)
}
func activationParts(value string) (string, string, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(parts[0]) != 32 {
		return "", "", ErrUnauthorized
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != parts[1] {
		return "", "", ErrUnauthorized
	}
	return parts[0], BodyDigest(secret), nil
}

// IssueActivation is a trusted local administration boundary, not an HTTP route.
func (s *Store) IssueActivation(ctx context.Context, owner string, validUntil, activationUntil time.Time) (Activation, error) {
	now := s.opts.Now().UTC()
	if !identifier(owner) || !validUntil.After(now) || validUntil.Year() > 2200 || !activationUntil.After(now) || activationUntil.After(now.Add(24*time.Hour)) || activationUntil.After(validUntil) {
		return Activation{}, ErrInvalid
	}
	id, err := opaque()
	if err != nil {
		return Activation{}, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return Activation{}, err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return Activation{}, err
	}
	defer unlock()
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		if err := s.grantResources(ctx, c); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `INSERT INTO deviceauth_activations(activation_id,verifier,owner_id,valid_until,activation_until) VALUES(?,?,?,?,?)`, id, BodyDigest(secret), owner, validUntil.UTC().UnixNano(), activationUntil.UTC().UnixNano())
		return err
	})
	return Activation{Credential: id + "." + base64.RawURLEncoding.EncodeToString(secret), ValidUntil: validUntil.UTC(), ActivationUntil: activationUntil.UTC()}, err
}
func (s *Store) activation(ctx context.Context, c *sql.Conn, material string) (string, string, int64, error) {
	id, verifier, err := activationParts(material)
	if err != nil {
		return "", "", 0, err
	}
	var stored, owner string
	var until, activationUntil int64
	var consumed sql.NullString
	err = c.QueryRowContext(ctx, `SELECT verifier,owner_id,valid_until,activation_until,consumed_device FROM deviceauth_activations WHERE activation_id=?`, id).Scan(&stored, &owner, &until, &activationUntil, &consumed)
	now := s.opts.Now().UnixNano()
	if err != nil || consumed.Valid || now >= until || now >= activationUntil || subtle.ConstantTimeCompare([]byte(stored), []byte(verifier)) != 1 {
		return "", "", 0, ErrUnauthorized
	}
	return id, owner, until, nil
}
func validPurpose(r ChallengeRequest) bool {
	switch r.Purpose {
	case "activate":
		return r.Method == "POST" && r.Path == "/api/v2/activate" && r.DeviceID == "" && r.CredentialID == ""
	case "command.apply", "command.disable", "command.revoke":
		return r.Method == "POST" && r.Path == "/api/v2/commands"
	case "credential.rotate":
		return r.Method == "POST" && r.Path == "/api/v2/credentials/rotate"
	case "credential.receipt":
		return r.Method == "POST" && r.Path == "/api/v2/credentials/receipt" && r.DeviceID == "" && r.CredentialID == ""
	case "operation.receipt":
		return r.Method == "POST" && r.Path == "/api/v2/operations/receipt"
	case "proxy.bootstrap":
		return r.Method == "POST" && r.Path == "/api/v2/proxy/bootstrap"
	case "request.resolve":
		return r.Method == "POST" && r.Path == "/api/v2/requests/resolve"
	case "operation.status":
		id := strings.TrimPrefix(r.Path, "/api/v2/operations/")
		decoded, err := hex.DecodeString(id)
		return r.Method == "GET" && strings.HasPrefix(r.Path, "/api/v2/operations/") && err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == id
	default:
		return false
	}
}
func (s *Store) credential(ctx context.Context, c *sql.Conn, deviceID, credentialID string) (Credential, Actor, error) {
	var cr Credential
	var owner, state string
	var until, deviceUntil int64
	var revoked sql.NullInt64
	err := c.QueryRowContext(ctx, `SELECT c.credential_id,c.device_id,c.public_key,c.valid_until,c.revoked_at,d.owner_id,d.state,d.valid_until FROM deviceauth_credentials c JOIN deviceauth_devices d USING(device_id) WHERE c.credential_id=? AND c.device_id=?`, credentialID, deviceID).Scan(&cr.ID, &cr.DeviceID, &cr.PublicKey, &until, &revoked, &owner, &state, &deviceUntil)
	now := s.opts.Now().UnixNano()
	if err != nil || revoked.Valid || state != "active" || now >= until || now >= deviceUntil {
		return Credential{}, Actor{}, ErrUnauthorized
	}
	cr.ValidUntil = time.Unix(0, until).UTC()
	return cr, Actor{ID: deviceID, OwnerID: owner}, nil
}
func (s *Store) Challenge(ctx context.Context, r ChallengeRequest) (Challenge, error) {
	digest, err := hex.DecodeString(r.BodyDigest)
	if !validPurpose(r) || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != r.BodyDigest {
		return Challenge{}, ErrInvalid
	}
	now := s.opts.Now().Unix()
	clientNonce, err := hex.DecodeString(r.ClientNonce)
	proofDigest, digestErr := hex.DecodeString(r.ProofDigest)
	if err != nil || len(clientNonce) != 16 || hex.EncodeToString(clientNonce) != r.ClientNonce || digestErr != nil || len(proofDigest) != 32 || hex.EncodeToString(proofDigest) != r.ProofDigest || r.ClientExpires <= now || r.ClientExpires > now+60 {
		return Challenge{}, ErrUnauthorized
	}
	id, err := opaque()
	if err != nil {
		return Challenge{}, err
	}
	nonce, err := opaque()
	if err != nil {
		return Challenge{}, err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return Challenge{}, err
	}
	defer unlock()
	ch := Challenge{ID: id, Nonce: nonce, Expires: r.ClientExpires}
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		if r.ClientExpires <= s.opts.Now().Unix() || r.ClientExpires > s.opts.Now().Unix()+60 {
			return ErrUnauthorized
		}
		var activationID, key string
		if r.Purpose != "credential.receipt" && r.Purpose != "operation.receipt" && r.Purpose != "request.resolve" && (r.ReceiptRequestID != "" || r.ReceiptKind != "" || r.ReceiptIdempotencyKey != "") {
			return ErrInvalid
		}
		if (r.Purpose != "request.resolve" || r.ReceiptKind != "credential.rotate") && r.ResolveOwnerProof != "" {
			return ErrInvalid
		}
		if r.Purpose == "activate" {
			var until int64
			activationID, _, until, err = s.activation(ctx, c, r.ActivationCredential)
			if err != nil {
				return err
			}
			if _, err = authKey(r.PublicKey); err != nil {
				return err
			}
			key = r.PublicKey
			if ch.Expires > time.Unix(0, until).Unix() {
				ch.Expires = time.Unix(0, until).Unix()
			}
		} else if r.Purpose == "request.resolve" {
			var ownerKey string
			activationID, key, ownerKey, err = s.resolveIdentity(ctx, c, r.DeviceID, r.CredentialID, ResolveRequest{Kind: r.ReceiptKind, RequestID: r.ReceiptRequestID, IdempotencyKey: r.ReceiptIdempotencyKey, PublicKey: r.PublicKey, ActivationCredential: r.ActivationCredential})
			if err != nil {
				return err
			}
			if r.ReceiptKind == "credential.rotate" && !proofOK(ownerKey, devicecontract.ChallengeProofBytes(r.ProofDigest, r.ClientNonce, r.ClientExpires), r.ResolveOwnerProof) {
				return ErrUnauthorized
			}
		} else if r.Purpose == "credential.receipt" {
			if r.ActivationCredential != "" || r.ReceiptIdempotencyKey != "" || !receiptKind(r.ReceiptKind) || !identifier(r.ReceiptRequestID) || len(r.ReceiptRequestID) > 128 {
				return ErrInvalid
			}
			key = r.PublicKey
		} else if r.Purpose == "operation.receipt" {
			if r.ActivationCredential != "" || r.PublicKey != "" || r.ReceiptKind != "" || !identifier(r.ReceiptRequestID) || len(r.ReceiptRequestID) > 128 || !identifier(r.ReceiptIdempotencyKey) || len(r.ReceiptIdempotencyKey) > 128 {
				return ErrInvalid
			}
			cr, err := s.historicalCredential(ctx, c, r.DeviceID, r.CredentialID)
			if err != nil {
				return err
			}
			key = cr.PublicKey
		} else {
			if r.ActivationCredential != "" || r.PublicKey != "" {
				return ErrInvalid
			}
			cr, _, err := s.credential(ctx, c, r.DeviceID, r.CredentialID)
			if err != nil {
				return err
			}
			key = cr.PublicKey
			if ch.Expires > cr.ValidUntil.Unix() {
				ch.Expires = cr.ValidUntil.Unix()
			}
		}
		if !proofOK(key, devicecontract.ChallengeProofBytes(r.ProofDigest, r.ClientNonce, r.ClientExpires), r.Proof) {
			return ErrUnauthorized
		}
		if r.Purpose == "credential.receipt" {
			cr, err := s.credentialReceiptRow(ctx, c, r.ReceiptKind, r.ReceiptRequestID, key)
			if err != nil {
				return err
			}
			if ch.Expires > cr.ValidUntil.Unix() {
				ch.Expires = cr.ValidUntil.Unix()
			}
		}
		if r.Purpose == "operation.receipt" {
			if _, err := s.operationReceiptRow(ctx, c, r.DeviceID, r.CredentialID, r.ReceiptRequestID, r.ReceiptIdempotencyKey); err != nil {
				return err
			}
		}
		if ch.Expires <= s.opts.Now().Unix() {
			return ErrUnauthorized
		}
		if _, err := c.ExecContext(ctx, `DELETE FROM deviceauth_challenges WHERE expires<=?`, s.opts.Now().Unix()); err != nil {
			return err
		}
		proofID := BodyDigest(devicecontract.ChallengeProofBytes(r.ProofDigest, r.ClientNonce, r.ClientExpires))
		var previous Challenge
		var previousProof string
		var used sql.NullInt64
		err := c.QueryRowContext(ctx, `SELECT challenge_id,nonce,expires,proof_digest,consumed_at FROM deviceauth_challenges WHERE auth_public_key=? AND client_nonce=?`, key, r.ClientNonce).Scan(&previous.ID, &previous.Nonce, &previous.Expires, &previousProof, &used)
		if err == nil {
			if previousProof != proofID || used.Valid || previous.Expires <= s.opts.Now().Unix() {
				return ErrUnauthorized
			}
			ch = previous
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// A new row requires private-key proof. Consumed rows count too until
		// expiry, making total retained nonce state bounded rather than just live.
		var total, principal int
		// New-key churn cannot evade a registered credential/activation's nonce
		// budget. The public-key clause also bounds identity-free receipt reads.
		if err := c.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN auth_public_key=? OR (?<>'' AND activation_id=?) OR (?<>'' AND credential_id=?) THEN 1 ELSE 0 END),0) FROM deviceauth_challenges`, key, activationID, activationID, r.CredentialID, r.CredentialID).Scan(&total, &principal); err != nil {
			return err
		}
		limitTotal, limitPrincipal := 10000, 32
		if r.Purpose == "command.disable" || r.Purpose == "command.revoke" || r.Purpose == "request.resolve" {
			limitTotal += int(s.opts.Resources.Devices) * 4
			limitPrincipal += 4
		}
		if total >= limitTotal || principal >= limitPrincipal {
			return ErrQuota
		}
		_, err = c.ExecContext(ctx, `INSERT INTO deviceauth_challenges(challenge_id,nonce,purpose,method,path,body_digest,device_id,credential_id,activation_id,auth_public_key,expires,client_nonce,proof_digest,receipt_request_id,receipt_kind,receipt_idempotency_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, nonce, r.Purpose, r.Method, r.Path, r.BodyDigest, r.DeviceID, r.CredentialID, activationID, key, ch.Expires, r.ClientNonce, proofID, r.ReceiptRequestID, r.ReceiptKind, r.ReceiptIdempotencyKey)
		return err
	})
	return ch, err
}
func (s *Store) consume(ctx context.Context, c *sql.Conn, r SignedRequest, activationID, key string) error {
	if !identifier(r.RequestID) || len(r.RequestID) > 128 || len(r.ChallengeID) != 32 || len(r.Nonce) != 32 || !validPurpose(ChallengeRequest{Purpose: r.Purpose, Method: r.Method, Path: r.Path, DeviceID: r.DeviceID, CredentialID: r.CredentialID}) {
		return ErrUnauthorized
	}
	var stored SignedRequest
	var aid, storedKey string
	var consumed sql.NullInt64
	err := c.QueryRowContext(ctx, `SELECT nonce,purpose,method,path,body_digest,device_id,credential_id,activation_id,auth_public_key,expires,consumed_at FROM deviceauth_challenges WHERE challenge_id=?`, r.ChallengeID).Scan(&stored.Nonce, &stored.Purpose, &stored.Method, &stored.Path, &stored.BodyDigest, &stored.DeviceID, &stored.CredentialID, &aid, &storedKey, &stored.Expires, &consumed)
	if err != nil || consumed.Valid || r.Expires <= s.opts.Now().Unix() || r.Expires != stored.Expires || r.Nonce != stored.Nonce || r.Purpose != stored.Purpose || r.Method != stored.Method || r.Path != stored.Path || r.BodyDigest != stored.BodyDigest || r.DeviceID != stored.DeviceID || r.CredentialID != stored.CredentialID || aid != activationID || storedKey != key || !signatureOK(key, r, r.Signature) {
		return ErrUnauthorized
	}
	principal := r.CredentialID
	if activationID != "" {
		principal = "activation:" + activationID
	}
	mutation := r.Purpose == "activate" || r.Purpose == "credential.rotate" || strings.HasPrefix(r.Purpose, "command.")
	if mutation {
		var cancelled int
		if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM deviceauth_cancellations WHERE principal_id=? AND request_id=?`, principal, r.RequestID).Scan(&cancelled); err != nil {
			return err
		}
		if cancelled != 0 {
			return ErrConflict
		}
		if r.Purpose != "command.disable" && r.Purpose != "command.revoke" {
			var count int
			if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM deviceauth_requests WHERE principal_id=?`, principal).Scan(&count); err != nil {
				return err
			}
			if count >= 4096 {
				return ErrQuota
			}
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_requests(principal_id,request_id,created_at) VALUES(?,?,?)`, principal, r.RequestID, s.opts.Now().UnixNano()); err != nil {
			return ErrUnauthorized
		}
	}
	if _, err = c.ExecContext(ctx, `UPDATE deviceauth_challenges SET consumed_at=? WHERE challenge_id=? AND consumed_at IS NULL`, s.opts.Now().UnixNano(), r.ChallengeID); err != nil {
		return err
	}
	if mutation {
		_, err = c.ExecContext(ctx, `INSERT INTO deviceauth_audit(device_id,credential_id,purpose,request_id,created_at) VALUES(?,?,?,?,?)`, r.DeviceID, r.CredentialID, r.Purpose, r.RequestID, s.opts.Now().UnixNano())
	}
	return err
}
func (s *Store) Activate(ctx context.Context, r SignedRequest, material, key string) (Credential, error) {
	if r.Purpose != "activate" {
		return Credential{}, ErrUnauthorized
	}
	if _, err := authKey(key); err != nil {
		return Credential{}, err
	}
	did, err := opaque()
	if err != nil {
		return Credential{}, err
	}
	cid, err := opaque()
	if err != nil {
		return Credential{}, err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return Credential{}, err
	}
	defer unlock()
	var cr Credential
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		aid, owner, until, err := s.activation(ctx, c, material)
		if err != nil {
			return err
		}
		if err := s.grantResources(ctx, c); err != nil {
			return err
		}
		if err = s.consume(ctx, c, r, aid, key); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_devices(device_id,owner_id,role,state,valid_until) VALUES(?,?,'customer','active',?)`, did, owner, until); err != nil {
			return ErrConflict
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_credentials(credential_id,device_id,public_key,valid_until) VALUES(?,?,?,?)`, cid, did, key, until); err != nil {
			return ErrConflict
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_credential_receipts(kind,request_id,public_key,principal_id,credential_id) VALUES('activate',?,?,?,?)`, r.RequestID, key, "activation:"+aid, cid); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE deviceauth_activations SET consumed_device=?,consumed_at=? WHERE activation_id=? AND consumed_device IS NULL`, did, s.opts.Now().UnixNano(), aid); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE deviceauth_audit SET device_id=?,credential_id=? WHERE purpose='activate' AND request_id=? AND device_id='' AND credential_id=''`, did, cid, r.RequestID); err != nil {
			return err
		}
		cr = Credential{DeviceID: did, ID: cid, PublicKey: key, ValidUntil: time.Unix(0, until).UTC()}
		return nil
	})
	return cr, err
}
func (s *Store) SubmitSigned(ctx context.Context, r SignedRequest, cmd Command) (Operation, error) {
	if r.Purpose != "command."+cmd.Action || cmd.Action == "expire" {
		return Operation{}, ErrUnauthorized
	}
	// This read only provides the validation input; it grants no authority. The
	// credential, owner and nonce are rechecked in the desired-state transaction.
	var owner string
	if err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM deviceauth_devices WHERE device_id=?`, r.DeviceID).Scan(&owner); err != nil {
		return Operation{}, ErrUnauthorized
	}
	cmd.DeviceID = r.DeviceID
	cmd.Actor = Actor{ID: r.DeviceID, OwnerID: owner}
	return s.submitChecked(ctx, cmd, false, func(c *sql.Conn) error {
		cr, actor, err := s.credential(ctx, c, r.DeviceID, r.CredentialID)
		if err != nil {
			return err
		}
		if actor != cmd.Actor {
			return ErrUnauthorized
		}
		return s.consume(ctx, c, r, "", cr.PublicKey)
	}, func(c *sql.Conn, op Operation) error {
		_, err := c.ExecContext(ctx, `UPDATE deviceauth_requests SET operation_id=? WHERE principal_id=? AND request_id=?`, op.ID, r.CredentialID, r.RequestID)
		return err
	})
}
func (s *Store) OperationSigned(ctx context.Context, r SignedRequest, id string) (Operation, bool, error) {
	if r.Purpose != "operation.status" || r.Path != "/api/v2/operations/"+id {
		return Operation{}, false, ErrUnauthorized
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return Operation{}, false, err
	}
	defer unlock()
	var op Operation
	var effective bool
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		cr, _, err := s.credential(ctx, c, r.DeviceID, r.CredentialID)
		if err != nil {
			return err
		}
		op, err = operationRow(ctx, c, id)
		if err != nil {
			return err
		}
		if op.DeviceID != r.DeviceID {
			return ErrUnauthorized
		}
		if err = s.consume(ctx, c, r, "", cr.PublicKey); err != nil {
			return err
		}
		d, err := deviceRow(ctx, c, r.DeviceID)
		if err != nil {
			return err
		}
		effective = op.State == "done" && d.Generation == op.Generation && d.AppliedGeneration == op.Generation
		return nil
	})
	return op, effective, err
}
func (s *Store) RotateCredential(ctx context.Context, r SignedRequest, key, newSignature string) (Credential, error) {
	if r.Purpose != "credential.rotate" || !signatureOK(key, r, newSignature) {
		return Credential{}, ErrUnauthorized
	}
	id, err := opaque()
	if err != nil {
		return Credential{}, err
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return Credential{}, err
	}
	defer unlock()
	var result Credential
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		old, _, err := s.credential(ctx, c, r.DeviceID, r.CredentialID)
		if err != nil {
			return err
		}
		if err := s.grantResources(ctx, c); err != nil {
			return err
		}
		if old.PublicKey == key {
			return ErrConflict
		}
		if err = s.consume(ctx, c, r, "", old.PublicKey); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_credentials(credential_id,device_id,public_key,valid_until) VALUES(?,?,?,?)`, id, r.DeviceID, key, old.ValidUntil.UnixNano()); err != nil {
			return ErrConflict
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_credential_receipts(kind,request_id,public_key,principal_id,credential_id) VALUES('credential.rotate',?,?,?,?)`, r.RequestID, key, old.ID, id); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE deviceauth_credentials SET revoked_at=?,replaced_by=? WHERE credential_id=? AND revoked_at IS NULL`, s.opts.Now().UnixNano(), id, old.ID); err != nil {
			return err
		}
		result = Credential{DeviceID: r.DeviceID, ID: id, PublicKey: key, ValidUntil: old.ValidUntil}
		return nil
	})
	return result, err
}

// IsNotFound lets handlers retain a stable public error without exposing SQL.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
