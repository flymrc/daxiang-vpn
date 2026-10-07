package deviceauth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SweepExpiry creates durable revoke work even when no client request arrives.
func (s *Store) SweepExpiry(ctx context.Context) error {
	unlock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err = s.checkDatabaseIdentity(); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT device_id FROM deviceauth_devices WHERE state='active' AND valid_until<=? ORDER BY device_id`, s.opts.Now().UnixNano())
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
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
	for _, id := range ids {
		c, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		d, err := deviceRow(ctx, c, id)
		c.Close()
		if err != nil {
			return err
		}
		if err = s.expireLocked(d); !errors.Is(err, ErrExpired) {
			return err
		}
	}
	return nil
}

type SchedulerStatus struct {
	LastCycle time.Time
	ErrorCode string
}
type Scheduler struct {
	store    *Store
	executor Executor
	runMu    sync.Mutex
	mu       sync.Mutex
	status   SchedulerStatus
	cursor   int
}

func NewScheduler(s *Store, e Executor) (*Scheduler, error) {
	if s == nil || e == nil {
		return nil, ErrInvalid
	}
	return &Scheduler{store: s, executor: e}, nil
}
func (s *Scheduler) Status() SchedulerStatus { s.mu.Lock(); defer s.mu.Unlock(); return s.status }

// Tick is also called at startup. Completed generations are reconciled again so
// a WG restart from old source configuration cannot bypass persisted tombstones.
// Work rotates fairly when a cycle's total budget ends; no failed peer starves
// the later devices indefinitely. Unknown runtime peers are never enumerated as
// customer work merely because they exist in WG.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	s.status.LastCycle = s.store.opts.Now().UTC()
	s.status.ErrorCode = ""
	s.mu.Unlock()
	fail := func(err error) error {
		s.mu.Lock()
		s.status.ErrorCode = "reconciliation_degraded"
		s.mu.Unlock()
		return err
	}
	if err := s.store.SweepExpiry(ctx); err != nil {
		return fail(err)
	}
	if err := s.store.SweepChallenges(ctx); err != nil {
		return fail(err)
	}
	if _, err := s.store.RefreshDeadlines(ctx); err != nil {
		return fail(err)
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT device_id FROM deviceauth_devices WHERE generation>0 ORDER BY device_id`)
	if err != nil {
		return fail(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return fail(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fail(err)
	}
	if len(ids) == 0 {
		s.cursor = 0
		return nil
	}
	s.cursor %= len(ids)
	var result error
	for range ids {
		id := ids[s.cursor]
		s.cursor = (s.cursor + 1) % len(ids)
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		if err = s.store.Reconcile(ctx, id, s.executor); err != nil && !errors.Is(err, ErrSuperseded) && !errors.Is(err, ErrExpired) {
			result = err
		}
	}
	if result != nil {
		return fail(result)
	}
	return nil
}
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval < 100*time.Millisecond || interval > 5*time.Second {
		return ErrInvalid
	}
	// Immediate restart reconciliation; each pass and each subprocess are bounded.
	for {
		cycle, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = s.Tick(cycle)
		cancel()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
