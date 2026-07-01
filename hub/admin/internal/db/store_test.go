package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "zongheng-vpn/hub/admin/internal/db/generated"
	"zongheng-vpn/hub/internal/auth"
)

func TestMaintainPrunesUnboundedTables(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	mustExec(t, store.Queries().UpsertAdminUser(ctx, generated.UpsertAdminUserParams{
		Username:     "admin",
		PasswordHash: "hash",
		CreatedAt:    formatTime(now.Add(-48 * time.Hour)),
		UpdatedAt:    formatTime(now.Add(-48 * time.Hour)),
	}))
	mustExec(t, store.Queries().CreateAdminSession(ctx, generated.CreateAdminSessionParams{
		ID:         "old-session",
		Username:   "admin",
		TokenHash:  "old-hash",
		CsrfToken:  "csrf",
		SourceIp:   "127.0.0.1",
		UserAgent:  "test",
		CreatedAt:  formatTime(now.Add(-2 * time.Hour)),
		LastSeenAt: formatTime(now.Add(-2 * time.Hour)),
		ExpiresAt:  formatTime(now.Add(-time.Hour)),
	}))
	mustExec(t, store.Queries().UpsertRotateLock(ctx, generated.UpsertRotateLockParams{
		EgressID:  "jp-android-01",
		StartedAt: formatTime(now.Add(-time.Hour)),
		UntilAt:   formatTime(now.Add(-time.Minute)),
	}))
	mustExec(t, store.Queries().UpsertTokenLease(ctx, generated.UpsertTokenLeaseParams{
		Token:       "ZH-OLD",
		MaskedToken: "ZH-***LD",
		ClientName:  "old-client",
		SourceIp:    "127.0.0.1",
		EgressID:    "jp-android-01",
		SeenAt:      formatTime(now.Add(-time.Hour)),
		ExpiresAt:   sql.NullString{String: formatTime(now.Add(-time.Minute)), Valid: true},
	}))

	for i, age := range []time.Duration{72 * time.Hour, 48 * time.Hour, 3 * time.Hour, 2 * time.Hour, time.Hour} {
		occurredAt := now.Add(-age)
		mustExec(t, store.InsertAudit(ctx, auth.AuditEvent{
			OccurredAt: occurredAt,
			Actor:      "test",
			SourceIP:   "127.0.0.1",
			EventType:  "admin.test",
			Target:     "audit-row",
			DetailJSON: `{"ok":true}`,
			Result:     "ok",
		}))
		mustExec(t, store.Queries().InsertLoginAttempt(ctx, generated.InsertLoginAttemptParams{
			OccurredAt: formatTime(occurredAt),
			Username:   "admin",
			SourceIp:   "127.0.0.1",
			Success:    int64(i % 2),
			ErrorCode:  "",
		}))
	}

	err := store.Maintain(ctx, MaintenancePolicy{
		Now:                   now,
		AuditRetention:        24 * time.Hour,
		MaxAuditEvents:        2,
		LoginAttemptRetention: 24 * time.Hour,
		MaxLoginAttempts:      2,
		Checkpoint:            true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := countRows(t, store, "audit_events"); got != 2 {
		t.Fatalf("audit_events count = %d, want 2", got)
	}
	if got := countRows(t, store, "admin_login_attempts"); got != 2 {
		t.Fatalf("admin_login_attempts count = %d, want 2", got)
	}
	if got := countRows(t, store, "admin_sessions"); got != 0 {
		t.Fatalf("admin_sessions count = %d, want 0", got)
	}
	if got := countRows(t, store, "token_leases"); got != 0 {
		t.Fatalf("token_leases count = %d, want 0", got)
	}
	if got := countRows(t, store, "rotate_locks"); got != 0 {
		t.Fatalf("rotate_locks count = %d, want 0", got)
	}
}

func TestInsertAuditCapsLargeDetailJSON(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	err := store.InsertAudit(ctx, auth.AuditEvent{
		OccurredAt: time.Now(),
		Actor:      strings.Repeat("a", 1024),
		SourceIP:   "127.0.0.1",
		EventType:  "admin.large",
		Target:     "target",
		DetailJSON: strings.Repeat("x", maxAuditDetailJSONBytes+1),
		Result:     "ok",
	})
	if err != nil {
		t.Fatal(err)
	}

	var actor, detail string
	row := store.db.QueryRowContext(ctx, "SELECT actor, detail_json FROM audit_events LIMIT 1")
	if err := row.Scan(&actor, &detail); err != nil {
		t.Fatal(err)
	}
	if len(actor) > maxAuditActorBytes {
		t.Fatalf("actor stored bytes = %d, want <= %d", len(actor), maxAuditActorBytes)
	}
	if len(detail) > 128 {
		t.Fatalf("detail_json stored bytes = %d, want compact truncation", len(detail))
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil {
		t.Fatalf("detail_json is not valid JSON: %q", detail)
	}
	if decoded["truncated"] != true {
		t.Fatalf("detail_json = %s, want truncated marker", detail)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustExec(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	row := store.db.QueryRow("SELECT count(*) FROM " + table)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
