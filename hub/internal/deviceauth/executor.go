package deviceauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"time"
)

type binding struct {
	key, address, deviceID, managedBy, role string
	revoked                                 int64
	applied                                 bool
	priorIntent                             bool
}
type action struct{ kind, key, address string }

func (s *Store) bindings(ctx context.Context, c *sql.Conn, id string) ([]binding, error) {
	rows, err := c.QueryContext(ctx, `SELECT b.public_key,b.address,b.device_id,b.managed_by,b.role,b.revoked_generation,b.applied,EXISTS(SELECT 1 FROM deviceauth_intents i WHERE i.public_key=b.public_key AND i.action='apply' AND i.epoch=?) FROM deviceauth_bindings b WHERE b.device_id=? ORDER BY b.public_key`, s.opts.Policy.Epoch, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []binding
	for rows.Next() {
		var b binding
		if err := rows.Scan(&b.key, &b.address, &b.deviceID, &b.managedBy, &b.role, &b.revoked, &b.applied, &b.priorIntent); err != nil {
			return nil, err
		}
		result = append(result, b)
		// New grants have a history quota. Revocation must still read every
		// retained obligation (including a future/imported oversized history).
	}
	return result, rows.Err()
}

func runtimeMap(peers []Peer) (map[string][]string, error) {
	if len(peers) > 16384 {
		return nil, ErrProtected
	}
	result := make(map[string][]string, len(peers))
	for _, peer := range peers {
		if !validKey(peer.PublicKey) || len(peer.AllowedIPs) > 64 {
			return nil, ErrProtected
		}
		if _, exists := result[peer.PublicKey]; exists {
			return nil, ErrProtected
		}
		ips := make([]string, 0, len(peer.AllowedIPs))
		for _, value := range peer.AllowedIPs {
			p, err := netip.ParsePrefix(value)
			if err != nil || p.Addr().Is4In6() {
				return nil, ErrProtected
			}
			ips = append(ips, p.Masked().String())
		}
		sort.Strings(ips)
		for i := 1; i < len(ips); i++ {
			if ips[i] == ips[i-1] {
				return nil, ErrProtected
			}
		}
		result[peer.PublicKey] = ips
	}
	return result, nil
}

func (s *Store) plan(d Device, bindings []binding, runtime map[string][]string) ([]action, error) {
	managed := make(map[string]binding, len(bindings))
	for _, b := range bindings {
		if b.deviceID != d.ID || b.managedBy != s.opts.Policy.ManagedBy || b.role != "customer" {
			return nil, ErrProtected
		}
		if _, err := s.address(b.key, b.address); err != nil {
			return nil, ErrProtected
		}
		if ips, present := runtime[b.key]; present {
			// Never adopt a preexisting peer just because a new desired row has
			// the same public key. A prior durable, absence-checked apply intent
			// is the provenance used to recover an action whose ack was lost.
			if !b.applied && !b.priorIntent {
				return nil, ErrProtected
			}
			if len(ips) != 1 || ips[0] != b.address {
				return nil, ErrProtected
			}
		}
		managed[b.key] = b
	}
	var result []action
	for _, b := range bindings {
		if b.revoked > 0 {
			if _, present := runtime[b.key]; present {
				result = append(result, action{"remove", b.key, b.address})
			}
		}
	}
	if d.State == "active" && d.PublicKey != "" {
		b, exists := managed[d.PublicKey]
		if !exists || b.revoked != 0 || b.address != d.Address {
			return nil, ErrProtected
		}
		target, _ := netip.ParsePrefix(d.Address)
		// Unknown, protected and other-device peers are never removed to make
		// room. Even a broad allowed-ip prefix overlapping one customer host
		// blocks apply; a customer address pool is not an ownership proof.
		for key, ips := range runtime {
			if key == d.PublicKey {
				continue
			}
			old, ours := managed[key]
			if ours && old.revoked > 0 {
				continue
			}
			for _, value := range ips {
				p, _ := netip.ParsePrefix(value)
				if p.Overlaps(target) {
					return nil, ErrProtected
				}
			}
		}
		if _, present := runtime[d.PublicKey]; !present {
			result = append(result, action{"apply", d.PublicKey, d.Address})
		}
	}
	return result, nil
}

// Process deliberately has THREE persistence phases under one process fence:
// claim/intent commits; exact external actions; verified result commit. A crash
// between them cannot undo WG, so retry always reads latest desired state and all
// history. No API reports applied until runtime verification and result commit.
func (s *Store) Process(ctx context.Context, id string, executor Executor) error {
	if !identifier(id) || executor == nil {
		return ErrInvalid
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var op Operation
	var d Device
	var history []binding
	var superseded bool
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		var err error
		op, err = operationRow(ctx, c, id)
		if err != nil {
			return err
		}
		d, err = deviceRow(ctx, c, op.DeviceID)
		if err != nil {
			return err
		}
		if op.Epoch != s.opts.Policy.Epoch || op.Generation != d.Generation {
			superseded = true
			_, err = c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='superseded',last_error='' WHERE operation_id=?`, id)
			return err
		}
		// Even historical done operations are freshly reconciled. Runtime may
		// have restarted from an old config since the previous verification.
		if d.State == "active" && !s.opts.Now().Before(d.ValidUntil) {
			return ErrExpired
		}
		history, err = s.bindings(ctx, c, d.ID)
		if err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE deviceauth_meta SET executor_fence=executor_fence+1 WHERE singleton=1 AND executor_fence < 9223372036854775807`); err != nil {
			return err
		}
		if err = c.QueryRowContext(ctx, `SELECT executor_fence FROM deviceauth_meta WHERE singleton=1`).Scan(&op.Fence); err != nil {
			return err
		}
		if op.Fence == int64(^uint64(0)>>1) {
			return ErrGeneration
		}
		_, err = c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='pending',attempts=attempts+1,fence=?,last_error='' WHERE operation_id=?`, op.Fence, id)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrExpired) {
			return s.expireLocked(d)
		}
		return err
	}
	if superseded {
		return ErrSuperseded
	}
	fence := Fence{Epoch: op.Epoch, DeviceID: d.ID, Generation: d.Generation, Sequence: op.Fence}
	actionCtx, cancel := context.WithTimeout(ctx, s.opts.ActionTimeout)
	defer cancel()
	beforePeers, err := executor.Snapshot(actionCtx, fence)
	if d.State == "active" && !s.opts.Now().Before(d.ValidUntil) {
		return s.expireLocked(d)
	}
	if err != nil || actionCtx.Err() != nil {
		return s.failed(id, ErrExecutionUnknown)
	}
	before, err := runtimeMap(beforePeers)
	if err != nil {
		return s.failed(id, err)
	}
	actions, err := s.plan(d, history, before)
	if err != nil {
		return s.failed(id, err)
	}
	if d.State == "active" && !s.opts.Now().Before(d.ValidUntil) {
		return s.expireLocked(d)
	}
	// Durable intent precedes any action; on first apply plan() proved the key
	// absent. It authorizes crash recovery only inside this sole-writer boundary.
	err = s.write(actionCtx, func(c *sql.Conn) error {
		if err := s.checkPolicy(actionCtx, c); err != nil {
			return err
		}
		for _, a := range actions {
			if _, err := c.ExecContext(actionCtx, `INSERT INTO deviceauth_intents(operation_id,public_key,action,epoch,generation,fence,address,state) VALUES(?,?,?,?,?,?,?,'prepared') ON CONFLICT(operation_id,public_key,action) DO UPDATE SET fence=excluded.fence,state='prepared'`, id, a.key, a.kind, op.Epoch, d.Generation, op.Fence, a.address); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return s.failed(id, ErrExecutionUnknown)
	}
	for _, a := range actions {
		if err := s.checkDatabaseIdentity(); err != nil {
			return s.failed(id, ErrPolicy)
		}
		if actionCtx.Err() != nil {
			return s.failed(id, ErrExecutionUnknown)
		}
		if a.kind == "remove" {
			err = executor.Remove(actionCtx, fence, a.key)
		} else {
			if d.State != "active" || !s.opts.Now().Before(d.ValidUntil) {
				return s.expireLocked(d)
			}
			err = executor.Apply(actionCtx, fence, Peer{PublicKey: a.key, AllowedIPs: []string{a.address}})
		}
		// A nil response after deadline is still unknown; keep the fence until
		// this synchronous method returns so a delayed action cannot race revoke.
		if d.State == "active" && !s.opts.Now().Before(d.ValidUntil) {
			return s.expireLocked(d)
		}
		if err != nil || actionCtx.Err() != nil {
			return s.failed(id, ErrExecutionUnknown)
		}
	}
	afterPeers, err := executor.Snapshot(actionCtx, fence)
	if d.State == "active" && !s.opts.Now().Before(d.ValidUntil) {
		return s.expireLocked(d)
	}
	if err != nil || actionCtx.Err() != nil {
		return s.failed(id, ErrExecutionUnknown)
	}
	after, err := runtimeMap(afterPeers)
	if err != nil {
		return s.failed(id, ErrVerification)
	}
	if err := verify(d, history, before, after); err != nil {
		return s.failed(id, err)
	}
	// Expiry during a slow apply must not be certified as an active grant.
	if d.State == "active" && !s.opts.Now().Before(d.ValidUntil) {
		return s.expireLocked(d)
	}
	err = s.write(actionCtx, func(c *sql.Conn) error {
		if err := s.checkPolicy(actionCtx, c); err != nil {
			return err
		}
		result, err := c.ExecContext(actionCtx, `UPDATE deviceauth_devices SET applied_generation=? WHERE device_id=? AND generation=?`, d.Generation, d.ID, d.Generation)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSuperseded
		}
		now := s.opts.Now().UTC().UnixNano()
		if _, err = c.ExecContext(actionCtx, `UPDATE deviceauth_bindings SET removed_generation=? WHERE device_id=? AND revoked_generation>0`, d.Generation, d.ID); err != nil {
			return err
		}
		if d.PublicKey != "" {
			if _, err = c.ExecContext(actionCtx, `UPDATE deviceauth_bindings SET applied=1 WHERE public_key=? AND device_id=? AND revoked_generation=0`, d.PublicKey, d.ID); err != nil {
				return err
			}
		}
		if _, err = c.ExecContext(actionCtx, `UPDATE deviceauth_tombstones SET verified_at=? WHERE public_key IN (SELECT public_key FROM deviceauth_bindings WHERE device_id=?) AND verified_at IS NULL`, now, d.ID); err != nil {
			return err
		}
		if _, err = c.ExecContext(actionCtx, `UPDATE deviceauth_intents SET state='verified' WHERE operation_id=? AND fence=?`, id, op.Fence); err != nil {
			return err
		}
		result, err = c.ExecContext(actionCtx, `UPDATE deviceauth_outbox SET state='done',verified_at=?,last_error='' WHERE operation_id=? AND fence=?`, now, id, op.Fence)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSuperseded
		}
		return nil
	})
	if err != nil {
		return s.failed(id, ErrExecutionUnknown)
	}
	return nil
}

// An observed expiry is a new desired generation, not just an error string.
// If an action crossed the expiry instant, its durable intent and new tombstone
// retain the removal obligation. The caller must drain/reconcile the new outbox;
// a global expiry/background/restart scheduler is still not wired in this slice.
func (s *Store) expireLocked(d Device) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sum := sha256.Sum256([]byte(fmt.Sprintf("executor-expiry:%s:%d", d.ID, d.Generation)))
	op, err := s.submit(ctx, Command{Actor: Actor{ID: "deviceauth-expiry-reconciler", OwnerID: d.OwnerID}, DeviceID: d.ID, Action: "expire", IdempotencyKey: hex.EncodeToString(sum[:]), ExpectedGeneration: d.Generation, Reason: "authorization deadline reached during reconciliation"}, true)
	if err != nil {
		return fmt.Errorf("%w: expiry persistence unavailable", ErrExpired)
	}
	if err = s.write(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='degraded',last_error='expired_during_execution' WHERE operation_id=? AND state='pending'`, op.ID)
		return err
	}); err != nil {
		return fmt.Errorf("%w: expiry signal unavailable", ErrExpired)
	}
	return ErrExpired
}

