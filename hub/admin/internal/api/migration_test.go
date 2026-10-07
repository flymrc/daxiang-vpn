package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbgen "zongheng-vpn/hub/admin/internal/db/generated"
	"zongheng-vpn/hub/internal/auth"
)

func TestBuildMigrationReadinessRetainsDisabledAndUnregisteredTokens(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	server := &Server{
		tokens: &auth.TokenStore{Tokens: map[string]auth.TokenRecord{
			"ZH-SECURE":   {Enabled: true},
			"ZH-UNKNOWN":  {Enabled: true},
			"ZH-DISABLED": {Enabled: false},
		}},
		startedAt: now.Add(-time.Hour),
	}
	report := server.buildMigrationReadiness([]dbgen.ClientMigrationObservation{
		{
			TokenID:              auth.TokenID("ZH-SECURE"),
			LastSeenUnixNs:       now.Add(-time.Minute).UnixNano(),
			ClientProduct:        "desktop-gui",
			ClientVersion:        "0.4.12",
			ProtocolVersion:      2,
			Ingress:              "trusted_proxy",
			KeyMode:              "client_generated",
			MigrationClass:       "secure_bootstrap",
			SecureBootstrapCount: 1,
		},
		{
			TokenID:        auth.TokenID("ZH-DISABLED"),
			LastSeenUnixNs: now.Add(-time.Minute).UnixNano(),
			MigrationClass: "legacy",
		},
	}, now)

	if report.Ready || report.CampaignConfigured {
		t.Fatalf("observation-only report became ready: %+v", report)
	}
	if report.ValidTokenCount != 2 || report.ObservedTokenCount != 2 || report.UnobservedTokenCount != 1 || report.MemberCount != 3 {
		t.Fatalf("token counts = valid:%d observed:%d unobserved:%d", report.ValidTokenCount, report.ObservedTokenCount, report.UnobservedTokenCount)
	}
	if report.SecureBootstrapTokenCount != 1 || report.LegacyTokenCount != 1 || report.UnknownTokenCount != 1 || len(report.Clients) != 3 {
		t.Fatalf("classification counts = secure:%d unknown:%d clients:%d", report.SecureBootstrapTokenCount, report.UnknownTokenCount, len(report.Clients))
	}
}

func TestBuildMigrationReadinessNeverReady(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	const rawToken = "ZH-RAW-TOKEN-SENTINEL"
	server := &Server{
		tokens: &auth.TokenStore{Tokens: map[string]auth.TokenRecord{
			rawToken: {Enabled: true, WireGuard: auth.WireGuard{PrivateKey: "PRIVATE_KEY_SENTINEL"}},
		}},
		startedAt: now.Add(-time.Hour),
	}
	report := server.buildMigrationReadiness([]dbgen.ClientMigrationObservation{{
		TokenID:              auth.TokenID(rawToken),
		LastSeenUnixNs:       now.Add(-time.Minute).UnixNano(),
		ClientProduct:        "desktop-gui",
		ClientVersion:        "0.4.12",
		ProtocolVersion:      2,
		Ingress:              "trusted_proxy",
		KeyMode:              "client_generated",
		MigrationClass:       "secure_bootstrap",
		SecureBootstrapCount: 1,
	}}, now)

	if report.Ready || report.CampaignConfigured {
		t.Fatalf("report must remain NO-GO: %+v", report)
	}
	if report.ValidTokenCount != 1 || report.SecureBootstrapTokenCount != 1 || report.UnobservedTokenCount != 0 {
		t.Fatalf("unexpected complete-coverage counts: %+v", report)
	}
	if !containsString(report.Blockers, "campaign_not_configured") || !containsString(report.Blockers, "e2e_evidence_unavailable") {
		t.Fatalf("missing permanent observation blockers: %v", report.Blockers)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, rawToken) || strings.Contains(text, "PRIVATE_KEY_SENTINEL") {
		t.Fatalf("readiness report leaked secret input: %s", text)
	}
}

func containsString[T ~string](values []T, want string) bool {
	for _, value := range values {
		if string(value) == want {
			return true
		}
	}
	return false
}
