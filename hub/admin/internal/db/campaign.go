package db

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"time"

	gen "zongheng-vpn/hub/admin/internal/db/generated"
	"zongheng-vpn/hub/internal/auth"
)

const MaxCampaignMembers = 4096
const MaxObserverRuns = 4096
const MaxInventoryBytes = 4 << 20
const MaxInstallationRefs = 16
const MaxSafeCounter int64 = 9007199254740991

var ErrInvalidInventory = errors.New("invalid_inventory")
var ErrInventoryApproval = errors.New("inventory_approval_required")
var ErrInventoryImmutable = errors.New("inventory_already_registered")
var ErrProjectionUnavailable = errors.New("projection_unavailable")
var id12 = regexp.MustCompile(`^[0-9a-f]{12}$`)
var id32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
var sha64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Inventory struct {
	Version    int               `json:"version"`
	RegistryID string            `json:"registry_id"`
	Members    []InventoryMember `json:"members"`
}
type InventoryMember struct {
	TokenID          string   `json:"token_id"`
	OwnerRef         string   `json:"owner_ref"`
	Shared           bool     `json:"shared"`
	InstallationRefs []string `json:"installation_refs"`
}

// DecodeInventory approves exact raw bytes. Case aliases, duplicate fields,
// nulls, unknown fields and omitted fields never acquire implicit defaults.
func DecodeInventory(raw []byte, approvedSHA string) (Inventory, error) {
	var inventory Inventory
	if len(raw) == 0 || len(raw) > MaxInventoryBytes {
		return inventory, ErrInvalidInventory
	}
	sum := sha256.Sum256(raw)
	if !sha64.MatchString(approvedSHA) || approvedSHA != hex.EncodeToString(sum[:]) {
		return inventory, ErrInventoryApproval
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := strictValue(dec, 0)
	if err != nil {
		return inventory, ErrInvalidInventory
	}
	if _, err = dec.Token(); err != io.EOF {
		return inventory, ErrInvalidInventory
	}
	obj, ok := value.(map[string]any)
	if !ok || !exactFields(obj, "version", "registry_id", "members") {
		return inventory, ErrInvalidInventory
	}
	members, ok := obj["members"].([]any)
	if !ok || len(members) == 0 || len(members) > MaxCampaignMembers {
		return inventory, ErrInvalidInventory
	}
	for _, m := range members {
		item, ok := m.(map[string]any)
		if !ok || !exactFields(item, "token_id", "owner_ref", "shared", "installation_refs") {
			return inventory, ErrInvalidInventory
		}
	}
	if err = json.Unmarshal(raw, &inventory); err != nil {
		return Inventory{}, ErrInvalidInventory
	}
	if err = validateInventory(inventory); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}
func exactFields(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := m[n]; !ok {
			return false
		}
	}
	return true
}
func strictValue(d *json.Decoder, depth int) (any, error) {
	if depth > 4 {
		return nil, ErrInvalidInventory
	}
	t, e := d.Token()
	if e != nil || t == nil {
		return nil, ErrInvalidInventory
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				name, ok := k.(string)
				if e != nil || !ok {
					return nil, ErrInvalidInventory
				}
				if _, ok = m[name]; ok {
					return nil, ErrInvalidInventory
				}
				v, e := strictValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[name] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, ErrInvalidInventory
			}
			return m, nil
		case '[':
			a := make([]any, 0)
			for d.More() {
				if len(a) > MaxCampaignMembers {
					return nil, ErrInvalidInventory
				}
				v, e := strictValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, ErrInvalidInventory
			}
			return a, nil
		}
		return nil, ErrInvalidInventory
	}
	return t, nil
}
func validateInventory(i Inventory) error {
	if i.Version != 1 || !id32.MatchString(i.RegistryID) || len(i.Members) == 0 || len(i.Members) > MaxCampaignMembers {
		return ErrInvalidInventory
	}
	seen := map[string]bool{}
	for _, m := range i.Members {
		if !id12.MatchString(m.TokenID) || seen[m.TokenID] || (m.OwnerRef != "" && !id32.MatchString(m.OwnerRef)) || m.InstallationRefs == nil || len(m.InstallationRefs) > MaxInstallationRefs {
			return ErrInvalidInventory
		}
		seen[m.TokenID] = true
		refs := map[string]bool{}
		for _, r := range m.InstallationRefs {
			if !id32.MatchString(r) || refs[r] {
				return ErrInvalidInventory
			}
			refs[r] = true
		}
	}
	return nil
}
func validObservationTime(t, now time.Time) bool {
	return !t.IsZero() && t.UnixNano() > 0 && time.Unix(0, t.UnixNano()).Equal(t) && !t.After(now.Add(time.Minute))
}

