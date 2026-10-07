package deviceauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
	"zongheng-vpn/shared/paths"
)

//go:embed schema.sql
var schema string

type Store struct {
	db                      *sql.DB
	opts                    Options
	policyJSON              string
	databasePath, fencePath string
	databaseInfo            os.FileInfo
}

// New uses the project's existing SQLite driver and database, without opening a
// second service or adopting production auth. It does not close or reconfigure
// the caller's sql.DB pool. Every connection used here sets its own pragmas.
func New(ctx context.Context, db *sql.DB, opts Options) (*Store, error) {
	if db == nil {
		return nil, ErrInvalid
	}
	// Clone BEFORE normalization. json.Unmarshal into an existing Policy can
	// reuse its slice backing array, so decoding back into opts is not a clone.
	// Strings/Protection fields are immutable values; only these slices alias.
	opts.Policy.AddressPools = append([]string(nil), opts.Policy.AddressPools...)
	opts.Policy.Protected = append([]Protection(nil), opts.Policy.Protected...)
	if err := normalizePolicy(&opts.Policy); err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := normalizeResources(&opts.Resources); err != nil {
		return nil, err
	}
	if opts.ActionTimeout == 0 {
		opts.ActionTimeout = 5 * time.Second
	}
	if opts.RevocationBudget == 0 {
		opts.RevocationBudget = 30 * time.Second
	}
	if opts.ActionTimeout <= 0 || opts.ActionTimeout > 30*time.Second || opts.RevocationBudget <= 0 || opts.RevocationBudget > 30*time.Second {
		return nil, fmt.Errorf("%w: execution/revocation budget", ErrInvalid)
	}
	path, info, err := databaseIdentity(ctx, db)
	if err != nil {
		return nil, err
	}
	fencePath := path + ".deviceauth.lock"
	if opts.FencePath != "" {
		absolute, err := filepath.Abs(opts.FencePath)
		if err != nil {
			return nil, err
		}
		parent, err := paths.CanonicalRoot(filepath.Dir(absolute))
		if err != nil || filepath.Join(parent, filepath.Base(absolute)) != fencePath {
			return nil, fmt.Errorf("%w: fence must be derived from canonical database file", ErrPolicy)
		}
	}
	policyJSON, err := json.Marshal(opts.Policy)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, opts: opts, policyJSON: string(policyJSON), databasePath: path, databaseInfo: info, fencePath: fencePath}
	unlock, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	err = s.write(ctx, func(c *sql.Conn) error {
		var existing int
		if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='deviceauth_meta'`).Scan(&existing); err != nil {
			return err
		}
		if existing != 0 {
			// Draft schema v1 is not silently adopted or partially altered. The
			// isolated authority has no automatic production migration contract.
			if err := s.checkPolicy(ctx, c); err != nil {
				return err
			}
		}
		if _, err := c.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO deviceauth_meta(singleton,schema_version,epoch,managed_by,policy_json,fence_path) VALUES(1,2,?,?,?,?)`, opts.Policy.Epoch, opts.Policy.ManagedBy, s.policyJSON, s.fencePath); err != nil {
			return err
		}
		return s.checkPolicy(ctx, c)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func normalizePolicy(p *Policy) error {
	if !identifier(p.Epoch) || !identifier(p.ManagedBy) || len(p.AddressPools) == 0 || len(p.AddressPools) > 64 || len(p.Protected) > 1024 {
		return ErrInvalid
	}
	for i, pool := range p.AddressPools {
		prefix, err := netip.ParsePrefix(pool)
		if err != nil || prefix.Addr().Is4In6() || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return fmt.Errorf("%w: address pool", ErrInvalid)
		}
		p.AddressPools[i] = prefix.String()
	}
	sort.Strings(p.AddressPools)
	for i := range p.AddressPools {
		if i > 0 && p.AddressPools[i] == p.AddressPools[i-1] {
			return ErrInvalid
		}
	}
	for i, protection := range p.Protected {
		if protection.PublicKey == "" && protection.Prefix == "" {
			return ErrInvalid
		}
		if protection.PublicKey != "" && !validKey(protection.PublicKey) {
			return ErrInvalid
		}
		if protection.Prefix != "" {
			prefix, err := netip.ParsePrefix(protection.Prefix)
			if err != nil || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
				return ErrInvalid
			}
			protection.Prefix = prefix.String()
		}
		p.Protected[i] = protection
	}
	sort.Slice(p.Protected, func(i, j int) bool {
		if p.Protected[i].PublicKey == p.Protected[j].PublicKey {
			return p.Protected[i].Prefix < p.Protected[j].Prefix
		}
		return p.Protected[i].PublicKey < p.Protected[j].PublicKey
	})
	return nil
}

