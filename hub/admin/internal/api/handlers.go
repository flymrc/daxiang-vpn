package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	generated "zongheng-vpn/hub/admin/internal/spec/generated"
	"zongheng-vpn/hub/internal/auth"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, generated.HealthResponse{Status: generated.Ok})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	tokens := s.tokenSummaries()
	enabled := 0
	for _, token := range tokens {
		if token.Enabled {
			enabled++
		}
	}
	leases := s.leaseSummaries()
	egress := s.egressSummaries(r.Context())
	online := 0
	for _, item := range egress {
		if item.Status == generated.Online {
			online++
		}
	}
	rotateToday := s.rotateCountToday(r.Context())
	writeJSON(w, http.StatusOK, generated.OverviewResponse{
		Hub: generated.HubInfo{
			PublicIp:      s.cfg.HubPublicIP,
			WgIp:          s.cfg.HubWGIP,
			Version:       s.cfg.Version,
			UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		},
		Stats: generated.OverviewStats{
			TokenCount:        len(tokens),
			EnabledTokenCount: enabled,
			ActiveLeaseCount:  len(leases),
			EgressOnlineCount: online,
			RotateTodayCount:  rotateToday,
		},
		UpdatedAt: time.Now(),
	})
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, generated.TokensResponse{Tokens: s.tokenSummaries()})
}

func (s *Server) handleTokenSecret(w http.ResponseWriter, r *http.Request, sc sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	id, ok := parseTokenSecretPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	for _, item := range s.tokens.Snapshot() {
		if tokenID(item.Token) == id {
			s.audit("admin.reveal_token", sc.session.Username, requestIP(r), "token:"+id, "{}", "ok", "")
			writeJSON(w, http.StatusOK, generated.TokenSecretResponse{
				Id:    id,
				Token: item.Token,
			})
			return
		}
	}
	s.audit("admin.reveal_token", sc.session.Username, requestIP(r), "token:"+id, "{}", "denied", "token_not_found")
	writeError(w, http.StatusNotFound, "token_not_found", "")
}

func (s *Server) handleLeases(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, generated.LeasesResponse{Leases: s.leaseSummaries()})
}

func (s *Server) handleEgress(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, generated.EgressResponse{Egress: s.egressSummaries(r.Context())})
}

func (s *Server) handleEgressAction(w http.ResponseWriter, r *http.Request, sc sessionContext) {
	if _, ok := parseRotatePath(r.URL.Path); ok {
		if !requireCSRF(w, r, sc) {
			return
		}
		s.handleRotateIP(w, r, sc)
		return
	}
	if _, ok := parseEgressExitIPPath(r.URL.Path); ok {
		s.handleEgressExitIP(w, r, sc)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "")
}

