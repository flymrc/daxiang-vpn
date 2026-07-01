package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	generated "zongheng-vpn/hub/admin/internal/db/generated"
	"zongheng-vpn/hub/internal/auth"
)

//go:embed schema.sql
var schemaFS embed.FS

type Store struct {
	db *sql.DB
	q  *generated.Queries
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("admin db path is required")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	data, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(string(data)); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, q: generated.New(db)}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Queries() *generated.Queries {
	if s == nil {
		return nil
	}
	return s.q
}

func (s *Store) EnsureAdminUser(ctx context.Context, username string, passwordHash string, now time.Time) error {
	if username == "" || passwordHash == "" {
		return nil
	}
	return s.q.UpsertAdminUser(ctx, generated.UpsertAdminUserParams{
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    formatTime(now),
		UpdatedAt:    formatTime(now),
	})
}

func (s *Store) InsertAudit(ctx context.Context, event auth.AuditEvent) error {
	return s.q.InsertAuditEvent(ctx, generated.InsertAuditEventParams{
		OccurredAt: formatTime(defaultTime(event.OccurredAt)),
		Actor:      nonempty(event.Actor, "unknown"),
		SourceIp:   event.SourceIP,
		EventType:  event.EventType,
		Target:     event.Target,
		DetailJson: nonempty(event.DetailJSON, "{}"),
		Result:     nonempty(event.Result, "unknown"),
		ErrorCode:  event.ErrorCode,
	})
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func defaultTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

func nonempty(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
