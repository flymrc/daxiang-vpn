package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"zongheng-vpn/hub/internal/auth"
)

func campaignRaw(i Inventory) ([]byte, string) {
	raw, _ := json.Marshal(i)
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}
func campaignInventory() Inventory {
	return Inventory{Version: 1, RegistryID: strings.Repeat("1", 32), Members: []InventoryMember{{TokenID: auth.TokenID("owned-one"), OwnerRef: "", InstallationRefs: []string{}}}}
}
func TestInventoryStrictFiniteRawApproval(t *testing.T) {
	raw, digest := campaignRaw(campaignInventory())
	if _, e := DecodeInventory(raw, digest); e != nil {
		t.Fatal(e)
	}
	changed := append([]byte{}, raw...)
	changed = append(changed, ' ')
	if _, e := DecodeInventory(changed, digest); e != ErrInventoryApproval {
		t.Fatal("approval did not bind raw bytes")
	}
	valid := string(raw)
	bad := []string{strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(valid, `"version":1`, `"Version":1`, 1), strings.Replace(valid, `"version":1`, `"version":null`, 1), strings.Replace(valid, `"version":1`, `"version":1.0`, 1), strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"version":1,`, "", 1), strings.Replace(valid, `"shared":false`, `"shared":null`, 1), strings.Replace(valid, `"installation_refs":[]`, `"installation_refs":null`, 1), strings.Replace(valid, `"owner_ref":""`, `"owner_ref":"raw-secret-label"`, 1), strings.Replace(valid, `"version":1`, `"version":1,"t0":"2026-10-07T00:00:00Z"`, 1), valid + "{}", `null`, strings.Replace(valid, `"members":[`, `"members":[null,`, 1)}
	for index, s := range bad {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			sum := sha256.Sum256([]byte(s))
			if _, e := DecodeInventory([]byte(s), hex.EncodeToString(sum[:])); e == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	i := campaignInventory()
	i.Members = append(i.Members, i.Members[0])
	raw, digest = campaignRaw(i)
	if _, e := DecodeInventory(raw, digest); e == nil {
		t.Fatal("duplicate member accepted")
	}
	i = campaignInventory()
	i.Members[0].InstallationRefs = make([]string, 17)
	for j := range i.Members[0].InstallationRefs {
		i.Members[0].InstallationRefs[j] = fmt.Sprintf("%032x", j)
	}
	raw, digest = campaignRaw(i)
	if _, e := DecodeInventory(raw, digest); e == nil {
		t.Fatal("overbudget refs accepted")
	}
	over := make([]byte, MaxInventoryBytes+1)
	if _, e := DecodeInventory(over, strings.Repeat("0", 64)); e == nil {
		t.Fatal("overbudget bytes accepted")
	}
}
func TestInventoryStoreApprovalAtomicAppendOnlyAndTimeBounds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	i := campaignInventory()
	raw, digest := campaignRaw(i)
	i.Members[0].TokenID = auth.TokenID("swapped")
	wrong, _ := campaignRaw(i)
	if ok, e := s.RegisterProvisionalInventory(ctx, wrong, digest, time.Now()); e == nil || ok {
		t.Fatal("SHA paired with altered members")
	}
	if countRows(t, s, "migration_inventory") != 0 || countRows(t, s, "migration_members") != 0 {
		t.Fatal("rejected approval mutated DB")
	}
	for _, future := range []time.Time{time.Now().Add(2 * time.Minute), time.Date(2400, 1, 1, 0, 0, 0, 0, time.UTC), time.Unix(0, 0)} {
		if ok, e := s.RegisterProvisionalInventory(ctx, raw, digest, future); e == nil || ok {
			t.Fatal("invalid registration time accepted")
		}
	}
	if ok, e := s.RegisterProvisionalInventory(ctx, raw, digest, time.Now()); e != nil || !ok {
		t.Fatal(e)
	}
	for _, query := range []string{`DELETE FROM migration_inventory`, `UPDATE migration_inventory SET baseline_count=2`, `DELETE FROM migration_members`, `UPDATE migration_members SET membership='extra'`} {
		if _, e := s.db.Exec(query); e == nil {
			t.Fatal("immutable approval changed")
		}
	}
	if ok, e := s.RegisterProvisionalInventory(ctx, raw, digest, time.Now().Add(time.Hour)); e != nil || ok {
		t.Fatal("idempotent SHA rewrote registration")
	}
	_, other := campaignRaw(i)
	if ok, e := s.RegisterProvisionalInventory(ctx, wrong, other, time.Now()); e != ErrInventoryImmutable || ok {
		t.Fatal("inventory replaced")
	}
}
func TestCampaignHistoricalCountsHaveSafeIntegerAndNoErase(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Second)
	o := &auth.MigrationObservation{TokenID: auth.TokenID("owned-history"), OccurredAt: now, ClientProduct: "cli", ClientVersion: "1.2.3", ProtocolVersion: 2, Ingress: "trusted_proxy", KeyMode: "client_generated", MigrationClass: "secure_bootstrap"}
	event := auth.AuditEvent{OccurredAt: now, EventType: "client.bootstrap", Result: "ok", MigrationObservation: o}
	if e := s.InsertAudit(ctx, event); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE migration_observation_facts SET secure_bootstrap_count=? WHERE token_id=?`, MaxSafeCounter, o.TokenID); e != nil {
		t.Fatal(e)
	}
	before := countRows(t, s, "audit_events")
	if e := s.InsertAudit(ctx, event); e == nil {
		t.Fatal("counter crossed JS safe integer")
	}
	if countRows(t, s, "audit_events") != before {
		t.Fatal("overflow transaction partial audit")
	}
	if _, e := s.db.Exec(`DELETE FROM migration_observation_facts`); e == nil {
		t.Fatal("history erased")
	}
	if _, e := s.db.Exec(`UPDATE migration_observation_facts SET secure_bootstrap_count=0`); e == nil {
		t.Fatal("history decreased")
	}
}
func TestCampaignCapacityRefusesWholeBatchAndKeepsOldMembers(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	i := campaignInventory()
	raw, digest := campaignRaw(i)
	if _, e := s.RegisterProvisionalInventory(ctx, raw, digest, time.Now()); e != nil {
		t.Fatal(e)
	}
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for index := 1; index < MaxCampaignMembers; index++ {
		if _, e := tx.Exec(`INSERT INTO migration_members VALUES(?,'extra','',0,'[]',?)`, fmt.Sprintf("%012x", index), formatTime(time.Now())); e != nil {
			t.Fatal(e)
		}
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CampaignSnapshot(ctx, []SourceToken{{TokenID: "ffffffffffff", State: "enabled"}}, "", time.Now()); e == nil {
		t.Fatal("capacity silently truncated")
	}
	if countRows(t, s, "migration_members") != MaxCampaignMembers {
		t.Fatal("capacity replaced old member")
	}
}
func TestCampaignUnknownMetadataDoesNotReturnCallerStrings(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	sentinel := "SECRET_SENTINEL_METADATA"
	event := auth.AuditEvent{OccurredAt: now, EventType: "client.bootstrap", Result: "ok", MigrationObservation: &auth.MigrationObservation{TokenID: auth.TokenID("owned-privacy"), OccurredAt: now, ClientProduct: sentinel, ClientVersion: sentinel, ProtocolVersion: -123, Ingress: "trusted_proxy", KeyMode: "client_generated", MigrationClass: "secure_bootstrap"}}
	if e := s.InsertAudit(context.Background(), event); e != nil {
		t.Fatal(e)
	}
	rows, e := s.ListClientMigrationObservations(context.Background())
	if e != nil || len(rows) != 1 {
		t.Fatal(e)
	}
	if rows[0].ClientProduct != "" || rows[0].ClientVersion != "" || rows[0].ProtocolVersion != 0 || rows[0].MigrationClass != "unknown" {
		t.Fatal("untrusted metadata was preserved")
	}
	facts, e := s.q.ListMigrationFacts(context.Background())
	if e != nil || len(facts) != 1 || facts[0].UnknownCount != 1 || facts[0].SecureBootstrapCount != 0 {
		t.Fatal("invalid metadata counted secure")
	}
}
