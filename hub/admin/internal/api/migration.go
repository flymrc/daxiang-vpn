package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	dbstore "zongheng-vpn/hub/admin/internal/db"
	dbgen "zongheng-vpn/hub/admin/internal/db/generated"
	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
)

var opaque12 = regexp.MustCompile(`^[a-f0-9]{12}$`)
var opaque32 = regexp.MustCompile(`^[a-f0-9]{32}$`)
var digest64 = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *Server) handleMigrationReadiness(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method_not_allowed", "")
		return
	}
	now := time.Now()
	sources, err := s.migrationSources(now)
	if err != nil {
		writeError(w, 503, "projection_unavailable", "")
		return
	}
	snapshot, err := s.store.CampaignSnapshot(r.Context(), sources, s.observerRunID, now)
	if err != nil {
		s.failObserver("write_failed")
		writeError(w, 503, "projection_unavailable", "")
		return
	}
	report, err := s.buildCampaignReadiness(snapshot, now)
	if err != nil {
		s.failObserver("write_failed")
		writeError(w, 503, "projection_unavailable", "")
		return
	}
	writeJSON(w, 200, report)
}

// All lifecycle decisions derive from one complete source snapshot. Trim
// aliases and short-ID collisions within that snapshot refuse the whole report.
func (s *Server) migrationSources(now time.Time) ([]dbstore.SourceToken, error) {
	if s.tokens != nil && len(s.tokens.Tokens) > dbstore.MaxCampaignMembers {
		return nil, dbstore.ErrProjectionUnavailable
	}
	snapshot := s.tokens.Snapshot()
	if len(snapshot) > dbstore.MaxCampaignMembers {
		return nil, dbstore.ErrProjectionUnavailable
	}
	sources := make([]dbstore.SourceToken, 0, len(snapshot))
	seen := map[string]bool{}
	for _, item := range snapshot {
		if item.Token == "" || item.Token != strings.TrimSpace(item.Token) {
			return nil, dbstore.ErrProjectionUnavailable
		}
		id := auth.TokenID(item.Token)
		if seen[id] {
			return nil, dbstore.ErrProjectionUnavailable
		}
		seen[id] = true
		state := "enabled"
		if item.Record.ExpiresAt != "" {
			expiry, e := time.Parse("2006-01-02", item.Record.ExpiresAt)
			if e != nil || expiry.Format("2006-01-02") != item.Record.ExpiresAt {
				state = "invalid"
			} else if now.After(expiry.Add(24 * time.Hour)) {
				state = "expired"
			}
		}
		if !item.Record.Enabled && state != "invalid" {
			state = "disabled"
		}
		sources = append(sources, dbstore.SourceToken{TokenID: id, State: state})
	}
	return sources, nil
}
func reportTime(t time.Time, now time.Time) bool {
	return !t.IsZero() && t.UnixNano() > 0 && time.Unix(0, t.UnixNano()).Equal(t) && !t.After(now.Add(time.Minute))
}
func reportTimeText(value string, now time.Time) (time.Time, error) {
	t, e := time.Parse(time.RFC3339Nano, value)
	if e != nil || !reportTime(t, now) {
		return time.Time{}, dbstore.ErrProjectionUnavailable
	}
	return t, nil
}

