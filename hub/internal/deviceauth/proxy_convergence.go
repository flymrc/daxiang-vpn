package deviceauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"sort"
	"time"
)

// ProxyConvergenceProof is internal runtime evidence, never an authorization
// receipt or a durable lease. The callback executes while all authority writers
// and the runtime executor are fenced. A later grant must repeat this proof.
type ProxyConvergenceProof struct {
	Epoch, ManagedBy, PolicySHA256 string
	Sequence                       int64
	VerifiedAt, EarliestValidUntil time.Time
	ActiveDevices                  int
}

// CanonicalPolicyDigest is the same normalized content identity persisted by
// Store. It does not sign a policy or authorize changing the hosting source.
func CanonicalPolicyDigest(p Policy) (string, error) {
	p.AddressPools = append([]string(nil), p.AddressPools...)
	p.Protected = append([]Protection(nil), p.Protected...)
	if err := normalizePolicy(&p); err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// WithProxyConvergence does not equate a successful Tick with readiness. It
// proves the latest desired/applied/outbox state and actual WG under one pinned
// execution fence and BEGIN IMMEDIATE, and keeps that fence through grant ACK.
// Failed grant or COMMIT must close the receiver before the service can listen.
func (s *Store) WithProxyConvergence(ctx context.Context, executor Executor, managedSources []string, grant func(context.Context, ProxyConvergenceProof) error) error {
	if executor == nil || grant == nil || len(managedSources) == 0 || len(managedSources) > 64 {
		return ErrInvalid
	}
	scopes := make([]netip.Prefix, 0, len(managedSources))
	canonical := make([]string, 0, len(managedSources))
	for _, value := range managedSources {
		p, err := netip.ParsePrefix(value)
		if err != nil || p != p.Masked() || p.Addr().Is4In6() || p.String() != value {
			return ErrPolicy
		}
		for _, old := range scopes {
			if p.Overlaps(old) {
				return ErrPolicy
			}
		}
		scopes = append(scopes, p)
		canonical = append(canonical, value)
	}
	sort.Strings(canonical)
	if len(canonical) != len(s.opts.Policy.AddressPools) {
		return ErrPolicy
	}
	for i, value := range canonical {
		if value != s.opts.Policy.AddressPools[i] {
			return ErrPolicy
		}
	}
	for _, protection := range s.opts.Policy.Protected {
		if protection.Prefix == "" {
			continue
		}
		p, err := netip.ParsePrefix(protection.Prefix)
		if err != nil {
			return ErrPolicy
		}
		for _, scope := range scopes {
			if p.Overlaps(scope) {
				return ErrProtected
			}
		}
	}
	unlock, pinned, err := s.acquirePinned(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	bounded, cancel := context.WithTimeout(context.WithValue(ctx, executionFenceKey{}, pinned), s.opts.ActionTimeout)
	defer cancel()
	return s.write(bounded, func(c *sql.Conn) error {
		if err := s.checkPolicy(bounded, c); err != nil {
			return err
		}
		if bounded.Err() != nil {
			return bounded.Err()
		}
		result, err := c.ExecContext(bounded, `UPDATE deviceauth_meta SET executor_fence=executor_fence+1 WHERE singleton=1 AND executor_fence<9223372036854775807`)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return ErrGeneration
		}
		proof := ProxyConvergenceProof{Epoch: s.opts.Policy.Epoch, ManagedBy: s.opts.Policy.ManagedBy}
		if err := c.QueryRowContext(bounded, `SELECT executor_fence FROM deviceauth_meta WHERE singleton=1`).Scan(&proof.Sequence); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(s.policyJSON))
		proof.PolicySHA256 = hex.EncodeToString(sum[:])
		peers, err := executor.Snapshot(bounded, Fence{Epoch: proof.Epoch, DeviceID: "proxy-convergence", Generation: 1, Sequence: proof.Sequence})
		if err != nil || bounded.Err() != nil {
			return ErrExecutionUnknown
		}
		runtime, err := runtimeMap(peers)
		if err != nil {
			return ErrVerification
		}
		now := s.opts.Now().UTC()
		expected := make(map[string]string)
		rows, err := c.QueryContext(bounded, `SELECT device_id FROM deviceauth_devices ORDER BY device_id LIMIT 10001`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) > 10000 {
			return ErrQuota
		}
		for _, id := range ids {
			d, err := deviceRow(bounded, c, id)
			if err != nil {
				return err
			}
			if d.Role != "customer" || d.Generation != d.AppliedGeneration {
				return ErrVerification
			}
			if d.State == "active" {
				if !now.Before(d.ValidUntil) {
					return ErrExpired
				}
				if proof.EarliestValidUntil.IsZero() || d.ValidUntil.Before(proof.EarliestValidUntil) {
					proof.EarliestValidUntil = d.ValidUntil
				}
			}
			if d.Generation > 0 {
				var count int
				if err := c.QueryRowContext(bounded, `SELECT COUNT(*) FROM deviceauth_operations o JOIN deviceauth_outbox b USING(operation_id) WHERE o.device_id=? AND o.generation=? AND o.epoch=? AND b.state='done' AND b.verified_at IS NOT NULL AND b.fence>0 AND ((?='active' AND o.action='apply') OR (?!='active' AND o.action!='apply'))`, id, d.Generation, s.opts.Policy.Epoch, d.State, d.State).Scan(&count); err != nil {
					return err
				}
				if count != 1 {
					return ErrVerification
				}
			}
			var unresolved int
			if err := c.QueryRowContext(bounded, `SELECT COUNT(*) FROM deviceauth_intents WHERE generation=? AND state!='verified' AND operation_id IN (SELECT operation_id FROM deviceauth_operations WHERE device_id=? AND generation=?)`, d.Generation, id, d.Generation).Scan(&unresolved); err != nil {
				return err
			}
			if unresolved != 0 {
				return ErrVerification
			}
			history, err := s.proxyProofBindings(bounded, c, id)
			if err != nil {
				return err
			}
			for _, b := range history {
				if b.deviceID != d.ID || b.managedBy != s.opts.Policy.ManagedBy || b.role != "customer" {
					return ErrProtected
				}
				if _, err := s.address(b.key, b.address); err != nil {
					return ErrProtected
				}
				if b.revoked > 0 {
					if _, present := runtime[b.key]; present {
						return ErrVerification
					}
					var removed int64
					if err := c.QueryRowContext(bounded, `SELECT removed_generation FROM deviceauth_bindings WHERE public_key=?`, b.key).Scan(&removed); err != nil {
						return err
					}
					if removed < b.revoked || removed != d.Generation {
						return ErrVerification
					}
					var negative int
					if err := c.QueryRowContext(bounded, `SELECT COUNT(*) FROM deviceauth_tombstones WHERE public_key=? AND (verified_at IS NULL OR generation>?)`, b.key, d.Generation).Scan(&negative); err != nil {
						return err
					}
					if negative != 0 {
						return ErrVerification
					}
					continue
				}
				if d.State != "active" || d.PublicKey != b.key || d.Address != b.address || !b.applied {
					return ErrVerification
				}
				var created, removed int64
				var reservation string
				if err := c.QueryRowContext(bounded, `SELECT created_generation,removed_generation FROM deviceauth_bindings WHERE public_key=?`, b.key).Scan(&created, &removed); err != nil {
					return err
				}
				if created != d.Generation || removed != 0 {
					return ErrVerification
				}
				if err := c.QueryRowContext(bounded, `SELECT device_id FROM deviceauth_addresses WHERE address=?`, b.address).Scan(&reservation); err != nil || reservation != d.ID {
					return ErrVerification
				}
				var tombstones int
				if err := c.QueryRowContext(bounded, `SELECT COUNT(*) FROM deviceauth_tombstones WHERE public_key=?`, b.key).Scan(&tombstones); err != nil {
					return err
				}
				if tombstones != 0 {
					return ErrVerification
				}
				if _, duplicate := expected[b.key]; duplicate {
					return ErrProtected
				}
				expected[b.key] = b.address
			}
			if d.PublicKey != "" {
				if expected[d.PublicKey] != d.Address {
					return ErrVerification
				}
				proof.ActiveDevices++
				// Device enrollment is the durable dataplane authority. When an
				// HTTP credential exists its shorter current lifetime also bounds
				// this lease; historical rotated/revoked credentials never extend it.
				crRows, err := c.QueryContext(bounded, `SELECT valid_until FROM deviceauth_credentials WHERE device_id=? AND revoked_at IS NULL AND replaced_by IS NULL LIMIT 2`, id)
				if err != nil {
					return err
				}
				credentialCount := 0
				for crRows.Next() {
					var until int64
					if err := crRows.Scan(&until); err != nil {
						crRows.Close()
						return err
					}
					credentialCount++
					expires := time.Unix(0, until).UTC()
					if !now.Before(expires) {
						crRows.Close()
						return ErrExpired
					}
					if proof.EarliestValidUntil.IsZero() || expires.Before(proof.EarliestValidUntil) {
						proof.EarliestValidUntil = expires
					}
				}
				err = crRows.Err()
				crRows.Close()
				if err != nil {
					return err
				}
				if credentialCount > 1 {
					return ErrVerification
				}
			} else if len(history) == 0 && d.Generation > 0 && d.State == "active" {
				return ErrVerification
			}
		}
		for key, address := range expected {
			ips, present := runtime[key]
			if !present || len(ips) != 1 || ips[0] != address {
				return ErrVerification
			}
		}
		// Source IP is the proxy's only identity. Every runtime peer overlapping
		// the immutable managed source scope must therefore be one exact current
		// customer host. We preserve unknown/protected peers outside the scope.
		for key, ips := range runtime {
			for _, value := range ips {
				p, _ := netip.ParsePrefix(value)
				for _, scope := range scopes {
					if !p.Overlaps(scope) {
						continue
					}
					if expected[key] != value || len(ips) != 1 || s.protected(key, p) {
						return ErrProtected
					}
				}
			}
		}
		if err := s.checkDatabaseIdentity(); err != nil {
			return err
		}
		proof.VerifiedAt = s.opts.Now().UTC()
		if bounded.Err() != nil {
			return bounded.Err()
		}
		if !proof.EarliestValidUntil.IsZero() && !proof.VerifiedAt.Before(proof.EarliestValidUntil) {
			return ErrExpired
		}
		if err := grant(bounded, proof); err != nil {
			return err
		}
		if bounded.Err() != nil {
			return bounded.Err()
		}
		if !proof.EarliestValidUntil.IsZero() && !s.opts.Now().Before(proof.EarliestValidUntil) {
			return ErrExpired
		}
		return s.checkDatabaseIdentity()
	})
}

func (s *Store) proxyProofBindings(ctx context.Context, c *sql.Conn, id string) ([]binding, error) {
	rows, err := c.QueryContext(ctx, `SELECT public_key,address,device_id,managed_by,role,revoked_generation,applied FROM deviceauth_bindings WHERE device_id=? ORDER BY public_key LIMIT 16385`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []binding
	for rows.Next() {
		var b binding
		if err := rows.Scan(&b.key, &b.address, &b.deviceID, &b.managedBy, &b.role, &b.revoked, &b.applied); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	if len(result) > 16384 {
		return nil, ErrQuota
	}
	return result, rows.Err()
}

func (s *Scheduler) WithProxyConvergence(ctx context.Context, sources []string, grant func(context.Context, ProxyConvergenceProof) error) error {
	return s.store.WithProxyConvergence(ctx, s.executor, sources, grant)
}
