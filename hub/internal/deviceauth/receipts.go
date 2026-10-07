package deviceauth

import (
	"context"
	"database/sql"
	"time"
)

func receiptKind(v string) bool { return v == "activate" || v == "credential.rotate" }

func (s *Store) historicalCredential(ctx context.Context, c *sql.Conn, deviceID, credentialID string) (Credential, error) {
	var cr Credential
	var until int64
	err := c.QueryRowContext(ctx, `SELECT c.credential_id,c.device_id,c.public_key,c.valid_until FROM deviceauth_credentials c JOIN deviceauth_devices d USING(device_id) WHERE c.credential_id=? AND c.device_id=? AND d.role='customer'`, credentialID, deviceID).Scan(&cr.ID, &cr.DeviceID, &cr.PublicKey, &until)
	if err != nil {
		return Credential{}, ErrUnauthorized
	}
	cr.ValidUntil = time.Unix(0, until).UTC()
	return cr, nil
}

func (s *Store) credentialReceiptRow(ctx context.Context, c *sql.Conn, kind, requestID, key string) (Credential, error) {
	var cid, did string
	if err := c.QueryRowContext(ctx, `SELECT r.credential_id,c.device_id FROM deviceauth_credential_receipts r JOIN deviceauth_credentials c USING(credential_id) WHERE r.kind=? AND r.request_id=? AND r.public_key=?`, kind, requestID, key).Scan(&cid, &did); err != nil {
		return Credential{}, err
	}
	cr, _, err := s.credential(ctx, c, did, cid)
	return cr, err
}

func (s *Store) operationReceiptRow(ctx context.Context, c *sql.Conn, deviceID, credentialID, requestID, idempotency string) (Operation, error) {
	var id string
	err := c.QueryRowContext(ctx, `SELECT r.operation_id FROM deviceauth_requests r JOIN deviceauth_operations o ON o.operation_id=r.operation_id WHERE r.principal_id=? AND r.request_id=? AND o.device_id=? AND o.idempotency_key=?`, credentialID, requestID, deviceID, idempotency).Scan(&id)
	if err != nil {
		return Operation{}, err
	}
	return operationRow(ctx, c, id)
}

func receiptBinding(ctx context.Context, c *sql.Conn, r SignedRequest, requestID, kind, idempotency string) error {
	var rid, k, idem string
	if err := c.QueryRowContext(ctx, `SELECT receipt_request_id,receipt_kind,receipt_idempotency_key FROM deviceauth_challenges WHERE challenge_id=?`, r.ChallengeID).Scan(&rid, &k, &idem); err != nil || rid != requestID || k != kind || idem != idempotency {
		return ErrUnauthorized
	}
	return nil
}

func (s *Store) CredentialReceipt(ctx context.Context, r SignedRequest, kind, requestID, key string) (Credential, error) {
	if r.Purpose != "credential.receipt" || !receiptKind(kind) || !identifier(requestID) || len(requestID) > 128 || r.DeviceID != "" || r.CredentialID != "" {
		return Credential{}, ErrUnauthorized
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
		if err := receiptBinding(ctx, c, r, requestID, kind, ""); err != nil {
			return err
		}
		if err := s.consume(ctx, c, r, "", key); err != nil {
			return err
		}
		var err error
		cr, err = s.credentialReceiptRow(ctx, c, kind, requestID, key)
		return err
	})
	return cr, err
}

func (s *Store) OperationReceipt(ctx context.Context, r SignedRequest, requestID, idempotency string) (Operation, bool, error) {
	if r.Purpose != "operation.receipt" || !identifier(requestID) || len(requestID) > 128 || !identifier(idempotency) || len(idempotency) > 128 {
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
		cr, err := s.historicalCredential(ctx, c, r.DeviceID, r.CredentialID)
		if err != nil {
			return err
		}
		if err := receiptBinding(ctx, c, r, requestID, "", idempotency); err != nil {
			return err
		}
		if err = s.consume(ctx, c, r, "", cr.PublicKey); err != nil {
			return err
		}
		op, err = s.operationReceiptRow(ctx, c, r.DeviceID, r.CredentialID, requestID, idempotency)
		if err != nil {
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