func (s *Server) handleEgressExitIP(w http.ResponseWriter, r *http.Request, sc sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	egressID, ok := parseEgressExitIPPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	egress, ok := s.tokens.EgressByName(egressID)
	if !ok {
		s.audit("admin.reveal_exit_ip", sc.session.Username, requestIP(r), "egress:"+egressID, "{}", "denied", "egress_not_found")
		writeError(w, http.StatusNotFound, "egress_not_found", "")
		return
	}
	exitIP, err := s.checkEgressExitIP(r.Context(), egress.ProxyAddr)
	if err != nil {
		s.audit("admin.reveal_exit_ip", sc.session.Username, requestIP(r), "egress:"+egressID, fmt.Sprintf(`{"error":%q}`, err.Error()), "error", "exit_ip_check_failed")
		writeError(w, http.StatusBadGateway, "exit_ip_check_failed", "")
		return
	}
	s.audit("admin.reveal_exit_ip", sc.session.Username, requestIP(r), "egress:"+egressID, "{}", "ok", "")
	writeJSON(w, http.StatusOK, generated.EgressExitIPResponse{
		EgressId:  egressID,
		ExitIp:    exitIP,
		CheckedAt: time.Now(),
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	rows, err := s.store.Queries().ListAuditEvents(r.Context(), int64(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "")
		return
	}
	events := make([]generated.AuditEvent, 0, len(rows))
	for _, row := range rows {
		detail := map[string]interface{}{}
		if row.DetailJson != "" {
			_ = json.Unmarshal([]byte(row.DetailJson), &detail)
		}
		var detailPtr *map[string]interface{}
		if len(detail) > 0 {
			detailPtr = &detail
		}
		var errorCode *string
		if row.ErrorCode != "" {
			errorCode = &row.ErrorCode
		}
		events = append(events, generated.AuditEvent{
			Id:         row.ID,
			OccurredAt: parseTime(row.OccurredAt),
			Actor:      row.Actor,
			SourceIp:   row.SourceIp,
			EventType:  row.EventType,
			Target:     row.Target,
			Detail:     detailPtr,
			Result:     row.Result,
			ErrorCode:  errorCode,
		})
	}
	writeJSON(w, http.StatusOK, generated.EventsResponse{Events: events})
}

func (s *Server) handleRotateIP(w http.ResponseWriter, r *http.Request, sc sessionContext) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	egressID, ok := parseRotatePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	var req generated.RotateIPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	downSeconds := 8
	if req.DownSeconds != nil {
		downSeconds = *req.DownSeconds
	}
	egress, ok := s.tokens.EgressByName(egressID)
	if !ok {
		writeError(w, http.StatusNotFound, "egress_not_found", "")
		return
	}
	result, err := s.clientAuth.RotateEgress(egress, downSeconds)
	detail := fmt.Sprintf(`{"down_seconds":%d}`, downSeconds)
	if errors.Is(err, auth.ErrRotateBusy) {
		retry := result.RetryAfterSeconds
		message := "换 IP 正在进行中，请稍后再试"
		s.audit("admin.rotate_ip", sc.session.Username, requestIP(r), "egress:"+egressID, fmt.Sprintf(`{"down_seconds":%d,"retry_after_seconds":%d}`, downSeconds, retry), "busy", "rotate_busy")
		writeJSON(w, http.StatusConflict, generated.RotateIPResponse{
			Status:            generated.Busy,
			EgressId:          egressID,
			DownSeconds:       downSeconds,
			RetryAfterSeconds: &retry,
			Message:           &message,
		})
		return
	}
	if errors.Is(err, auth.ErrInvalidDownSeconds) {
		s.audit("admin.rotate_ip", sc.session.Username, requestIP(r), "egress:"+egressID, detail, "denied", "invalid_down_seconds")
		writeError(w, http.StatusBadRequest, "invalid_down_seconds", "")
		return
	}
	if errors.Is(err, auth.ErrUnsupportedEgress) {
		s.audit("admin.rotate_ip", sc.session.Username, requestIP(r), "egress:"+egressID, detail, "denied", "unsupported_egress")
		writeError(w, http.StatusBadRequest, "unsupported_egress", "")
		return
	}
	if err != nil {
		s.audit("admin.rotate_ip", sc.session.Username, requestIP(r), "egress:"+egressID, detail, "error", "control_failed")
		writeError(w, http.StatusBadGateway, "control_failed", "")
		return
	}
	s.audit("admin.rotate_ip", sc.session.Username, requestIP(r), "egress:"+egressID, fmt.Sprintf(`{"down_seconds":%d,"lock_until":%q}`, downSeconds, result.LockUntil.Format(time.RFC3339)), "ok", "")
	writeJSON(w, http.StatusOK, generated.RotateIPResponse{
		Status:      generated.Triggered,
		EgressId:    egressID,
		DownSeconds: downSeconds,
	})
}

func (s *Server) checkEgressExitIP(ctx context.Context, proxyAddr string) (string, error) {
	proxyAddr = strings.TrimSpace(proxyAddr)
	if proxyAddr == "" {
		return "", fmt.Errorf("empty egress proxy address")
	}
	proxyURL := &url.URL{Scheme: "http", Host: proxyAddr}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   s.cfg.ExitIPCheckTimeout,
		Transport: transport,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.ExitIPCheckURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "zhhub-admin/exit-ip-check")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("exit ip check status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if net.ParseIP(strings.Trim(value, "[]")) == nil {
		return "", fmt.Errorf("exit ip check returned non-ip value")
	}
	return value, nil
}