// Reconcile runs the latest persisted desired state for one known device,
// including previously completed operations. The future restart scheduler must
// explicitly invoke it; this package does not modify startup or scan/delete
// runtime peers absent from the customer registry.
func (s *Store) Reconcile(ctx context.Context, deviceID string, executor Executor) error {
	if !identifier(deviceID) {
		return ErrInvalid
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT o.operation_id FROM deviceauth_operations o JOIN deviceauth_devices d ON d.device_id=o.device_id AND d.generation=o.generation WHERE d.device_id=? AND o.epoch=?`, deviceID, s.opts.Policy.Epoch).Scan(&id)
	if err != nil {
		return err
	}
	return s.Process(ctx, id, executor)
}

func verify(d Device, history []binding, before, after map[string][]string) error {
	managed := make(map[string]bool, len(history))
	for _, b := range history {
		managed[b.key] = true
		if b.revoked > 0 {
			if _, present := after[b.key]; present {
				return ErrVerification
			}
		}
	}
	if d.State == "active" && d.PublicKey != "" {
		if !reflect.DeepEqual(after[d.PublicKey], []string{d.Address}) {
			return ErrVerification
		}
	}
	for key, ips := range before {
		if !managed[key] && !reflect.DeepEqual(ips, after[key]) {
			return ErrVerification
		}
	}
	for key := range after {
		if !managed[key] {
			if _, existed := before[key]; !existed {
				return ErrVerification
			}
		}
	}
	return nil
}

func (s *Store) failed(id string, cause error) error {
	// Never persist arbitrary executor stderr: it may contain config/credentials.
	code := "execution_unknown"
	switch {
	case errors.Is(cause, ErrProtected):
		code = "protected_peer"
	case errors.Is(cause, ErrVerification):
		code = "verification_failed"
	case errors.Is(cause, ErrExpired):
		code = "expired"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.write(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='degraded',last_error=? WHERE operation_id=? AND state NOT IN ('done','superseded')`, code, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("%w: result persistence unavailable", cause)
	}
	return cause
}