func (s *Server) buildCampaignReadiness(snapshot dbstore.CampaignSnapshot, now time.Time) (generated.MigrationReadinessResponse, error) {
	fail := func() (generated.MigrationReadinessResponse, error) {
		return generated.MigrationReadinessResponse{}, dbstore.ErrProjectionUnavailable
	}
	report := generated.MigrationReadinessResponse{ContractVersion: 2, Mode: generated.ObservationOnly, Clients: make([]generated.MigrationClientStatus, 0, len(snapshot.Members)), GeneratedAt: now, ObserverStartedAt: s.startedAt, Observer: generated.MigrationObserver{CurrentRunId: s.observerRunID, Runs: []generated.MigrationObserverRun{}}}
	global := map[generated.MigrationBlocker]bool{}
	add := func(code string) { global[generated.MigrationBlocker(code)] = true }
	for _, code := range []string{"campaign_not_configured", "e2e_evidence_unavailable", "approved_release_unavailable", "revocation_evidence_unavailable", "session_cleanup_unavailable", "recovery_evidence_unavailable", "quiet_window_unavailable", "observer_continuity_unavailable", "dataplane_startup_unverified"} {
		add(code)
	}
	if snapshot.Inventory != nil {
		h := snapshot.Inventory
		if !opaque32.MatchString(h.RegistryID) || !digest64.MatchString(h.ApprovedSha256) || h.BaselineCount < 1 || h.BaselineCount > dbstore.MaxCampaignMembers {
			return fail()
		}
		report.InventoryRegistered = true
		report.RegistryId = h.RegistryID
		report.ApprovedInventorySha256 = h.ApprovedSha256
		report.BaselineMemberCount = int(h.BaselineCount)
	} else {
		add("inventory_not_registered")
	}
	facts := map[string]dbgen.MigrationObservationFact{}
	for _, f := range snapshot.Facts {
		facts[f.TokenID] = f
	}
	observations := map[string]dbgen.ClientMigrationObservation{}
	for _, o := range snapshot.Observations {
		observations[o.TokenID] = o
	}
	baseline := 0
	seen := map[string]bool{}
	for _, member := range snapshot.Members {
		if !opaque12.MatchString(member.TokenID) || seen[member.TokenID] || (member.OwnerRef != "" && !opaque32.MatchString(member.OwnerRef)) || (member.Shared != 0 && member.Shared != 1) {
			return fail()
		}
		seen[member.TokenID] = true
		refs := []string{}
		if json.Unmarshal([]byte(member.InstallationRefsJson), &refs) != nil || refs == nil || len(refs) > dbstore.MaxInstallationRefs {
			return fail()
		}
		seenRefs := map[string]bool{}
		for _, r := range refs {
			if !opaque32.MatchString(r) || seenRefs[r] {
				return fail()
			}
			seenRefs[r] = true
		}
		status := generated.MigrationClientStatus{TokenId: member.TokenID, Membership: generated.MigrationClientStatusMembership(member.Membership), OwnerRef: member.OwnerRef, Shared: member.Shared != 0, InstallationRefs: refs, MigrationClass: generated.Unknown, LineageState: generated.MigrationClientStatusLineageState("unknown"), Blockers: []generated.MigrationBlocker{}}
		rowBlocks := map[generated.MigrationBlocker]bool{}
		rowAdd := func(code string) { rowBlocks[generated.MigrationBlocker(code)] = true; add(code) }
		for _, code := range []string{"approved_release_unavailable", "e2e_evidence_unavailable", "revocation_evidence_unavailable", "session_cleanup_unavailable", "recovery_evidence_unavailable"} {
			rowAdd(code)
		}
		switch member.Membership {
		case "baseline":
			baseline++
			if !report.InventoryRegistered {
				return fail()
			}
		case "extra":
			report.ExtraMemberCount++
			if !report.InventoryRegistered {
				return fail()
			}
			rowAdd("extra_unregistered")
		case "unregistered":
			if report.InventoryRegistered {
				return fail()
			}
		default:
			return fail()
		}
		state, ok := snapshot.Sources[member.TokenID]
		if !ok {
			state = "missing"
		}
		status.SourceState = generated.MigrationClientStatusSourceState(state)
		switch state {
		case "enabled":
			report.ValidTokenCount++
		case "missing", "disabled", "expired", "invalid":
			rowAdd("source_" + state)
			rowAdd("disposition_required")
		default:
			return fail()
		}
		if status.Shared || len(refs) > 1 {
			status.LineageState = generated.MigrationClientStatusLineageState("shared_unverified")
			rowAdd("shared_lineage_unverified")
		} else if member.OwnerRef != "" && len(refs) == 1 {
			status.LineageState = generated.MigrationClientStatusLineageState("declared_unverified")
			rowAdd("lineage_unverified")
		} else {
			rowAdd("lineage_unknown")
		}
		if f, ok := facts[member.TokenID]; ok {
			counts := []int64{f.SecureBootstrapCount, f.LegacyCount, f.UnknownCount, f.CompatIngressCount, f.DeniedCount, f.ErrorCount}
			for _, c := range counts {
				if c < 0 || c > dbstore.MaxSafeCounter {
					return fail()
				}
			}
			first, last := time.Unix(0, f.FirstSeenUnixNs).UTC(), time.Unix(0, f.LastSeenUnixNs).UTC()
			if !reportTime(first, now) || !reportTime(last, now) || last.Before(first) {
				return fail()
			}
			status.Observed = true
			status.LastSeenAt = &last
			status.History = generated.MigrationHistory{SecureBootstrapCount: f.SecureBootstrapCount, LegacyCount: f.LegacyCount, UnknownCount: f.UnknownCount, CompatIngressCount: f.CompatIngressCount, DeniedCount: f.DeniedCount, ErrorCount: f.ErrorCount, FirstSeenAt: &first, LastSeenAt: &last}
			if report.LastObservationAt == nil || last.After(*report.LastObservationAt) {
				report.LastObservationAt = &last
			}
			for i, code := range []string{"historical_legacy", "historical_unknown", "historical_compat", "historical_denied", "historical_error"} {
				if counts[i+1] > 0 {
					rowAdd(code)
				}
			}
			if f.CompatIngressCount > 0 {
				report.CompatIngressTokenCount++
			}
		}
		if o, ok := observations[member.TokenID]; ok {
			status.ClientProduct, status.ClientVersion, status.ProtocolVersion = auth.NormalizeClientMetadata(o.ClientProduct, o.ClientVersion, int(o.ProtocolVersion))
			if o.Ingress == "compat" || o.Ingress == "trusted_proxy" {
				status.Ingress = o.Ingress
			}
			if o.KeyMode == "client_generated" || o.KeyMode == "server_legacy" {
				status.KeyMode = o.KeyMode
			}
			status.PrivateKeyReturned = o.PrivateKeyReturned != 0
			switch o.MigrationClass {
			case "secure_bootstrap", "legacy", "unknown":
				status.MigrationClass = generated.MigrationClientStatusMigrationClass(o.MigrationClass)
			default:
				return fail()
			}
			// Historical v1 labels are untrusted inputs to the current classifier.
			// Their accounting remains in history, but cannot be a secure candidate.
			if status.MigrationClass == generated.SecureBootstrap && (o.Ingress != "trusted_proxy" || o.KeyMode != "client_generated" || o.PrivateKeyReturned != 0 || !auth.ValidClientMetadata(o.ClientProduct, o.ClientVersion, int(o.ProtocolVersion))) {
				status.MigrationClass = generated.Unknown
			}
		}
		if status.Observed {
			report.ObservedTokenCount++
		} else {
			report.UnobservedTokenCount++
			rowAdd("unobserved_tokens")
		}
		switch status.MigrationClass {
		case generated.SecureBootstrap:
			report.SecureBootstrapTokenCount++
		case generated.Legacy:
			report.LegacyTokenCount++
		default:
			report.UnknownTokenCount++
		}
		if status.MigrationClass != generated.SecureBootstrap {
			rowAdd("non_secure_bootstrap_tokens")
		}
		for b := range rowBlocks {
			status.Blockers = append(status.Blockers, b)
		}
		sort.Slice(status.Blockers, func(i, j int) bool { return status.Blockers[i] < status.Blockers[j] })
		report.Clients = append(report.Clients, status)
	}
	report.MemberCount = len(report.Clients)
	if report.MemberCount > dbstore.MaxCampaignMembers || (report.InventoryRegistered && baseline != report.BaselineMemberCount) {
		return fail()
	}
	currentOpen := false
	seenRuns := map[string]bool{}
	for _, run := range snapshot.Runs {
		if !opaque32.MatchString(run.RunID) || seenRuns[run.RunID] {
			return fail()
		}
		seenRuns[run.RunID] = true
		started, e := reportTimeText(run.StartedAt, now)
		if e != nil {
			return fail()
		}
		r := generated.MigrationObserverRun{RunId: run.RunID, StartedAt: started, State: generated.MigrationObserverRunState(run.State), Reason: generated.MigrationObserverRunReason(run.Reason)}
		if run.EndedAt.Valid {
			t, e := reportTimeText(run.EndedAt.String, now)
			if e != nil || t.Before(started) {
				return fail()
			}
			r.EndedAt = &t
		}
		if run.LastSuccessAt.Valid {
			t, e := reportTimeText(run.LastSuccessAt.String, now)
			if e != nil || t.Before(started) {
				return fail()
			}
			r.LastSuccessAt = &t
			if report.Observer.LastSuccessAt == nil || t.After(*report.Observer.LastSuccessAt) {
				report.Observer.LastSuccessAt = &t
			}
		}
		switch run.State {
		case "open":
			if run.Reason != "none" || run.EndedAt.Valid {
				return fail()
			}
			if run.RunID == s.observerRunID {
				currentOpen = true
			} else {
				report.Observer.GapCount++
			}
		case "closed":
			if run.Reason != "none" || !run.EndedAt.Valid {
				return fail()
			}
		case "failed":
			switch run.Reason {
			case "write_failed", "unclean_shutdown", "sink_replaced", "observer_closed":
			default:
				return fail()
			}
			report.Observer.GapCount++
		default:
			return fail()
		}
		report.Observer.Runs = append(report.Observer.Runs, r)
	}
	if len(snapshot.Runs) > dbstore.MaxObserverRuns {
		return fail()
	}
	report.Observer.Healthy = currentOpen && report.Observer.GapCount == 0 && !s.observationWriteFailed.Load()
	report.ObservationWriteHealthy = report.Observer.Healthy
	if report.Observer.GapCount > 0 {
		add("observer_gap")
	}
	if !report.Observer.Healthy {
		add("observation_write_failed")
	}
	for b := range global {
		report.Blockers = append(report.Blockers, b)
	}
	sort.Slice(report.Blockers, func(i, j int) bool { return report.Blockers[i] < report.Blockers[j] })
	sort.Slice(report.Clients, func(i, j int) bool { return report.Clients[i].TokenId < report.Clients[j].TokenId })
	return report, nil
}