func identifier(v string) bool {
	return v != "" && len(v) <= 256 && utf8.ValidString(v) && strings.TrimSpace(v) == v && !strings.ContainsFunc(v, unicode.IsControl)
}
func validKey(v string) bool {
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(raw) != 32 || base64.StdEncoding.EncodeToString(raw) != v {
		return false
	}
	for _, b := range raw {
		if b != 0 {
			return true
		}
	}
	return false
}
func (s *Store) address(key, value string) (string, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || prefix.Addr().Is4In6() || prefix.Bits() != prefix.Addr().BitLen() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().IsLoopback() {
		return "", ErrInvalid
	}
	permitted := false
	for _, pool := range s.opts.Policy.AddressPools {
		p, _ := netip.ParsePrefix(pool)
		permitted = permitted || p.Contains(prefix.Addr())
	}
	if !permitted {
		return "", ErrProtected
	}
	if s.protected(key, prefix) {
		return "", ErrProtected
	}
	return prefix.String(), nil
}
func (s *Store) protected(key string, prefix netip.Prefix) bool {
	for _, protection := range s.opts.Policy.Protected {
		if protection.PublicKey != "" && key == protection.PublicKey {
			return true
		}
		if protection.Prefix != "" {
			p, _ := netip.ParsePrefix(protection.Prefix)
			if p.Overlaps(prefix) {
				return true
			}
		}
	}
	return false
}

// write requires the persistent process fence already held. BEGIN IMMEDIATE
// additionally serializes SQLite writers (including other, unrelated Admin tables).
func (s *Store) write(ctx context.Context, fn func(*sql.Conn) error) error {
	if err := s.checkDatabaseIdentity(); err != nil {
		return err
	}
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, "PRAGMA busy_timeout=1000; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL;"); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer c.ExecContext(context.Background(), "ROLLBACK") // no effect after successful COMMIT
	if err = fn(c); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}
func (s *Store) checkPolicy(ctx context.Context, c *sql.Conn) error {
	var version int
	var epoch, managed, policy, fence string
	err := c.QueryRowContext(ctx, `SELECT schema_version,epoch,managed_by,policy_json,fence_path FROM deviceauth_meta WHERE singleton=1`).Scan(&version, &epoch, &managed, &policy, &fence)
	if err != nil {
		return err
	}
	if version != 2 || epoch != s.opts.Policy.Epoch || managed != s.opts.Policy.ManagedBy || policy != s.policyJSON || fence != s.fencePath {
		return ErrPolicy
	}
	return nil
}
func deviceRow(ctx context.Context, c *sql.Conn, id string) (Device, error) {
	var d Device
	var until int64
	err := c.QueryRowContext(ctx, `SELECT device_id,owner_id,role,state,valid_until,generation,applied_generation,COALESCE(current_key,''),COALESCE(current_address,'') FROM deviceauth_devices WHERE device_id=?`, id).Scan(&d.ID, &d.OwnerID, &d.Role, &d.State, &until, &d.Generation, &d.AppliedGeneration, &d.PublicKey, &d.Address)
	d.ValidUntil = time.Unix(0, until).UTC()
	return d, err
}
func operationRow(ctx context.Context, c *sql.Conn, id string) (Operation, error) {
	var op Operation
	var deadline int64
	err := c.QueryRowContext(ctx, `SELECT o.operation_id,o.device_id,o.action,o.epoch,o.generation,b.state,b.fence,b.attempts,b.last_error,o.deadline FROM deviceauth_operations o JOIN deviceauth_outbox b USING(operation_id) WHERE o.operation_id=?`, id).Scan(&op.ID, &op.DeviceID, &op.Action, &op.Epoch, &op.Generation, &op.State, &op.Fence, &op.Attempts, &op.LastError, &deadline)
	op.Deadline = time.Unix(0, deadline).UTC()
	return op, err
}

