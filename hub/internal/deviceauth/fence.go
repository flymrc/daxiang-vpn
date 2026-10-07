package deviceauth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"zongheng-vpn/shared/paths"
)

// SQLite aliases must resolve to one file/fence. Hard-linked database or lock
// files are rejected. The parent directory is a deployment trust boundary, not
// an attacker-writable temp directory; its ACL/ownership must be installed by the
// future hosting service. We never rewrite ACLs or unlink a persistent fence.
func databaseIdentity(ctx context.Context, db *sql.DB) (string, os.FileInfo, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var file string
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return "", nil, err
		}
		if name == "main" {
			file = path
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	if file == "" {
		return "", nil, fmt.Errorf("%w: a local file-backed SQLite database is required", ErrInvalid)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", nil, err
	}
	parent, err := paths.CanonicalRoot(filepath.Dir(abs))
	if err != nil {
		return "", nil, err
	}
	canonical := filepath.Join(parent, filepath.Base(abs))
	f, err := openRegular(canonical, false)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	canonical, err = regularPath(f)
	if err != nil {
		return "", nil, err
	}
	info, err := f.Stat()
	return canonical, info, err
}

func (s *Store) acquire(ctx context.Context) (func(), error) {
	unlock, _, err := s.acquirePinned(ctx)
	return unlock, err
}

// The executor-only variant exposes the already-locked open file description to
// an independent Linux supervisor. Its inherited descriptor keeps the SAME
// flock alive after an authority crash; it never unlocks it explicitly.
func (s *Store) acquirePinned(ctx context.Context) (func(), *os.File, error) {
	parent, err := paths.CanonicalRoot(filepath.Dir(s.databasePath))
	if err != nil || parent != filepath.Dir(s.databasePath) {
		return nil, nil, fmt.Errorf("%w: database directory changed", ErrPolicy)
	}
	dbFile, err := openRegular(s.databasePath, false)
	if err != nil {
		return nil, nil, err
	}
	info, err := dbFile.Stat()
	_ = dbFile.Close()
	if err != nil || !os.SameFile(info, s.databaseInfo) {
		return nil, nil, fmt.Errorf("%w: database file changed", ErrPolicy)
	}
	f, err := openRegular(s.fencePath, true)
	if err != nil {
		return nil, nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		err = lockFile(f)
		if err == nil {
			break
		}
		if !isLockBusy(err) {
			_ = f.Close()
			return nil, nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	locked, err := f.Stat()
	named, nameErr := os.Lstat(s.fencePath)
	if err != nil || nameErr != nil || !os.SameFile(locked, named) || named.Mode()&os.ModeSymlink != 0 {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: fence file changed", ErrPolicy)
	}
	// Waiting for the fence may have taken arbitrarily long. Re-open/check the
	// database AFTER acquiring it, and keep the no-delete Windows handle through
	// every transaction and external action. A pre-wait inode check alone allows
	// a restored old snapshot to authorize a queued stale apply.
	parent, err = paths.CanonicalRoot(filepath.Dir(s.databasePath))
	if err != nil || parent != filepath.Dir(s.databasePath) {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, nil, ErrPolicy
	}
	pinned, err := openRegular(s.databasePath, false)
	if err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, nil, err
	}
	info, err = pinned.Stat()
	if err != nil || !os.SameFile(info, s.databaseInfo) {
		_ = pinned.Close()
		_ = unlockFile(f)
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: database file changed while waiting for fence", ErrPolicy)
	}
	return func() { _ = unlockFile(f); _ = f.Close(); _ = pinned.Close() }, f, nil
}

// Rechecked at each SQLite phase and before each external mutation. On Unix an
// open descriptor does not prohibit unlink, so a replaced namespace fails closed.
// Trusted-directory ownership and coordinated backup/restore remain requirements;
// this does not authorize an in-place rollback of the same SQLite inode.
func (s *Store) checkDatabaseIdentity() error {
	parent, err := paths.CanonicalRoot(filepath.Dir(s.databasePath))
	if err != nil || parent != filepath.Dir(s.databasePath) {
		return ErrPolicy
	}
	f, err := openRegular(s.databasePath, false)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(info, s.databaseInfo) {
		return ErrPolicy
	}
	return nil
}
