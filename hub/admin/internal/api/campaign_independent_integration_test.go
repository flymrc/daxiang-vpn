//go:build integration

package api

// These fixtures exercise the normal SQLite/HTTP boundary and a killed native
// test process. They never load production tokens, call WG/SSH, or approve a
// release. A declared installation is deliberately not verified evidence.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dbstore "zongheng-vpn/hub/admin/internal/db"
	dbgen "zongheng-vpn/hub/admin/internal/db/generated"
	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
)

func independentCampaignServer(t *testing.T, path string, tokens *auth.TokenStore) (*Server, *auth.Server) {
	t.Helper()
	client := auth.NewServer(tokens)
	s, err := NewServer(Config{DBPath: path, MaintenanceInterval: -1, AdminUsername: "owned-campaign-admin", AdminPasswordPHC: "owned-session-fixture", SessionTTL: time.Hour}, tokens, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, client
}

func independentCampaignInventory(t *testing.T, tokens ...string) (dbstore.Inventory, []byte, string) {
	t.Helper()
	inventory := dbstore.Inventory{Version: 1, RegistryID: strings.Repeat("d", 32), Members: make([]dbstore.InventoryMember, 0, len(tokens))}
	for i, token := range tokens {
		member := dbstore.InventoryMember{TokenID: auth.TokenID(token), OwnerRef: strings.Repeat("a", 32), InstallationRefs: []string{}}
		if i == 0 {
			member.Shared = true
			member.InstallationRefs = []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
		}
		if i == 1 {
			member.InstallationRefs = []string{strings.Repeat("3", 32)}
		}
		inventory.Members = append(inventory.Members, member)
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	approved := hex.EncodeToString(digest[:])
	decoded, err := dbstore.DecodeInventory(raw, approved)
	if err != nil {
		t.Fatal(err)
	}
	return decoded, raw, approved
}

func independentCampaignRegister(t *testing.T, s *Server, tokens ...string) string {
	t.Helper()
	_, raw, digest := independentCampaignInventory(t, tokens...)
	created, err := s.store.RegisterProvisionalInventory(context.Background(), raw, digest, time.Now().UTC())
	if err != nil || !created {
		t.Fatalf("initial registration: created=%v error=%v", created, err)
	}
	return digest
}

func independentCampaignSQL(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	c, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}

func independentCampaignReadinessRequest(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	const cookie = "owned-campaign-read-only-session"
	now := time.Now().UTC()
	// Seed a synthetic already-authenticated session; login/password policy is
	// outside this test. ServeHTTP still performs its real cookie/DB checks.
	err := s.store.Queries().CreateAdminSession(context.Background(), dbgen.CreateAdminSessionParams{ID: strings.Repeat("9", 32), Username: "owned-campaign-admin", TokenHash: hashSecret(cookie), CsrfToken: "owned-csrf", SourceIp: "127.0.0.1", UserAgent: "owned-fixture", CreatedAt: formatTime(now), LastSeenAt: formatTime(now), ExpiresAt: formatTime(now.Add(time.Hour))})
	if err != nil && !strings.Contains(err.Error(), "UNIQUE constraint") {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/admin/api/migration/readiness", nil)
	r.RemoteAddr = "127.0.0.1:24123"
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func independentCampaignReadiness(t *testing.T, s *Server) generated.MigrationReadinessResponse {
	t.Helper()
	w := independentCampaignReadinessRequest(t, s)
	if w.Code != http.StatusOK {
		t.Fatalf("readiness HTTP status=%d body=%s", w.Code, w.Body.String())
	}
	var report generated.MigrationReadinessResponse
	if json.Unmarshal(w.Body.Bytes(), &report) != nil || report.ContractVersion != 2 || report.Ready || report.CampaignConfigured || report.T0 != nil {
		t.Fatal("provisional report claimed campaign activation")
	}
	for _, permanent := range []string{"campaign_not_configured", "e2e_evidence_unavailable", "approved_release_unavailable", "quiet_window_unavailable"} {
		if !independentCampaignBlocker(report.Blockers, permanent) {
			t.Fatalf("missing fixed blocker: %s", permanent)
		}
	}
	return report
}

func independentCampaignBlocker(values []generated.MigrationBlocker, want string) bool {
	for _, value := range values {
		if string(value) == want {
			return true
		}
	}
	return false
}

func independentCampaignMember(t *testing.T, report generated.MigrationReadinessResponse, token string) generated.MigrationClientStatus {
	t.Helper()
	for _, member := range report.Clients {
		if member.TokenId == auth.TokenID(token) {
			return member
		}
	}
	t.Fatalf("historical member disappeared: %s", auth.TokenID(token))
	return generated.MigrationClientStatus{}
}

func independentCampaignObservation(t *testing.T, s *Server, token, class string, at time.Time) {
	t.Helper()
	observation := &auth.MigrationObservation{TokenID: auth.TokenID(token), OccurredAt: at, ClientProduct: "cli", ClientVersion: "2.0.0", ProtocolVersion: 2, Ingress: "trusted_proxy", KeyMode: "client_generated", MigrationClass: class}
	if class == "legacy" {
		observation.Ingress = "compat"
		observation.KeyMode = "server_legacy"
		observation.PrivateKeyReturned = true
	}
	if class == "unknown" {
		observation.ClientVersion = "dev"
	}
	err := s.store.InsertAudit(context.Background(), auth.AuditEvent{OccurredAt: at, Actor: "owned-fixture", EventType: "client.bootstrap", Target: "token:owned-mask", DetailJSON: "{}", Result: "ok", MigrationObservation: observation})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCampaignIndependentFixedInventoryAndLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-campaign.db")
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-shared": {Enabled: true}, "owned-single": {Enabled: true}, "owned-unknown": {Enabled: true}}}
	s, _ := independentCampaignServer(t, path, tokens)
	digest := independentCampaignRegister(t, s, "owned-shared", "owned-single", "owned-unknown")
	before := independentCampaignReadiness(t, s)
	if !before.InventoryRegistered || before.BaselineMemberCount != 3 || before.MemberCount != 3 || before.ExtraMemberCount != 0 || before.ValidTokenCount != 3 || before.ApprovedInventorySha256 != digest {
		t.Fatal("invalid fixed baseline fixture")
	}
	// Reconstruct the current YAML authority rather than marking campaign rows.
	s.tokens = &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-single": {Enabled: false}, "owned-unknown": {Enabled: true, ExpiresAt: "2000-01-01"}, "owned-new": {Enabled: true}, "owned-bad-expiry": {Enabled: true, ExpiresAt: "not-a-date"}}}
	after := independentCampaignReadiness(t, s)
	if after.BaselineMemberCount != 3 || after.MemberCount != 5 || after.ExtraMemberCount != 2 || after.ValidTokenCount != 1 || after.ApprovedInventorySha256 != digest || after.RegistryId != before.RegistryId {
		t.Fatal("source change reset initial inventory or lost objects")
	}
	for _, tc := range []struct{ token, state, blocker string }{{"owned-shared", "missing", "source_missing"}, {"owned-single", "disabled", "source_disabled"}, {"owned-unknown", "expired", "source_expired"}, {"owned-bad-expiry", "invalid", "source_invalid"}} {
		member := independentCampaignMember(t, after, tc.token)
		if string(member.SourceState) != tc.state || !independentCampaignBlocker(member.Blockers, tc.blocker) || !independentCampaignBlocker(member.Blockers, "disposition_required") {
			t.Fatal("source lifecycle became an implicit disposition")
		}
	}
	extra := independentCampaignMember(t, after, "owned-new")
	if string(extra.Membership) != "extra" || string(extra.LineageState) != "unknown" || !independentCampaignBlocker(extra.Blockers, "extra_unregistered") {
		t.Fatal("new source silently became registered/verified")
	}
	// An extra also stays in history when it subsequently disappears.
	delete(s.tokens.Tokens, "owned-new")
	final := independentCampaignReadiness(t, s)
	if final.BaselineMemberCount != 3 || final.MemberCount != 5 || string(independentCampaignMember(t, final, "owned-new").SourceState) != "missing" {
		t.Fatal("extra member vanished on second source change")
	}
}

func TestCampaignIndependentHistoryAndDeclaredLineageStayBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-history.db")
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-shared": {Enabled: true}, "owned-single": {Enabled: true}, "owned-unknown": {Enabled: true}}}
	s, _ := independentCampaignServer(t, path, tokens)
	independentCampaignRegister(t, s, "owned-shared", "owned-single", "owned-unknown")
	now := time.Now().UTC()
	independentCampaignObservation(t, s, "owned-shared", "legacy", now.Add(-3*time.Minute))
	independentCampaignObservation(t, s, "owned-shared", "secure_bootstrap", now.Add(-time.Minute))
	independentCampaignObservation(t, s, "owned-single", "secure_bootstrap", now.Add(-time.Minute))
	independentCampaignObservation(t, s, "owned-single", "unknown", now.Add(-time.Minute))
	independentCampaignObservation(t, s, "owned-single", "secure_bootstrap", now.Add(-30*time.Second))
	independentCampaignObservation(t, s, "owned-single", "legacy", now.Add(-5*time.Minute))
	report := independentCampaignReadiness(t, s)
	shared := independentCampaignMember(t, report, "owned-shared")
	if string(shared.MigrationClass) != "secure_bootstrap" || shared.History.LegacyCount != 1 || shared.History.CompatIngressCount != 1 || string(shared.LineageState) != "shared_unverified" || !independentCampaignBlocker(shared.Blockers, "shared_lineage_unverified") || !independentCampaignBlocker(shared.Blockers, "historical_legacy") || !independentCampaignBlocker(shared.Blockers, "historical_compat") {
		t.Fatal("latest secure event erased shared/history blockers")
	}
	single := independentCampaignMember(t, report, "owned-single")
	if string(single.LineageState) != "declared_unverified" || !independentCampaignBlocker(single.Blockers, "lineage_unverified") || single.History.UnknownCount != 1 || single.History.LegacyCount != 1 || !independentCampaignBlocker(single.Blockers, "historical_unknown") {
		t.Fatal("installation reference or event ordering became confirmed evidence")
	}
	unknown := independentCampaignMember(t, report, "owned-unknown")
	if string(unknown.LineageState) != "unknown" || !independentCampaignBlocker(unknown.Blockers, "lineage_unknown") {
		t.Fatal("unobserved installation implicitly confirmed")
	}
	// Generic audit retention must not remove facts used by the fixed inventory.
	independentCampaignSQL(t, path, "DELETE FROM audit_events")
	retained := independentCampaignMember(t, independentCampaignReadiness(t, s), "owned-shared")
	if retained.History.LegacyCount != 1 || retained.History.SecureBootstrapCount != 1 {
		t.Fatal("audit pruning erased retained security history")
	}
}

func TestCampaignIndependentKnownTokenHTTPDenialPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-denied.db")
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-denied": {Enabled: true}}}
	s, client := independentCampaignServer(t, path, tokens)
	independentCampaignRegister(t, s, "owned-denied")
	independentCampaignObservation(t, s, "owned-denied", "secure_bootstrap", time.Now().Add(-time.Minute))
	body, _ := json.Marshal(map[string]any{"token": "owned-denied", "wireguard_public_key": "malformed-public-key", "client_product": "cli", "client_version": "2.0.0", "protocol_version": 2})
	r := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", bytes.NewReader(body))
	r.RemoteAddr = "198.51.100.20:41234"
	w := httptest.NewRecorder()
	client.BootstrapHandler(auth.ClientIngressCompat)(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("owned denial status=%d", w.Code)
	}
	member := independentCampaignMember(t, independentCampaignReadiness(t, s), "owned-denied")
	if member.History.DeniedCount != 1 || member.History.CompatIngressCount < 1 || !independentCampaignBlocker(member.Blockers, "historical_denied") || !independentCampaignBlocker(member.Blockers, "historical_compat") {
		t.Fatal("actual HTTP denial absent from retained negative facts")
	}
}

func TestCampaignIndependentPersistedOldMetadataIsReclassified(t *testing.T) {
	for _, tc := range []struct {
		name, version, ingress, keyMode string
		privateFlag                     int
		existingFacts                   bool
	}{
		{"not-a-semver", "not-a-semver", "trusted_proxy", "client_generated", 0, false},
		{"unsigned-build", "unsigned-build", "trusted_proxy", "client_generated", 0, true},
		{"DEV", "DEV", "trusted_proxy", "client_generated", 0, true},
		{"raw_old_fields", "2.0.0", "OWNED_SECRET_INGRESS_CANARY", "OWNED_SECRET_KEYMODE_CANARY", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned-old-metadata.db")
			store, err := dbstore.OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			_, raw, digest := independentCampaignInventory(t, "owned-old-client")
			if created, err := store.RegisterProvisionalInventory(context.Background(), raw, digest, time.Now()); err != nil || !created {
				t.Fatal("could not register owned old metadata fixture")
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			seen := time.Now().Add(-time.Minute).UTC()
			// Exact native row allowed by the old schema/classifier. Deliberately
			// bypass the NEW InsertAudit validation to reproduce an existing DB.
			independentCampaignSQL(t, path, `INSERT INTO client_migration_observations (
			 token_id,first_seen_unix_ns,last_seen_unix_ns,last_seen_at,
			 client_product,client_version,protocol_version,ingress,key_mode,
			 private_key_returned,migration_class,last_secure_bootstrap_unix_ns,secure_bootstrap_count
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, auth.TokenID("owned-old-client"), seen.UnixNano(), seen.UnixNano(), formatTime(seen), "cli", tc.version, 2, tc.ingress, tc.keyMode, tc.privateFlag, "secure_bootstrap", seen.UnixNano(), 1)
			if tc.existingFacts {
				// The earlier implementation may already have backfilled only the
				// permissive secure counter. Reopening must repair its missing
				// conservative negative without deleting or reducing any facts.
				independentCampaignSQL(t, path, "INSERT INTO migration_observation_facts VALUES(?,?,?,?,?,?,?,?,?)", auth.TokenID("owned-old-client"), seen.UnixNano(), seen.UnixNano(), 1, 0, 0, 0, 0, 0)
			}
			tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-old-client": {Enabled: true}}}
			s, _ := independentCampaignServer(t, path, tokens)
			report := independentCampaignReadiness(t, s)
			member := independentCampaignMember(t, report, "owned-old-client")
			if string(member.MigrationClass) != "unknown" || !independentCampaignBlocker(member.Blockers, "non_secure_bootstrap_tokens") || !independentCampaignBlocker(report.Blockers, "non_secure_bootstrap_tokens") {
				t.Fatal("old permissive label survived as current secure evidence")
			}
			if member.History.SecureBootstrapCount != 1 || report.ApprovedInventorySha256 != digest || report.BaselineMemberCount != 1 {
				t.Fatal("reclassification erased historical accounting or fixed inventory")
			}
			if tc.name == "raw_old_fields" {
				encoded, _ := json.Marshal(report)
				if member.Ingress != "" || member.KeyMode != "" || !member.PrivateKeyReturned || bytes.Contains(encoded, []byte("OWNED_SECRET_")) {
					t.Fatal("raw old fields leaked or an uncertain private flag became clean")
				}
			}
			// Reopen while the old invalid latest row still exists. Checking
			// only after a later good row would miss repeated import inflation.
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, _ = independentCampaignServer(t, path, tokens)
			stillInvalid := independentCampaignMember(t, independentCampaignReadiness(t, s), "owned-old-client")
			if member.History.UnknownCount < 1 || stillInvalid.History.UnknownCount != member.History.UnknownCount {
				t.Fatal("invalid metadata import was lost or counted again on reopen")
			}
			independentCampaignObservation(t, s, "owned-old-client", "secure_bootstrap", time.Now().UTC())
			later := independentCampaignReadiness(t, s)
			laterMember := independentCampaignMember(t, later, "owned-old-client")
			if string(laterMember.MigrationClass) != "secure_bootstrap" || laterMember.History.UnknownCount < 1 || !independentCampaignBlocker(laterMember.Blockers, "historical_unknown") || !independentCampaignBlocker(later.Blockers, "historical_unknown") {
				t.Fatal("later secure observation washed the old invalid metadata negative")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, _ := independentCampaignServer(t, path, tokens)
			retained := independentCampaignMember(t, independentCampaignReadiness(t, reopened), "owned-old-client")
			if retained.History.UnknownCount != laterMember.History.UnknownCount || !independentCampaignBlocker(retained.Blockers, "historical_unknown") {
				t.Fatal("restart lost or inflated the old metadata negative")
			}
		})
	}
}

func TestCampaignIndependentRegistrationApprovalAtomicityAndNoReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-registration.db")
	store, err := dbstore.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inventory, raw, digest := independentCampaignInventory(t, "owned-one", "owned-two", "owned-three")
	if _, err = dbstore.DecodeInventory(raw, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong approval digest accepted")
	}
	for _, field := range []string{`"t0":"2026-10-07T00:00:00Z"`, `"ready":true`, `"compliant":true`, `"approved_release_sha256":"` + strings.Repeat("e", 64) + `"`} {
		claimed := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(","+field+"}")...)
		sum := sha256.Sum256(claimed)
		if _, err = dbstore.DecodeInventory(claimed, hex.EncodeToString(sum[:])); err == nil {
			t.Fatal("inventory acquired an authority/evidence setter")
		}
	}
	// Decode is not an authorization capability for a mutable Go value. The
	// actual persistence boundary must bind these exact bytes to the approval.
	mutated, err := dbstore.DecodeInventory(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	mutated.Members[0].OwnerRef = strings.Repeat("f", 32)
	mutatedRaw, _ := json.Marshal(mutated)
	if created, err := store.RegisterProvisionalInventory(context.Background(), mutatedRaw, digest, time.Now()); err == nil || created {
		t.Fatal("mutated approved payload entered persistent inventory")
	}
	if _, err = store.Queries().GetMigrationInventory(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("approval mismatch left an inventory header")
	}
	unapprovedMembers, err := store.Queries().ListMigrationMembers(context.Background())
	if err != nil || len(unapprovedMembers) != 0 {
		t.Fatal("approval mismatch left member rows")
	}
	// Abort the real second-member insert. Neither header nor first member
	// may survive this failed transaction.
	independentCampaignSQL(t, path, "CREATE TRIGGER owned_registration_failure BEFORE INSERT ON migration_members WHEN NEW.token_id='"+auth.TokenID("owned-two")+"' BEGIN SELECT RAISE(ABORT,'owned registration failure'); END")
	if created, err := store.RegisterProvisionalInventory(context.Background(), raw, digest, time.Now()); err == nil || created {
		t.Fatal("partial inventory registration reported success")
	}
	if _, err = store.Queries().GetMigrationInventory(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed transaction retained inventory header")
	}
	members, err := store.Queries().ListMigrationMembers(context.Background())
	if err != nil || len(members) != 0 {
		t.Fatal("failed transaction retained partial members")
	}
	independentCampaignSQL(t, path, "DROP TRIGGER owned_registration_failure")
	if created, err := store.RegisterProvisionalInventory(context.Background(), raw, digest, time.Now()); err != nil || !created {
		t.Fatal("valid retry did not register inventory")
	}
	initial, err := store.Queries().GetMigrationInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if created, err := store.RegisterProvisionalInventory(context.Background(), raw, digest, time.Now().Add(time.Hour)); err != nil || created {
		t.Fatal("same approval was not idempotent")
	}
	replayed, err := store.Queries().GetMigrationInventory(context.Background())
	if err != nil || replayed != initial {
		t.Fatal("idempotent replay reset registration time/baseline")
	}
	changed := inventory
	changed.RegistryID = strings.Repeat("e", 32)
	changedRaw, _ := json.Marshal(changed)
	changedSum := sha256.Sum256(changedRaw)
	if created, err := store.RegisterProvisionalInventory(context.Background(), changedRaw, hex.EncodeToString(changedSum[:]), time.Now()); err == nil || created {
		t.Fatal("new approval replaced sealed inventory")
	}
	retained, err := store.Queries().GetMigrationInventory(context.Background())
	if err != nil || retained != initial {
		t.Fatal("rejected replacement changed original inventory")
	}
}

func TestCampaignIndependentCurrentSourceAliasRemainsBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-alias.db")
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-alias": {Enabled: true}, " owned-alias ": {Enabled: true}}}
	s, _ := independentCampaignServer(t, path, tokens)
	digest := independentCampaignRegister(t, s, "owned-alias")
	w := independentCampaignReadinessRequest(t, s)
	var rejection map[string]any
	if w.Code != http.StatusServiceUnavailable || json.Unmarshal(w.Body.Bytes(), &rejection) != nil || rejection["error"] != "projection_unavailable" || strings.Contains(w.Body.String(), "owned-alias") {
		t.Fatal("current source alias merged as valid authority")
	}
	header, err := s.store.Queries().GetMigrationInventory(context.Background())
	members, memberErr := s.store.Queries().ListMigrationMembers(context.Background())
	if err != nil || memberErr != nil || header.BaselineCount != 1 || header.ApprovedSha256 != digest || len(members) != 1 {
		t.Fatal("source ambiguity discarded immutable baseline")
	}
}

func TestCampaignIndependentWriteFailureCannotBeWashedByRestart(t *testing.T) {
	t.Setenv("ZHHUB_ANDROID_CARRIER_CACHE_TTL_SECONDS", "0")
	path := filepath.Join(t.TempDir(), "owned-gap.db")
	tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-gap": {Enabled: true, Egress: auth.Egress{Name: "owned-local"}}}}
	s, client := independentCampaignServer(t, path, tokens)
	digest := independentCampaignRegister(t, s, "owned-gap")
	post := func() {
		r := httptest.NewRequest(http.MethodPost, "/api/client/bootstrap", strings.NewReader(`{"token":"owned-gap"}`))
		r.RemoteAddr = "198.51.100.21:41234"
		w := httptest.NewRecorder()
		client.BootstrapHandler(auth.ClientIngressCompat)(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("owned observe-only legacy status=%d", w.Code)
		}
	}
	post()
	before := independentCampaignMember(t, independentCampaignReadiness(t, s), "owned-gap")
	independentCampaignSQL(t, path, "CREATE TRIGGER owned_observation_failure BEFORE UPDATE ON client_migration_observations BEGIN SELECT RAISE(ABORT,'owned observation failure'); END")
	// Poison writes fail as well: the durable pre-arm is the surviving proof.
	independentCampaignSQL(t, path, "CREATE TRIGGER owned_poison_failure BEFORE UPDATE ON migration_observer_runs BEGIN SELECT RAISE(ABORT,'owned poison failure'); END")
	post()
	during := independentCampaignReadiness(t, s)
	if during.ObservationWriteHealthy || during.Observer.Healthy {
		t.Fatal("actual SQLite write abort not reflected as observer failure")
	}
	_ = s.Close()
	independentCampaignSQL(t, path, "DROP TRIGGER owned_observation_failure")
	independentCampaignSQL(t, path, "DROP TRIGGER owned_poison_failure")
	next, _ := independentCampaignServer(t, path, tokens)
	after := independentCampaignReadiness(t, next)
	if after.ObservationWriteHealthy || after.Observer.Healthy || after.Observer.GapCount < 1 || !independentCampaignBlocker(after.Blockers, "observer_gap") {
		t.Fatal("restart washed unrecorded gap into healthy observation")
	}
	if after.BaselineMemberCount != 1 || after.ApprovedInventorySha256 != digest || independentCampaignMember(t, after, "owned-gap").History.LegacyCount != before.History.LegacyCount {
		t.Fatal("failed transaction/restart changed inventory or fabricated observations")
	}
}

func TestCampaignIndependentNativeProcessHelper(t *testing.T) {
	mode := os.Getenv("ZHVPN_TEST_CAMPAIGN_INDEPENDENT_MODE")
	if mode == "" {
		return
	}
	path := os.Getenv("ZHVPN_TEST_CAMPAIGN_INDEPENDENT_DB")
	if mode == "register" {
		raw, err := os.ReadFile(os.Getenv("ZHVPN_TEST_CAMPAIGN_INDEPENDENT_INVENTORY"))
		if err != nil {
			os.Exit(91)
		}
		_, err = dbstore.DecodeInventory(raw, os.Getenv("ZHVPN_TEST_CAMPAIGN_INDEPENDENT_SHA"))
		if err != nil {
			os.Exit(92)
		}
		store, err := dbstore.OpenStore(path)
		if err != nil {
			os.Exit(93)
		}
		created, err := store.RegisterProvisionalInventory(context.Background(), raw, os.Getenv("ZHVPN_TEST_CAMPAIGN_INDEPENDENT_SHA"), time.Now())
		if err != nil {
			os.Exit(94)
		}
		if store.Close() != nil {
			os.Exit(95)
		}
		if json.NewEncoder(os.Stdout).Encode(struct {
			Created bool `json:"created"`
		}{created}) != nil {
			os.Exit(96)
		}
		os.Exit(0)
	}
	if mode == "prearm" {
		tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-crash": {Enabled: true}}}
		server, err := NewServer(Config{DBPath: path, MaintenanceInterval: -1}, tokens, auth.NewServer(tokens))
		if err != nil {
			os.Exit(97)
		}
		defer server.Close()
		if os.WriteFile(os.Getenv("ZHVPN_TEST_CAMPAIGN_INDEPENDENT_MARKER"), []byte("prearmed"), 0600) != nil {
			os.Exit(98)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(99)
}

func independentCampaignCommand(ctx context.Context, mode, path string, extra ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCampaignIndependentNativeProcessHelper$")
	cmd.Env = append(os.Environ(), "ZHVPN_TEST_CAMPAIGN_INDEPENDENT_MODE="+mode, "ZHVPN_TEST_CAMPAIGN_INDEPENDENT_DB="+path)
	cmd.Env = append(cmd.Env, extra...)
	return cmd
}

func TestCampaignIndependentNativeConcurrentRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owned-concurrent.db")
	store, err := dbstore.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, raw, digest := independentCampaignInventory(t, "owned-one", "owned-two")
	input := filepath.Join(dir, "owned-inventory.json")
	if os.WriteFile(input, raw, 0600) != nil {
		t.Fatal("could not save owned inventory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	results := make(chan bool, 8)
	failures := make(chan string, 8)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cmd := independentCampaignCommand(ctx, "register", path, "ZHVPN_TEST_CAMPAIGN_INDEPENDENT_INVENTORY="+input, "ZHVPN_TEST_CAMPAIGN_INDEPENDENT_SHA="+digest)
			output, err := cmd.Output()
			if err != nil {
				failures <- err.Error()
				return
			}
			var result struct {
				Created bool `json:"created"`
			}
			if json.Unmarshal(output, &result) != nil {
				failures <- "invalid child receipt"
				return
			}
			results <- result.Created
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	created, total := 0, 0
	for result := range results {
		total++
		if result {
			created++
		}
	}
	if total != 8 || created != 1 {
		t.Fatalf("cross-process registration: replies=%d first-writes=%d", total, created)
	}
	header, err := store.Queries().GetMigrationInventory(context.Background())
	if err != nil || header.BaselineCount != 2 || header.ApprovedSha256 != digest {
		t.Fatal("concurrent immutable header inconsistent")
	}
	members, err := store.Queries().ListMigrationMembers(context.Background())
	if err != nil || len(members) != 2 {
		t.Fatal("concurrent registration duplicated/lost baseline")
	}
}

func TestCampaignIndependentPrearmedCrashAndCleanRestart(t *testing.T) {
	for _, crash := range []bool{false, true} {
		name := "clean"
		if crash {
			name = "native_kill"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "owned-restart.db")
			tokens := &auth.TokenStore{Tokens: map[string]auth.TokenRecord{"owned-crash": {Enabled: true}}}
			first, _ := independentCampaignServer(t, path, tokens)
			digest := independentCampaignRegister(t, first, "owned-crash")
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if crash {
				marker := filepath.Join(dir, "owned-prearm.marker")
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := independentCampaignCommand(ctx, "prearm", path, "ZHVPN_TEST_CAMPAIGN_INDEPENDENT_MARKER="+marker)
				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				waited := false
				defer func() {
					if !waited {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
					}
				}()
				deadline := time.Now().Add(10 * time.Second)
				for {
					if _, err := os.Stat(marker); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("native child never reached persisted pre-arm")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				err := cmd.Wait()
				waited = true
				if err == nil {
					t.Fatal("crash fixture exited normally")
				}
			}
			next, _ := independentCampaignServer(t, path, tokens)
			report := independentCampaignReadiness(t, next)
			if report.BaselineMemberCount != 1 || report.ApprovedInventorySha256 != digest {
				t.Fatal("restart reset fixed inventory")
			}
			if crash {
				if report.Observer.Healthy || report.ObservationWriteHealthy || report.Observer.GapCount < 1 || !independentCampaignBlocker(report.Blockers, "observer_gap") {
					t.Fatal("native kill lost prearmed gap")
				}
				found := false
				for _, run := range report.Observer.Runs {
					if string(run.Reason) == "unclean_shutdown" && string(run.State) == "failed" {
						found = true
					}
				}
				if !found {
					t.Fatal("crash predecessor not retained")
				}
			} else if !report.Observer.Healthy || !report.ObservationWriteHealthy || report.Observer.GapCount != 0 {
				t.Fatal("clean restart fabricated a crash gap")
			}
		})
	}
}
