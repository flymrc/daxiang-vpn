package deviceauth

import (
	"context"
	"database/sql"
)

func normalizeResources(r *ResourceLimits) error {
	defaults := ResourceLimits{GrantHighWaterBytes: 64 << 20, Devices: 10000, Credentials: 50000, Activations: 20000, Requests: 200000, Audits: 200000, Cancellations: 20000}
	pairs := [][2]*int64{{&r.GrantHighWaterBytes, &defaults.GrantHighWaterBytes}, {&r.Devices, &defaults.Devices}, {&r.Credentials, &defaults.Credentials}, {&r.Activations, &defaults.Activations}, {&r.Requests, &defaults.Requests}, {&r.Audits, &defaults.Audits}, {&r.Cancellations, &defaults.Cancellations}}
	for _, p := range pairs {
		if *p[0] == 0 {
			*p[0] = *p[1]
		}
		if *p[0] <= 0 || *p[0] > *p[1] {
			return ErrInvalid
		}
	}
	return nil
}

// Every grant/activation/rotation uses this ceiling inside its desired/auth
// transaction. Safety disable/revoke/expiry bypass it; an HTTP credential stops
// authorizing mutations immediately after its first disable/revoke. Thus at most
// one remaining signed safety transition per enrolled device uses the reserve.
// Permanent request/audit/credential/tombstone histories are never discarded.
func (s *Store) grantResources(ctx context.Context, c *sql.Conn) error {
	var pages, size int64
	if err := c.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return err
	}
	if err := c.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return err
	}
	if size <= 0 || pages >= s.opts.Resources.GrantHighWaterBytes/size {
		return ErrQuota
	}
	counts := []struct {
		table string
		limit int64
	}{{"deviceauth_devices", s.opts.Resources.Devices}, {"deviceauth_credentials", s.opts.Resources.Credentials}, {"deviceauth_activations", s.opts.Resources.Activations}, {"deviceauth_requests", s.opts.Resources.Requests}, {"deviceauth_audit", s.opts.Resources.Audits}}
	for _, v := range counts {
		var count int64
		if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+v.table).Scan(&count); err != nil {
			return err
		}
		if count >= v.limit {
			return ErrQuota
		}
	}
	return nil
}

func (s *Store) SweepChallenges(ctx context.Context) error {
	unlock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `DELETE FROM deviceauth_challenges WHERE expires<=?`, s.opts.Now().Unix())
		return err
	})
}