// Enroll records an already-authorized customer device. It does not accept a
// token, generate private keys, adopt a runtime peer, or bootstrap credentials.
func (s *Store) Enroll(ctx context.Context, e Enrollment) error {
	if !identifier(e.DeviceID) || !identifier(e.OwnerID) || e.Role != "customer" || !e.ValidUntil.After(s.opts.Now()) || e.ValidUntil.Year() > 2200 {
		return ErrInvalid
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
		if err := s.grantResources(ctx, c); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `INSERT INTO deviceauth_devices(device_id,owner_id,role,state,valid_until) VALUES(?,?,'customer','active',?)`, e.DeviceID, e.OwnerID, e.ValidUntil.UTC().UnixNano())
		if err != nil {
			return fmt.Errorf("%w: device enrollment", ErrConflict)
		}
		return nil
	})
}

// Submit atomically writes desired state, tombstones, operation and durable outbox.
// A returned operation is accepted, never evidence that a WG peer was changed.
func (s *Store) Submit(ctx context.Context, cmd Command) (Operation, error) {
	return s.submit(ctx, cmd, false)
}

// fenceHeld is only used by the trusted executor to persist an observed expiry;
// all public desired-state entrypoints always acquire the same process fence.
func (s *Store) submit(ctx context.Context, cmd Command, fenceHeld bool) (Operation, error) {
	return s.submitChecked(ctx, cmd, fenceHeld, nil)
}

