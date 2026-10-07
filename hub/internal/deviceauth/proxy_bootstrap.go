package deviceauth

import (
	"context"
	"database/sql"
	"net/netip"
	"time"

	dc "zongheng-vpn/shared/devicecontract"
)

func mustProxyPrefix(p dc.ProxyRouteProfile) netip.Prefix {
	return netip.MustParsePrefix(p.AllowedIps[0])
}

// ValidateProxyProfile is a trusted hosting check, not an HTTP authority grant.
// No caller can choose the executor interface through a bootstrap request.
func (s *Store) ValidateProxyProfile(ctx context.Context, profile dc.ProxyRouteProfile, wgInterface string) error {
	if dc.ValidateProxyRouteProfile(profile) != nil || profile.WgInterface != wgInterface || profile.AuthorityEpoch != s.opts.Policy.Epoch || profile.ManagedBy != s.opts.Policy.ManagedBy {
		return ErrPolicy
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		// The Hub proxy address must be reserved as an infrastructure address.
		// This prevents apply from allocating it to a customer peer.
		if !s.protected("", mustProxyPrefix(profile)) {
			return ErrProtected
		}
		return nil
	})
}

// ProxyBootstrapSigned consumes one read nonce under the same authority fence
// and SQL transaction as the current credential, binding and convergence check.
// It never grants/changes desired state, allocates an address or executes WG.
// Returned facts are a short TLS projection, not an offline signed permit.
func (s *Store) ProxyBootstrapSigned(ctx context.Context, r SignedRequest, expectedGeneration int64, publicKey string, profile dc.ProxyRouteProfile, wgInterface string) (dc.ProxyBootstrap, error) {
	var out dc.ProxyBootstrap
	if r.Purpose != "proxy.bootstrap" || r.Method != "POST" || r.Path != "/api/v2/proxy/bootstrap" {
		return out, ErrUnauthorized
	}
	if expectedGeneration < 1 || expectedGeneration > dc.ProxySafeInteger || !dc.ValidWireGuardPublicKey(publicKey) {
		return out, ErrInvalid
	}
	if dc.ValidateProxyRouteProfile(profile) != nil || profile.WgInterface != wgInterface || profile.AuthorityEpoch != s.opts.Policy.Epoch || profile.ManagedBy != s.opts.Policy.ManagedBy {
		return out, ErrPolicy
	}
	profile.AllowedIps = append([]string(nil), profile.AllowedIps...)
	digest, err := dc.ProxyRouteProfileDigest(profile)
	if err != nil {
		return out, ErrInvalid
	}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return out, err
	}
	defer unlock()
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		if !s.protected("", mustProxyPrefix(profile)) {
			return ErrProtected
		}
		cr, _, err := s.credential(ctx, c, r.DeviceID, r.CredentialID)
		if err != nil {
			return err
		}
		d, err := deviceRow(ctx, c, r.DeviceID)
		if err != nil {
			return err
		}
		if d.Role != "customer" || d.State != "active" || d.ID != r.DeviceID || d.PublicKey != publicKey || d.Generation != expectedGeneration || d.AppliedGeneration != d.Generation || !dc.ValidProxyCustomerAddress(d.Address) {
			return ErrGeneration
		}
		if _, err := s.address(publicKey, d.Address); err != nil {
			return err
		}
		var address, owner, managedBy, role string
		var created, revoked, removed, applied int64
		if err := c.QueryRowContext(ctx, `SELECT address,device_id,managed_by,role,created_generation,revoked_generation,removed_generation,applied FROM deviceauth_bindings WHERE public_key=?`, publicKey).Scan(&address, &owner, &managedBy, &role, &created, &revoked, &removed, &applied); err != nil {
			return ErrVerification
		}
		if address != d.Address || owner != d.ID || managedBy != s.opts.Policy.ManagedBy || role != "customer" || created != d.Generation || revoked != 0 || removed != 0 || applied != 1 {
			return ErrVerification
		}
		var reservedOwner string
		if err := c.QueryRowContext(ctx, `SELECT device_id FROM deviceauth_addresses WHERE address=?`, address).Scan(&reservedOwner); err != nil || reservedOwner != d.ID {
			return ErrVerification
		}
		var converged, negative int
		if err := c.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deviceauth_operations o JOIN deviceauth_outbox b USING(operation_id) WHERE o.device_id=? AND o.epoch=? AND o.generation=? AND o.action='apply' AND b.state='done' AND b.verified_at IS NOT NULL), EXISTS(SELECT 1 FROM deviceauth_tombstones WHERE public_key=?) OR EXISTS(SELECT 1 FROM deviceauth_bindings b WHERE b.device_id=? AND b.revoked_generation>0 AND (b.created_generation>=? OR b.revoked_generation>? OR b.removed_generation!=? OR EXISTS(SELECT 1 FROM deviceauth_tombstones t WHERE t.public_key=b.public_key AND (t.verified_at IS NULL OR t.generation>?))))`, d.ID, s.opts.Policy.Epoch, d.Generation, publicKey, d.ID, d.Generation, d.Generation, d.Generation, d.Generation).Scan(&converged, &negative); err != nil {
			return err
		}
		if converged != 1 || negative != 0 {
			return ErrVerification
		}
		if err := s.consume(ctx, c, r, "", cr.PublicKey); err != nil {
			return err
		}
		now := s.opts.Now().UTC()
		if now.Unix() <= 0 || now.Year() > 2200 || !now.Before(cr.ValidUntil) || !now.Before(d.ValidUntil) {
			return ErrExpired
		}
		expires := now.Add(30 * time.Second)
		if cr.ValidUntil.Before(expires) {
			expires = cr.ValidUntil
		}
		if d.ValidUntil.Before(expires) {
			expires = d.ValidUntil
		}
		if expires.Unix() <= now.Unix() {
			return ErrExpired
		}
		out = dc.ProxyBootstrap{Version: 1, DeviceId: d.ID, CredentialId: cr.ID, WireguardPublicKey: d.PublicKey, Address: d.Address, DesiredGeneration: d.Generation, AppliedGeneration: d.AppliedGeneration, IssuedUnixSeconds: now.Unix(), ExpiresUnixSeconds: expires.Unix(), Profile: profile, ProfileSha256: digest}
		return nil
	})
	if err != nil {
		return dc.ProxyBootstrap{}, err
	}
	// A delayed commit must not emit an already-expired projection. The consumed
	// read nonce can be retried with a fresh proof; no authorization was mutated.
	if s.opts.Now().Unix() >= out.ExpiresUnixSeconds {
		return dc.ProxyBootstrap{}, ErrExpired
	}
	return out, nil
}