func (s *Store) RegisterProvisionalInventory(ctx context.Context, raw []byte, approvedSHA string, now time.Time) (bool, error) {
	if len(raw) == 0 || len(raw) > MaxInventoryBytes {
		return false, ErrInvalidInventory
	}
	i, err := DecodeInventory(bytes.Clone(raw), approvedSHA)
	if err != nil {
		return false, err
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	old, e := s.q.WithTx(tx).GetMigrationInventory(ctx)
	if e == nil {
		if old.RegistryID == i.RegistryID && old.ApprovedSha256 == approvedSHA {
			return false, nil
		}
		return false, ErrInventoryImmutable
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	if !validObservationTime(now, time.Now()) {
		return false, ErrInvalidInventory
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO migration_inventory(singleton,registry_id,approved_sha256,baseline_count,registered_at) VALUES(1,?,?,?,?)`, i.RegistryID, approvedSHA, len(i.Members), formatTime(now)); e != nil {
		return false, e
	}
	for _, m := range i.Members {
		refs, _ := json.Marshal(m.InstallationRefs)
		if _, e = tx.ExecContext(ctx, `INSERT INTO migration_members(token_id,membership,owner_ref,shared,installation_refs_json,registered_at) VALUES(?,'baseline',?,?,?,?)`, m.TokenID, m.OwnerRef, m.Shared, string(refs), formatTime(now)); e != nil {
			return false, e
		}
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return true, nil
}

type SourceToken struct {
	TokenID string
	State   string
}
type CampaignSnapshot struct {
	Inventory    *gen.MigrationInventory
	Members      []gen.MigrationMember
	Facts        []gen.MigrationObservationFact
	Observations []gen.ClientMigrationObservation
	Runs         []gen.MigrationObserverRun
	Sources      map[string]string
}

// CampaignSnapshot passively appends unknown extras. It never approves a
// member or changes the sealed baseline. Any failed batch returns no report.
func (s *Store) CampaignSnapshot(ctx context.Context, sources []SourceToken, currentRunID string, now time.Time) (CampaignSnapshot, error) {
	result := CampaignSnapshot{Sources: map[string]string{}}
	if !validObservationTime(now, time.Now()) {
		return result, ErrProjectionUnavailable
	}
	if len(sources) > MaxCampaignMembers {
		return result, ErrProjectionUnavailable
	}
	for _, src := range sources {
		if !id12.MatchString(src.TokenID) {
			return result, ErrProjectionUnavailable
		}
		switch src.State {
		case "enabled", "disabled", "expired", "invalid":
		default:
			return result, ErrProjectionUnavailable
		}
		if _, ok := result.Sources[src.TokenID]; ok {
			return result, ErrProjectionUnavailable
		}
		result.Sources[src.TokenID] = src.State
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	h, e := q.GetMigrationInventory(ctx)
	if e == nil {
		result.Inventory = &h
	} else if !errors.Is(e, sql.ErrNoRows) {
		return result, e
	}
	result.Facts, e = q.ListMigrationFacts(ctx)
	if e != nil || len(result.Facts) > MaxCampaignMembers {
		return result, ErrProjectionUnavailable
	}
	result.Observations, e = q.ListClientMigrationObservations(ctx)
	if e != nil || len(result.Observations) > MaxCampaignMembers {
		return result, ErrProjectionUnavailable
	}
	result.Members, e = q.ListMigrationMembers(ctx)
	if e != nil || len(result.Members) > MaxCampaignMembers {
		return result, ErrProjectionUnavailable
	}
	result.Runs, e = q.ListMigrationObserverRuns(ctx)
	if e != nil || len(result.Runs) > MaxObserverRuns {
		return result, ErrProjectionUnavailable
	}
	all := map[string]bool{}
	for _, m := range result.Members {
		all[m.TokenID] = true
	}
	for _, f := range result.Facts {
		all[f.TokenID] = true
	}
	for id := range result.Sources {
		all[id] = true
	}
	if len(all) > MaxCampaignMembers {
		return result, ErrProjectionUnavailable
	}
	if result.Inventory != nil {
		existing := map[string]bool{}
		for _, m := range result.Members {
			existing[m.TokenID] = true
		}
		ids := make([]string, 0, len(all))
		for id := range all {
			if !existing[id] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			if _, e = tx.ExecContext(ctx, `INSERT INTO migration_members(token_id,membership,owner_ref,shared,installation_refs_json,registered_at) VALUES(?,'extra','',0,'[]',?)`, id, formatTime(now)); e != nil {
				return result, e
			}
		}
		result.Members, e = q.ListMigrationMembers(ctx)
		if e != nil {
			return result, e
		}
	} else {
		if len(result.Members) != 0 {
			return result, ErrProjectionUnavailable
		}
		ids := make([]string, 0, len(all))
		for id := range all {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			result.Members = append(result.Members, gen.MigrationMember{TokenID: id, Membership: "unregistered", InstallationRefsJson: "[]"})
		}
	}
	for _, f := range result.Facts {
		if !id12.MatchString(f.TokenID) || !validObservationTime(time.Unix(0, f.FirstSeenUnixNs), now) || !validObservationTime(time.Unix(0, f.LastSeenUnixNs), now) {
			return result, ErrProjectionUnavailable
		}
	}
	if e = tx.Commit(); e != nil {
		return result, e
	}
	return result, nil
}

func (s *Store) StartObserverRun(ctx context.Context, now time.Time) (string, error) {
	if !validObservationTime(now, time.Now()) {
		return "", ErrProjectionUnavailable
	}
	var buf [16]byte
	if _, e := rand.Read(buf[:]); e != nil {
		return "", e
	}
	id := hex.EncodeToString(buf[:])
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	runs, e := s.q.WithTx(tx).ListMigrationObserverRuns(ctx)
	if e != nil || len(runs) >= MaxObserverRuns {
		return "", ErrProjectionUnavailable
	}
	// A prior still-open run cannot be certified clean after this restart.
	if _, e = tx.ExecContext(ctx, `UPDATE migration_observer_runs SET state='failed',reason='unclean_shutdown',ended_at=? WHERE state='open'`, formatTime(now)); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO migration_observer_runs(run_id,started_at,state,reason) VALUES(?,?,'open','none')`, id, formatTime(now)); e != nil {
		return "", e
	}
	if e = tx.Commit(); e != nil {
		return "", e
	}
	return id, nil
}
func (s *Store) FailObserverRun(ctx context.Context, id, reason string) error {
	switch reason {
	case "write_failed", "sink_replaced", "observer_closed":
	default:
		return ErrProjectionUnavailable
	}
	_, e := s.db.ExecContext(ctx, `UPDATE migration_observer_runs SET state='failed',reason=?,ended_at=? WHERE run_id=? AND state!='failed'`, reason, formatTime(time.Now()), id)
	return e
}
func (s *Store) CloseObserverRun(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE migration_observer_runs SET state='closed',reason='none',ended_at=? WHERE run_id=? AND state='open'`, formatTime(time.Now()), id)
	return e
}

func (s *Store) initializeCampaignHistory(ctx context.Context) error {
	rows, e := s.q.ListClientMigrationObservations(ctx)
	if e != nil || len(rows) > MaxCampaignMembers {
		return ErrProjectionUnavailable
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, r := range rows {
		if !id12.MatchString(r.TokenID) || !validObservationTime(time.Unix(0, r.FirstSeenUnixNs), time.Now()) || !validObservationTime(time.Unix(0, r.LastSeenUnixNs), time.Now()) {
			return ErrProjectionUnavailable
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO migration_observation_facts VALUES(?,?,?,?,?,?,?,?,?)`, r.TokenID, r.FirstSeenUnixNs, r.LastSeenUnixNs, r.SecureBootstrapCount, r.LegacyCount, r.UnknownCount, r.CompatIngressCount, 0, 0); e != nil {
			return e
		}
		if r.MigrationClass == "secure_bootstrap" && (r.Ingress != "trusted_proxy" || r.KeyMode != "client_generated" || r.PrivateKeyReturned != 0 || !auth.ValidClientMetadata(r.ClientProduct, r.ClientVersion, int(r.ProtocolVersion))) {
			result, e := tx.ExecContext(ctx, `INSERT INTO migration_import_flags(token_id,reason) VALUES(?,'invalid_metadata') ON CONFLICT(token_id) DO NOTHING`, r.TokenID)
			if e != nil {
				return e
			}
			inserted, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if inserted == 1 {
				if _, e = tx.ExecContext(ctx, `UPDATE migration_observation_facts SET unknown_count=unknown_count+1 WHERE token_id=?`, r.TokenID); e != nil {
					return e
				}
			}
		}
	}
	return tx.Commit()
}
func recordMigrationFact(ctx context.Context, tx *sql.Tx, event auth.AuditEvent, ns int64) error {
	o := event.MigrationObservation
	if o == nil {
		return nil
	}
	secure, legacy, unknown, compat, denied, failed := 0, 0, 0, 0, 0, 0
	if o.Ingress == "compat" {
		compat = 1
	}
	switch event.Result {
	case "ok":
		switch o.MigrationClass {
		case "secure_bootstrap":
			secure = 1
		case "legacy":
			legacy = 1
		default:
			unknown = 1
		}
	case "denied":
		denied = 1
	case "error":
		failed = 1
	default:
		return ErrProjectionUnavailable
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO migration_observation_facts VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(token_id) DO UPDATE SET first_seen_unix_ns=MIN(first_seen_unix_ns,excluded.first_seen_unix_ns),last_seen_unix_ns=MAX(last_seen_unix_ns,excluded.last_seen_unix_ns),secure_bootstrap_count=secure_bootstrap_count+excluded.secure_bootstrap_count,legacy_count=legacy_count+excluded.legacy_count,unknown_count=unknown_count+excluded.unknown_count,compat_ingress_count=compat_ingress_count+excluded.compat_ingress_count,denied_count=denied_count+excluded.denied_count,error_count=error_count+excluded.error_count`, o.TokenID, ns, ns, secure, legacy, unknown, compat, denied, failed)
	return e
}