// check executes within the same authority fence and SQLite transaction as the
// desired mutation; failed authorization and rejected commands consume nothing.
func (s *Store) submitChecked(ctx context.Context, cmd Command, fenceHeld bool, check func(*sql.Conn) error, complete ...func(*sql.Conn, Operation) error) (Operation, error) {
	if !identifier(cmd.Actor.ID) || !identifier(cmd.Actor.OwnerID) || !identifier(cmd.DeviceID) || !identifier(cmd.IdempotencyKey) || cmd.ExpectedGeneration < 0 || len(cmd.Reason) > 1024 || !utf8.ValidString(cmd.Reason) {
		return Operation{}, ErrInvalid
	}
	switch cmd.Action {
	case "apply", "disable", "expire", "revoke":
	default:
		return Operation{}, ErrInvalid
	}
	if cmd.Action == "apply" {
		if !validKey(cmd.PublicKey) {
			return Operation{}, ErrInvalid
		}
		address, err := s.address(cmd.PublicKey, cmd.Address)
		if err != nil {
			return Operation{}, err
		}
		cmd.Address = address
	} else if cmd.PublicKey != "" || cmd.Address != "" {
		return Operation{}, ErrInvalid
	}
	encoded, _ := json.Marshal(cmd)
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	if !fenceHeld {
		unlock, err := s.acquire(ctx)
		if err != nil {
			return Operation{}, err
		}
		defer unlock()
	}
	var op Operation
	err := s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		if check != nil {
			if err := check(c); err != nil {
				return err
			}
		}
		d, err := deviceRow(ctx, c, cmd.DeviceID)
		if err != nil {
			return err
		}
		if d.OwnerID != cmd.Actor.OwnerID || d.Role != "customer" {
			return ErrUnauthorized
		}
		now := s.opts.Now().UTC()
		// Recheck authorization BEFORE consulting historical idempotency state.
		if cmd.Action == "apply" && d.State != "active" {
			return ErrUnauthorized
		}
		if cmd.Action == "apply" && !now.Before(d.ValidUntil) {
			return ErrExpired
		}
		if cmd.Action == "apply" {
			if err := s.grantResources(ctx, c); err != nil {
				return err
			}
			// Superseding an operation cannot reset an old-key revocation budget.
			var blocked bool
			err := c.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deviceauth_operations o JOIN deviceauth_outbox b USING(operation_id) WHERE o.device_id=? AND b.state IN ('pending','degraded') AND o.deadline<=?) OR EXISTS(SELECT 1 FROM deviceauth_tombstones t JOIN deviceauth_bindings b USING(public_key) WHERE b.device_id=? AND t.verified_at IS NULL AND t.committed_at<=?)`, d.ID, now.UnixNano(), d.ID, now.Add(-s.opts.RevocationBudget).UnixNano()).Scan(&blocked)
			if err != nil {
				return err
			}
			if blocked {
				// Rejecting a grant rolls back challenge/request/audit consumption
				// too. Persist the existing degradation separately under this same
				// fence, never by committing a rejected command's auth transaction.
				return ErrOverdue
			}
		}
		if cmd.Action == "expire" && now.Before(d.ValidUntil) {
			return ErrInvalid
		}
		var oldID, oldDigest string
		err = c.QueryRowContext(ctx, `SELECT operation_id,request_digest FROM deviceauth_operations WHERE actor_id=? AND action=? AND idempotency_key=?`, cmd.Actor.ID, cmd.Action, cmd.IdempotencyKey).Scan(&oldID, &oldDigest)
		if err == nil {
			if oldDigest != digest {
				return ErrConflict
			}
			op, err = operationRow(ctx, c, oldID)
			if err == nil {
				for _, fn := range complete {
					if err = fn(c, op); err != nil {
						return err
					}
				}
			}
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if d.Generation != cmd.ExpectedGeneration {
			return ErrGeneration
		}
		if d.Generation == int64(^uint64(0)>>1) {
			return ErrGeneration
		}
		generation := d.Generation + 1
		if cmd.Action == "apply" {
			var bindingCount int
			if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM deviceauth_bindings WHERE device_id=?`, d.ID).Scan(&bindingCount); err != nil {
				return err
			}
			if bindingCount >= 4096 {
				return fmt.Errorf("%w: binding history quota", ErrInvalid)
			}
			var keyOwner string
			err := c.QueryRowContext(ctx, `SELECT device_id FROM deviceauth_bindings WHERE public_key=?`, cmd.PublicKey).Scan(&keyOwner)
			if err == nil {
				return ErrConflict
			} // old keys, even of this device, never resurrect
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			var addressOwner string
			err = c.QueryRowContext(ctx, `SELECT device_id FROM deviceauth_addresses WHERE address=?`, cmd.Address).Scan(&addressOwner)
			if err == nil && addressOwner != d.ID {
				return ErrConflict
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = c.ExecContext(ctx, `INSERT OR IGNORE INTO deviceauth_addresses(address,device_id) VALUES(?,?)`, cmd.Address, d.ID); err != nil {
				return err
			}
		}
		// Every old key remains in history. Superseding the rotate job cannot
		// supersede its still-unverified old-key removal obligation.
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_tombstones(public_key,generation,reason,committed_at) SELECT public_key,?,?,? FROM deviceauth_bindings WHERE device_id=? AND revoked_generation=0`, generation, cmd.Reason, now.UnixNano(), d.ID); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `UPDATE deviceauth_bindings SET revoked_generation=? WHERE device_id=? AND revoked_generation=0`, generation, d.ID); err != nil {
			return err
		}
		state := cmd.Action
		switch cmd.Action {
		case "apply":
			state = "active"
		case "disable":
			state = "disabled"
		case "expire":
			state = "expired"
		case "revoke":
			state = "revoked"
		}
		// Permanent device revocation cannot be weakened by a later disable/expire.
		if d.State == "revoked" {
			state = "revoked"
		}
		if cmd.Action == "apply" {
			if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_bindings(public_key,device_id,address,managed_by,role,created_generation) VALUES(?,?,?,?,'customer',?)`, cmd.PublicKey, d.ID, cmd.Address, s.opts.Policy.ManagedBy, generation); err != nil {
				return err
			}
			_, err = c.ExecContext(ctx, `UPDATE deviceauth_devices SET state=?,generation=?,current_key=?,current_address=? WHERE device_id=? AND generation=?`, state, generation, cmd.PublicKey, cmd.Address, d.ID, d.Generation)
		} else {
			_, err = c.ExecContext(ctx, `UPDATE deviceauth_devices SET state=?,generation=?,current_key=NULL,current_address=NULL WHERE device_id=? AND generation=?`, state, generation, d.ID, d.Generation)
		}
		if err != nil {
			return err
		}
		// Desired generation supersedes old jobs immediately. Their binding,
		// tombstone and prepared intents stay intact; only the latest job drives
		// all retained removal obligations. A never-run old pending row must not
		// permanently block grants after latest reconciliation has verified them.
		if _, err = c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='superseded' WHERE state IN ('pending','degraded') AND operation_id IN (SELECT operation_id FROM deviceauth_operations WHERE device_id=? AND generation<?)`, d.ID, generation); err != nil {
			return err
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		id := hex.EncodeToString(random[:])
		deadline := now.Add(s.opts.RevocationBudget)
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_operations(operation_id,actor_id,owner_id,action,idempotency_key,request_digest,device_id,epoch,generation,committed_at,deadline) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, cmd.Actor.ID, cmd.Actor.OwnerID, cmd.Action, cmd.IdempotencyKey, digest, d.ID, s.opts.Policy.Epoch, generation, now.UnixNano(), deadline.UnixNano()); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, `INSERT INTO deviceauth_outbox(operation_id) VALUES(?)`, id); err != nil {
			return err
		}
		op, err = operationRow(ctx, c, id)
		if err == nil {
			for _, fn := range complete {
				if err = fn(c, op); err != nil {
					return err
				}
			}
		}
		return err
	})
	if errors.Is(err, ErrOverdue) {
		degradeErr := s.write(ctx, func(c *sql.Conn) error {
			if err := s.checkPolicy(ctx, c); err != nil {
				return err
			}
			_, err := c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='degraded',last_error='deadline_exceeded' WHERE state IN ('pending','degraded') AND operation_id IN (SELECT operation_id FROM deviceauth_operations WHERE device_id=?)`, cmd.DeviceID)
			return err
		})
		if degradeErr != nil {
			return Operation{}, degradeErr
		}
		return Operation{}, ErrOverdue
	}
	return op, err
}

