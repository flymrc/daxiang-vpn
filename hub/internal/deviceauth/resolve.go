package deviceauth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type ResolveRequest struct {
	Kind, RequestID, IdempotencyKey, PublicKey, ActivationCredential string
}

type ResolveReceipt struct {
	State, Kind, RequestID, IdempotencyKey, PublicKey string
	Credential                                        *Credential
	Operation                                         *Operation
	Effective                                         bool
}

func resolveKind(kind string) bool {
	return receiptKind(kind) || kind == "command.apply" || kind == "command.disable" || kind == "command.revoke"
}

// Resolution proves a narrowly scoped negative authority. Expired/consumed
// activation material and historical credentials can cancel their own original
// request IDs, but never issue a credential, revive a device or extend a grant.
// Rotation needs both original owner and prospective new-key possession.
func (s *Store) resolveIdentity(ctx context.Context, c *sql.Conn, deviceID, credentialID string, q ResolveRequest) (activationID, key, ownerKey string, err error) {
	if !resolveKind(q.Kind) || !identifier(q.RequestID) || len(q.RequestID) > 128 {
		return "", "", "", ErrInvalid
	}
	if receiptKind(q.Kind) {
		if q.IdempotencyKey != "" {
			return "", "", "", ErrInvalid
		}
		if _, err := authKey(q.PublicKey); err != nil {
			return "", "", "", err
		}
		key = q.PublicKey
	} else if q.ActivationCredential != "" || q.PublicKey != "" || !identifier(q.IdempotencyKey) || len(q.IdempotencyKey) > 128 {
		return "", "", "", ErrInvalid
	}
	if q.Kind == "activate" {
		if deviceID != "" || credentialID != "" {
			return "", "", "", ErrUnauthorized
		}
		id, verifier, err := activationParts(q.ActivationCredential)
		if err != nil {
			return "", "", "", err
		}
		var stored string
		if err := c.QueryRowContext(ctx, `SELECT verifier FROM deviceauth_activations WHERE activation_id=?`, id).Scan(&stored); err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(verifier)) != 1 {
			return "", "", "", ErrUnauthorized
		}
		return id, key, "", nil
	}
	if q.ActivationCredential != "" {
		return "", "", "", ErrInvalid
	}
	cr, err := s.historicalCredential(ctx, c, deviceID, credentialID)
	if err != nil {
		return "", "", "", err
	}
	if q.Kind == "credential.rotate" {
		if key == cr.PublicKey {
			return "", "", "", ErrInvalid
		}
		return "", key, cr.PublicKey, nil
	}
	return "", cr.PublicKey, "", nil
}

func (s *Store) ResolvePending(ctx context.Context, r SignedRequest, q ResolveRequest, ownerProof string) (ResolveReceipt, error) {
	if r.Purpose != "request.resolve" || (q.Kind != "credential.rotate" && ownerProof != "") {
		return ResolveReceipt{}, ErrUnauthorized
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return ResolveReceipt{}, err
	}
	defer unlock()
	result := ResolveReceipt{Kind: q.Kind, RequestID: q.RequestID, IdempotencyKey: q.IdempotencyKey, PublicKey: q.PublicKey}
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		aid, key, ownerKey, err := s.resolveIdentity(ctx, c, r.DeviceID, r.CredentialID, q)
		if err != nil {
			return err
		}
		if q.Kind == "credential.rotate" && !signatureOK(ownerKey, r, ownerProof) {
			return ErrUnauthorized
		}
		if err := receiptBinding(ctx, c, r, q.RequestID, q.Kind, q.IdempotencyKey); err != nil {
			return err
		}
		if err := s.consume(ctx, c, r, aid, key); err != nil {
			return err
		}
		principal := r.CredentialID
		if aid != "" {
			principal = "activation:" + aid
		}
		if receiptKind(q.Kind) {
			var cr Credential
			var until int64
			err = c.QueryRowContext(ctx, `SELECT c.credential_id,c.device_id,c.public_key,c.valid_until FROM deviceauth_credential_receipts r JOIN deviceauth_credentials c USING(credential_id) WHERE r.kind=? AND r.request_id=? AND r.public_key=? AND r.principal_id=?`, q.Kind, q.RequestID, key, principal).Scan(&cr.ID, &cr.DeviceID, &cr.PublicKey, &until)
			if err == nil {
				cr.ValidUntil = time.Unix(0, until).UTC()
				result.State, result.Credential = "committed", &cr
				return nil
			}
		} else {
			var op Operation
			op, err = s.operationReceiptRow(ctx, c, r.DeviceID, r.CredentialID, q.RequestID, q.IdempotencyKey)
			if err == nil {
				if op.Action != strings.TrimPrefix(q.Kind, "command.") {
					return ErrConflict
				}
				d, e := deviceRow(ctx, c, r.DeviceID)
				if e != nil {
					return e
				}
				result.State, result.Operation = "committed", &op
				result.Effective = op.State == "done" && d.Generation == op.Generation && d.AppliedGeneration == op.Generation
				return nil
			}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// A different kind/key/idempotency for an already committed principal RID
		// must never produce a false cancelled receipt.
		var accepted int
		if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM deviceauth_requests WHERE principal_id=? AND request_id=?`, principal, q.RequestID).Scan(&accepted); err != nil {
			return err
		}
		if accepted != 0 {
			return ErrConflict
		}
		var kind, storedKey, device, idem string
		err = c.QueryRowContext(ctx, `SELECT kind,public_key,device_id,idempotency_key FROM deviceauth_cancellations WHERE principal_id=? AND request_id=?`, principal, q.RequestID).Scan(&kind, &storedKey, &device, &idem)
		if err == nil {
			if kind != q.Kind || storedKey != key || device != r.DeviceID || idem != q.IdempotencyKey {
				return ErrConflict
			}
			result.State = "cancelled"
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// This bounded reserve is independent of the positive grant high-water
		// gate. Tombstones and their transition audits are permanent; retries
		// consume only an expiring nonce. Physical storage failure stays unknown.
		var total, count int64
		if err := c.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN principal_id=? THEN 1 ELSE 0 END),0) FROM deviceauth_cancellations`, principal).Scan(&total, &count); err != nil {
			return err
		}
		if total >= s.opts.Resources.Cancellations || count >= 16 {
			return ErrQuota
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO deviceauth_cancellations(principal_id,request_id,kind,public_key,device_id,idempotency_key,committed_at) VALUES(?,?,?,?,?,?,?)`, principal, q.RequestID, q.Kind, key, r.DeviceID, q.IdempotencyKey, s.opts.Now().UnixNano()); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO deviceauth_audit(device_id,credential_id,purpose,request_id,created_at) VALUES(?,?,'request.cancel',?,?)`, r.DeviceID, r.CredentialID, q.RequestID, s.opts.Now().UnixNano()); err != nil {
			return err
		}
		result.State = "cancelled"
		return nil
	})
	return result, err
}
