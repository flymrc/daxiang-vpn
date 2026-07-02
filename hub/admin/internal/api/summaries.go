package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	dbgen "zongheng-vpn/hub/admin/internal/db/generated"
	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
)

func (s *Server) tokenSummaries() []generated.TokenSummary {
	now := time.Now()
	lastActive := map[string]time.Time{}
	if s.clientAuth != nil {
		for _, lease := range s.clientAuth.TokenLeasesSnapshot(now) {
			if !lease.SeenAt.IsZero() {
				lastActive[lease.Token] = lease.SeenAt
			}
		}
	}
	snapshots := s.tokens.Snapshot()
	rows := make([]generated.TokenSummary, 0, len(snapshots))
	for _, item := range snapshots {
		record := item.Record
		status := tokenStatus(record, now)
		var expires *string
		if record.ExpiresAt != "" {
			value := record.ExpiresAt
			expires = &value
		}
		var lastActiveAt *time.Time
		if seenAt, ok := lastActive[item.Token]; ok {
			lastActiveAt = &seenAt
		}
		rows = append(rows, generated.TokenSummary{
			Id:           tokenID(item.Token),
			MaskedToken:  maskToken(item.Token),
			ClientName:   record.ClientName,
			Enabled:      record.Enabled,
			Status:       status,
			EgressId:     record.Egress.Name,
			EgressName:   record.Egress.DisplayName,
			WgAddress:    record.WireGuard.Address,
			ExpiresAt:    expires,
			LastActiveAt: lastActiveAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].MaskedToken < rows[j].MaskedToken
	})
	return rows
}

func (s *Server) leaseSummaries() []generated.LeaseSummary {
	now := time.Now()
	records := map[string]auth.TokenRecord{}
	for _, item := range s.tokens.Snapshot() {
		records[item.Token] = item.Record
	}
	leases := s.clientAuth.TokenLeasesSnapshot(now)
	rows := make([]generated.LeaseSummary, 0, len(leases))
	for _, lease := range leases {
		record := records[lease.Token]
		var expires *time.Time
		if !lease.ExpiresAt.IsZero() {
			expires = &lease.ExpiresAt
		}
		rows = append(rows, generated.LeaseSummary{
			TokenId:     tokenID(lease.Token),
			MaskedToken: maskToken(lease.Token),
			ClientName:  record.ClientName,
			SourceIp:    lease.SourceIP,
			EgressId:    record.Egress.Name,
			SeenAt:      lease.SeenAt,
			ExpiresAt:   expires,
		})
		_ = s.store.Queries().UpsertTokenLease(context.Background(), dbgen.UpsertTokenLeaseParams{
			Token:       lease.Token,
			MaskedToken: maskToken(lease.Token),
			ClientName:  record.ClientName,
			SourceIp:    lease.SourceIP,
			EgressID:    record.Egress.Name,
			SeenAt:      formatTime(lease.SeenAt),
			ExpiresAt:   nullStringTime(lease.ExpiresAt),
		})
	}
	_ = s.store.Queries().DeleteExpiredTokenLeases(context.Background(), sql.NullString{String: formatTime(now), Valid: true})
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SeenAt.After(rows[j].SeenAt)
	})
	return rows
}

func (s *Server) egressSummaries(ctx context.Context) []generated.EgressSummary {
	nodes := map[string]auth.Egress{}
	for _, item := range s.tokens.Snapshot() {
		if item.Record.Egress.Name != "" {
			nodes[item.Record.Egress.Name] = item.Record.Egress
		}
	}
	locks := map[string]time.Time{}
	for _, lock := range s.clientAuth.RotateLocksSnapshot(time.Now()) {
		locks[lock.Egress] = lock.Until
		_ = s.store.Queries().UpsertRotateLock(ctx, dbgen.UpsertRotateLockParams{
			EgressID:  lock.Egress,
			StartedAt: formatTime(lock.StartedAt),
			UntilAt:   formatTime(lock.Until),
		})
	}
	rows := make([]generated.EgressSummary, 0, len(nodes))
	for id, node := range nodes {
		deprecated := id == "mac-mini" || strings.Contains(node.ProxyAddr, "10.66.0.100")
		status := generated.Deprecated
		var rawHealth *map[string]interface{}
		var sessions *int
		var active *int
		if !deprecated && id == "jp-android-01" {
			raw, err := s.fetchReverseHealth(ctx)
			if err != nil {
				status = generated.Offline
			} else {
				rawHealth = &raw
				sessionCount := intFromMap(raw, "session_count")
				activeConnections := intFromMap(raw, "active_proxy_connections")
				sessions = &sessionCount
				active = &activeConnections
				if sessionCount > 0 {
					status = generated.Online
				} else {
					status = generated.Degraded
				}
			}
		}
		var lockUntil *time.Time
		if until, ok := locks[id]; ok {
			lockUntil = &until
		}
		row := generated.EgressSummary{
			Id:                id,
			DisplayName:       node.DisplayName,
			Region:            node.Region,
			Type:              node.Type,
			ManagementAddr:    node.ManagementAddr,
			ProxyAddr:         node.ProxyAddr,
			Status:            status,
			SessionCount:      sessions,
			ActiveConnections: active,
			RotateLockUntil:   lockUntil,
			RawHealth:         rawHealth,
		}
		rows = append(rows, row)
		dep := int64(0)
		if deprecated {
			dep = 1
		}
		_ = s.store.Queries().UpsertEgressNode(ctx, dbgen.UpsertEgressNodeParams{
			EgressID:       id,
			DisplayName:    node.DisplayName,
			Region:         node.Region,
			Type:           node.Type,
			ManagementAddr: node.ManagementAddr,
			ProxyAddr:      node.ProxyAddr,
			Deprecated:     dep,
			UpdatedAt:      formatTime(time.Now()),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Id < rows[j].Id
	})
	return rows
}

func (s *Server) fetchReverseHealth(ctx context.Context) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.ReverseHealthURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reverse health status %d", resp.StatusCode)
	}
	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Server) rotateCountToday(ctx context.Context) int {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	count, err := s.store.Queries().CountAuditEventsSince(ctx, formatTime(start))
	if err != nil {
		return 0
	}
	return int(count)
}

func tokenStatus(record auth.TokenRecord, now time.Time) generated.TokenSummaryStatus {
	if !record.Enabled {
		return generated.Disabled
	}
	if record.ExpiresAt != "" {
		expires, err := time.Parse("2006-01-02", record.ExpiresAt)
		if err != nil || now.After(expires.Add(24*time.Hour)) {
			return generated.Expired
		}
		if expires.Sub(now) <= 3*24*time.Hour {
			return generated.Expiring
		}
	}
	return generated.Enabled
}
