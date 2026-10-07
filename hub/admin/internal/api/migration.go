package api

import (
	"net/http"
	"sort"
	"time"

	dbgen "zongheng-vpn/hub/admin/internal/db/generated"
	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
)

func (s *Server) handleMigrationReadiness(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	rows, err := s.store.ListClientMigrationObservations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "")
		return
	}
	writeJSON(w, http.StatusOK, s.buildMigrationReadiness(rows, time.Now()))
}

func (s *Server) buildMigrationReadiness(rows []dbgen.ClientMigrationObservation, now time.Time) generated.MigrationReadinessResponse {
	observations := make(map[string]dbgen.ClientMigrationObservation, len(rows))
	var lastObservationAt *time.Time
	for _, row := range rows {
		observations[row.TokenID] = row
		if row.LastSeenUnixNs > 0 && (lastObservationAt == nil || row.LastSeenUnixNs > lastObservationAt.UnixNano()) {
			value := time.Unix(0, row.LastSeenUnixNs).UTC()
			lastObservationAt = &value
		}
	}

	clients := make([]generated.MigrationClientStatus, 0)
	secureCount, legacyCount, unknownCount, compatCount := 0, 0, 0, 0
	for _, item := range s.tokens.Snapshot() {
		if _, ok := s.tokens.Resolve(item.Token, now); !ok {
			continue
		}
		status := generated.MigrationClientStatus{
			TokenId:        auth.TokenID(item.Token),
			MigrationClass: generated.MigrationClientStatusMigrationClassUnknown,
		}
		if row, ok := observations[status.TokenId]; ok {
			status.Observed = true
			status.ClientProduct = row.ClientProduct
			status.ClientVersion = row.ClientVersion
			status.ProtocolVersion = int(row.ProtocolVersion)
			status.Ingress = row.Ingress
			status.KeyMode = row.KeyMode
			status.PrivateKeyReturned = row.PrivateKeyReturned != 0
			status.MigrationClass = generated.MigrationClientStatusMigrationClass(row.MigrationClass)
			seenAt := time.Unix(0, row.LastSeenUnixNs).UTC()
			status.LastSeenAt = &seenAt
			if row.Ingress == string(auth.ClientIngressCompat) {
				compatCount++
			}
		}
		switch status.MigrationClass {
		case generated.MigrationClientStatusMigrationClassSecureBootstrap:
			secureCount++
		case generated.MigrationClientStatusMigrationClassLegacy:
			legacyCount++
		default:
			unknownCount++
		}
		clients = append(clients, status)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].TokenId < clients[j].TokenId })

	observedCount := 0
	for _, client := range clients {
		if client.Observed {
			observedCount++
		}
	}
	unobservedCount := len(clients) - observedCount
	blockers := []string{"campaign_not_configured", "e2e_evidence_unavailable"}
	if s.observationWriteFailed.Load() {
		blockers = append(blockers, "observation_write_failed")
	}
	if unobservedCount > 0 {
		blockers = append(blockers, "unobserved_tokens")
	}
	if secureCount != len(clients) {
		blockers = append(blockers, "non_secure_bootstrap_tokens")
	}
	return generated.MigrationReadinessResponse{
		Ready:                     false,
		Mode:                      generated.ObservationOnly,
		CampaignConfigured:        false,
		ObservationWriteHealthy:   !s.observationWriteFailed.Load(),
		ValidTokenCount:           len(clients),
		ObservedTokenCount:        observedCount,
		UnobservedTokenCount:      unobservedCount,
		SecureBootstrapTokenCount: secureCount,
		LegacyTokenCount:          legacyCount,
		UnknownTokenCount:         unknownCount,
		CompatIngressTokenCount:   compatCount,
		ObserverStartedAt:         s.startedAt,
		LastObservationAt:         lastObservationAt,
		Blockers:                  blockers,
		Clients:                   clients,
		GeneratedAt:               now,
	}
}