// Production uses the atomic durable snapshot. This adapter exists for pure
// projection tests of pre-registration data, without inventing campaign facts.
func (s *Server) buildMigrationReadiness(rows []dbgen.ClientMigrationObservation, now time.Time) generated.MigrationReadinessResponse {
	sources, e := s.migrationSources(now)
	if e != nil {
		return generated.MigrationReadinessResponse{}
	}
	snapshot := dbstore.CampaignSnapshot{Sources: map[string]string{}, Observations: rows}
	ids := map[string]bool{}
	for _, src := range sources {
		snapshot.Sources[src.TokenID] = src.State
		ids[src.TokenID] = true
	}
	for _, r := range rows {
		ids[r.TokenID] = true
		first := r.FirstSeenUnixNs
		if first == 0 {
			first = r.LastSeenUnixNs
		}
		snapshot.Facts = append(snapshot.Facts, dbgen.MigrationObservationFact{TokenID: r.TokenID, FirstSeenUnixNs: first, LastSeenUnixNs: r.LastSeenUnixNs, SecureBootstrapCount: r.SecureBootstrapCount, LegacyCount: r.LegacyCount, UnknownCount: r.UnknownCount, CompatIngressCount: r.CompatIngressCount})
	}
	for id := range ids {
		snapshot.Members = append(snapshot.Members, dbgen.MigrationMember{TokenID: id, Membership: "unregistered", InstallationRefsJson: "[]"})
	}
	report, _ := s.buildCampaignReadiness(snapshot, now)
	return report
}
