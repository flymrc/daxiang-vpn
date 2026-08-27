package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	generated "zongheng-vpn/hub/admin/internal/db/generated"
	"zongheng-vpn/hub/internal/auth"
)

const (
	maxAuditActorBytes      = 256
	maxAuditSourceIPBytes   = 128
	maxAuditEventTypeBytes  = 128
	maxAuditTargetBytes     = 256
	maxAuditDetailJSONBytes = 4096
	maxAuditResultBytes     = 64
	maxAuditErrorCodeBytes  = 128
	maxClientProductBytes   = 64
	maxClientVersionBytes   = 64
)

//go:embed schema.sql
var schemaFS embed.FS

type Store struct {
	db *sql.DB
	q  *generated.Queries
}

type MaintenancePolicy struct {
	Now                   time.Time
	AuditRetention        time.Duration
	MaxAuditEvents        int64
	LoginAttemptRetention time.Duration
	MaxLoginAttempts      int64
	Checkpoint            bool
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
	occurredAt := defaultTime(event.OccurredAt)
	auditParams := generated.InsertAuditEventParams{
		OccurredAt: formatTime(occurredAt),
		Actor:      nonempty(truncateText(event.Actor, maxAuditActorBytes), "unknown"),
		SourceIp:   truncateText(event.SourceIP, maxAuditSourceIPBytes),
		EventType:  truncateText(event.EventType, maxAuditEventTypeBytes),
		Target:     truncateText(event.Target, maxAuditTargetBytes),
		DetailJson: sanitizeDetailJSON(event.DetailJSON),
		Result:     nonempty(truncateText(event.Result, maxAuditResultBytes), "unknown"),
		ErrorCode:  truncateText(event.ErrorCode, maxAuditErrorCodeBytes),
	}
	if event.MigrationObservation == nil {
		return s.q.InsertAuditEvent(ctx, auditParams)
	}
	observationParams, err := migrationObservationParams(event, occurredAt)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.q.WithTx(tx)
	if err := qtx.InsertAuditEvent(ctx, auditParams); err != nil {
		return err
	}
	if err := qtx.UpsertClientMigrationObservation(ctx, observationParams); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListClientMigrationObservations(ctx context.Context) ([]generated.ClientMigrationObservation, error) {
	return s.q.ListClientMigrationObservations(ctx)
}

func migrationObservationParams(event auth.AuditEvent, auditTime time.Time) (generated.UpsertClientMigrationObservationParams, error) {
	observation := event.MigrationObservation
	if event.EventType != "client.bootstrap" || event.Result != "ok" {
		return generated.UpsertClientMigrationObservationParams{}, errors.New("migration observation requires successful client.bootstrap audit")
	}
	if observation.TokenID == "" {
		return generated.UpsertClientMigrationObservationParams{}, errors.New("migration observation token id is required")
	}
	switch observation.Ingress {
	case "compat", "trusted_proxy":
	default:
		return generated.UpsertClientMigrationObservationParams{}, fmt.Errorf("invalid migration ingress %q", observation.Ingress)
	}
	switch observation.KeyMode {
	case "client_generated", "server_legacy":
	default:
		return generated.UpsertClientMigrationObservationParams{}, fmt.Errorf("invalid migration key mode %q", observation.KeyMode)
	}
	switch observation.MigrationClass {
	case "secure_bootstrap", "legacy", "unknown":
	default:
		return generated.UpsertClientMigrationObservationParams{}, fmt.Errorf("invalid migration class %q", observation.MigrationClass)
	}
	occurredAt := observation.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = auditTime
	}
	privateKeyReturned := int64(0)
	if observation.PrivateKeyReturned {
		privateKeyReturned = 1
	}
	return generated.UpsertClientMigrationObservationParams{
		TokenID:            observation.TokenID,
		OccurredAtUnixNs:   occurredAt.UnixNano(),
		OccurredAt:         formatTime(occurredAt),
		ClientProduct:      truncateText(observation.ClientProduct, maxClientProductBytes),
		ClientVersion:      truncateText(observation.ClientVersion, maxClientVersionBytes),
		ProtocolVersion:    int64(observation.ProtocolVersion),
		Ingress:            observation.Ingress,
		KeyMode:            observation.KeyMode,
		PrivateKeyReturned: privateKeyReturned,
		MigrationClass:     observation.MigrationClass,
	}, nil
}

func (s *Store) Maintain(ctx context.Context, policy MaintenancePolicy) error {
	if s == nil || s.q == nil {
		return nil
	}
	now := policy.Now
	if now.IsZero() {
		now = time.Now()
	}
	nowText := formatTime(now)
	if err := s.q.DeleteExpiredAdminSessions(ctx, nowText); err != nil {
		return err
	}
	if err := s.q.DeleteExpiredTokenLeases(ctx, sql.NullString{String: nowText, Valid: true}); err != nil {
		return err
	}
	if err := s.q.DeleteExpiredRotateLocks(ctx, nowText); err != nil {
		return err
	}
	if policy.AuditRetention > 0 {
		if err := s.q.DeleteAuditEventsBefore(ctx, formatTime(now.Add(-policy.AuditRetention))); err != nil {
			return err
		}
	}
	if policy.MaxAuditEvents > 0 {
		if err := s.q.PruneAuditEvents(ctx, policy.MaxAuditEvents); err != nil {
			return err
		}
	}
	if policy.LoginAttemptRetention > 0 {
		if err := s.q.DeleteAdminLoginAttemptsBefore(ctx, formatTime(now.Add(-policy.LoginAttemptRetention))); err != nil {
			return err
		}
	}
	if policy.MaxLoginAttempts > 0 {
		if err := s.q.PruneAdminLoginAttempts(ctx, policy.MaxLoginAttempts); err != nil {
			return err
		}
	}
	if policy.Checkpoint {
		return s.checkpointWAL(ctx)
	}
	return nil
}

func (s *Store) checkpointWAL(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var busy, logFrames, checkpointedFrames int
		if err := rows.Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
			return err
		}
	}
	return rows.Err()
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

func sanitizeDetailJSON(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	if len(value) > maxAuditDetailJSONBytes {
		return fmt.Sprintf(`{"truncated":true,"original_bytes":%d}`, len(value))
	}
	if json.Valid([]byte(value)) {
		return value
	}
	raw, err := json.Marshal(truncateText(value, 512))
	if err != nil {
		return "{}"
	}
	return fmt.Sprintf(`{"raw":%s}`, raw)
}

func truncateText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	for len(value) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		if size <= 0 {
			return ""
		}
		value = value[:len(value)-size]
	}
	return value
}