// RefreshDeadlines persists overdue signals without clearing tombstones or
// granting permissions. The opt-in device authority scheduler calls it too.
func (s *Store) RefreshDeadlines(ctx context.Context) (int64, error) {
	unlock, err := s.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()
	var count int64
	err = s.write(ctx, func(c *sql.Conn) error {
		if err := s.checkPolicy(ctx, c); err != nil {
			return err
		}
		now := s.opts.Now().UTC()
		r, err := c.ExecContext(ctx, `UPDATE deviceauth_outbox SET state='degraded',last_error='deadline_exceeded' WHERE state IN ('pending','degraded') AND last_error!='deadline_exceeded' AND operation_id IN (SELECT o.operation_id FROM deviceauth_operations o WHERE o.deadline<=? OR EXISTS(SELECT 1 FROM deviceauth_tombstones t JOIN deviceauth_bindings b USING(public_key) WHERE b.device_id=o.device_id AND t.verified_at IS NULL AND t.committed_at<=?))`, now.UnixNano(), now.Add(-s.opts.RevocationBudget).UnixNano())
		if err != nil {
			return err
		}
		count, err = r.RowsAffected()
		return err
	})
	return count, err
}

// Inspect methods expose no private credential material and do not authorize requests.
func (s *Store) Device(ctx context.Context, id string) (Device, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return Device{}, err
	}
	defer c.Close()
	return deviceRow(ctx, c, id)
}
func (s *Store) Operation(ctx context.Context, id string) (Operation, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer c.Close()
	return operationRow(ctx, c, id)
}
func (s *Store) Pending(ctx context.Context) ([]Operation, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	rows, err := c.QueryContext(ctx, `SELECT o.operation_id FROM deviceauth_operations o JOIN deviceauth_outbox b USING(operation_id) WHERE b.state IN ('pending','degraded') ORDER BY o.committed_at,o.operation_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var result []Operation
	for _, id := range ids {
		op, err := operationRow(ctx, c, id)
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, nil
}
