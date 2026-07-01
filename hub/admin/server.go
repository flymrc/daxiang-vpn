package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"zongheng-vpn/hub/admin/storage"
	"zongheng-vpn/hub/internal/auth"
)

const sessionCookieName = "zhhub_admin_session"

type Server struct {
	cfg        Config
	store      *Store
	tokens     *auth.TokenStore
	clientAuth *auth.Server
	mux        *http.ServeMux
	startedAt  time.Time
	httpClient *http.Client
}

type sessionContext struct {
	session storage.AdminSession
}

func NewServer(cfg Config, tokenStore *auth.TokenStore, clientAuth *auth.Server) (*Server, error) {
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:        cfg,
		store:      store,
		tokens:     tokenStore,
		clientAuth: clientAuth,
		mux:        http.NewServeMux(),
		startedAt:  time.Now(),
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	if err := store.EnsureAdminUser(context.Background(), cfg.AdminUsername, cfg.AdminPasswordPHC, time.Now()); err != nil {
		_ = store.Close()
		return nil, err
	}
	if cfg.AdminPasswordPHC == "" {
		log.Printf("ZHHUB_ADMIN_PASSWORD_HASH 未设置：admin 登录将不可用")
	}
	if clientAuth != nil {
		clientAuth.SetAuditSink(func(event auth.AuditEvent) {
			if err := store.InsertAudit(context.Background(), event); err != nil {
				log.Printf("admin audit 写入失败: %v", err)
			}
		})
	}
	s.routes()
	return s, nil
}

func (s *Server) Close() error {
	return s.store.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/admin/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/admin/api/auth/logout", s.requireSession(s.handleLogout, true))
	s.mux.HandleFunc("/admin/api/auth/me", s.requireSession(s.handleMe, false))
	s.mux.HandleFunc("/admin/api/health", s.handleHealth)
	s.mux.HandleFunc("/admin/api/overview", s.requireSession(s.handleOverview, false))
	s.mux.HandleFunc("/admin/api/tokens", s.requireSession(s.handleTokens, false))
	s.mux.HandleFunc("/admin/api/leases", s.requireSession(s.handleLeases, false))
	s.mux.HandleFunc("/admin/api/egress", s.requireSession(s.handleEgress, false))
	s.mux.HandleFunc("/admin/api/events", s.requireSession(s.handleEvents, false))
	s.mux.HandleFunc("/admin/api/egress/", s.requireSession(s.handleRotateIP, true))
	s.mux.HandleFunc("/admin/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "")
	})
	s.mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	s.mux.HandleFunc("/admin/", s.handleUI)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{Status: Ok})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	source := requestIP(r)
	now := time.Now()
	limited, err := s.loginLimited(r.Context(), req.Username, source, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "")
		return
	}
	if limited {
		s.recordLogin(r.Context(), req.Username, source, false, "rate_limited")
		s.audit("admin.login", req.Username, source, "admin:"+req.Username, `{"reason":"rate_limited"}`, "denied", "rate_limited")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "登录失败次数过多，请稍后再试")
		return
	}
	user, err := s.store.q.GetAdminUser(r.Context(), req.Username)
	if err != nil || !VerifyPassword(user.PasswordHash, req.Password) {
		s.recordLogin(r.Context(), req.Username, source, false, "invalid_credentials")
		s.audit("admin.login", req.Username, source, "admin:"+req.Username, `{"reason":"invalid_credentials"}`, "denied", "invalid_credentials")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}
	sessionToken, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "")
		return
	}
	csrfToken, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "")
		return
	}
	sessionID, err := randomHex(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "random_failed", "")
		return
	}
	expiresAt := now.Add(s.cfg.SessionTTL)
	if err := s.store.q.CreateAdminSession(r.Context(), storage.CreateAdminSessionParams{
		ID:         sessionID,
		Username:   user.Username,
		TokenHash:  hashSecret(sessionToken),
		CsrfToken:  csrfToken,
		SourceIp:   source,
		UserAgent:  r.UserAgent(),
		CreatedAt:  formatTime(now),
		LastSeenAt: formatTime(now),
		ExpiresAt:  formatTime(expiresAt),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "")
		return
	}
	s.recordLogin(r.Context(), req.Username, source, true, "")
	s.audit("admin.login", req.Username, source, "admin:"+req.Username, "{}", "ok", "")
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken,
		Path:     "/admin",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, AuthMeResponse{
		Username:  user.Username,
		CsrfToken: csrfToken,
		ExpiresAt: expiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, sc sessionContext) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	if cookie != nil {
		_ = s.store.q.DeleteAdminSessionByHash(r.Context(), hashSecret(cookie.Value))
	}
	s.audit("admin.logout", sc.session.Username, requestIP(r), "admin:"+sc.session.Username, "{}", "ok", "")
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, sc sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, AuthMeResponse{
		Username:  sc.session.Username,
		CsrfToken: sc.session.CsrfToken,
		ExpiresAt: parseTime(sc.session.ExpiresAt),
	})
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
		if item.Status == Online {
			online++
		}
	}
	rotateToday := s.rotateCountToday(r.Context())
	writeJSON(w, http.StatusOK, OverviewResponse{
		Hub: HubInfo{
			PublicIp:      s.cfg.HubPublicIP,
			WgIp:          s.cfg.HubWGIP,
			Version:       s.cfg.Version,
			UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		},
		Stats: OverviewStats{
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
	writeJSON(w, http.StatusOK, TokensResponse{Tokens: s.tokenSummaries()})
}

func (s *Server) handleLeases(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, LeasesResponse{Leases: s.leaseSummaries()})
}

func (s *Server) handleEgress(w http.ResponseWriter, r *http.Request, _ sessionContext) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, EgressResponse{Egress: s.egressSummaries(r.Context())})
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
	rows, err := s.store.q.ListAuditEvents(r.Context(), int64(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "")
		return
	}
	events := make([]AuditEvent, 0, len(rows))
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
		events = append(events, AuditEvent{
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
	writeJSON(w, http.StatusOK, EventsResponse{Events: events})
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
	var req RotateIPRequest
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
		writeJSON(w, http.StatusConflict, RotateIPResponse{
			Status:            Busy,
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
	writeJSON(w, http.StatusOK, RotateIPResponse{
		Status:      Triggered,
		EgressId:    egressID,
		DownSeconds: downSeconds,
	})
}

func (s *Server) requireSession(next func(http.ResponseWriter, *http.Request, sessionContext), csrf bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.sessionFromRequest(w, r)
		if !ok {
			return
		}
		if csrf && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CsrfToken)) != 1 {
			writeError(w, http.StatusForbidden, "bad_csrf", "")
			return
		}
		next(w, r, sessionContext{session: session})
	}
}

func (s *Server) sessionFromRequest(w http.ResponseWriter, r *http.Request) (storage.AdminSession, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return storage.AdminSession{}, false
	}
	tokenHash := hashSecret(cookie.Value)
	session, err := s.store.q.GetAdminSessionByHash(r.Context(), tokenHash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return storage.AdminSession{}, false
	}
	if expires := parseTime(session.ExpiresAt); !expires.IsZero() && time.Now().After(expires) {
		_ = s.store.q.DeleteAdminSessionByHash(r.Context(), tokenHash)
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return storage.AdminSession{}, false
	}
	_ = s.store.q.TouchAdminSession(r.Context(), storage.TouchAdminSessionParams{
		TokenHash:  tokenHash,
		LastSeenAt: formatTime(time.Now()),
	})
	return session, true
}

func (s *Server) loginLimited(ctx context.Context, username string, sourceIP string, now time.Time) (bool, error) {
	count, err := s.store.q.CountRecentFailedLoginAttempts(ctx, storage.CountRecentFailedLoginAttemptsParams{
		Username:   username,
		SourceIp:   sourceIP,
		OccurredAt: formatTime(now.Add(-15 * time.Minute)),
	})
	return count >= 8, err
}

func (s *Server) recordLogin(ctx context.Context, username string, sourceIP string, success bool, errorCode string) {
	ok := int64(0)
	if success {
		ok = 1
	}
	_ = s.store.q.InsertLoginAttempt(ctx, storage.InsertLoginAttemptParams{
		OccurredAt: formatTime(time.Now()),
		Username:   username,
		SourceIp:   sourceIP,
		Success:    ok,
		ErrorCode:  errorCode,
	})
}

func (s *Server) audit(eventType string, actor string, sourceIP string, target string, detailJSON string, result string, errorCode string) {
	if err := s.store.InsertAudit(context.Background(), auth.AuditEvent{
		OccurredAt: time.Now(),
		Actor:      actor,
		SourceIP:   sourceIP,
		EventType:  eventType,
		Target:     target,
		DetailJSON: detailJSON,
		Result:     result,
		ErrorCode:  errorCode,
	}); err != nil {
		log.Printf("admin audit 写入失败: %v", err)
	}
}

func (s *Server) tokenSummaries() []TokenSummary {
	now := time.Now()
	snapshots := s.tokens.Snapshot()
	rows := make([]TokenSummary, 0, len(snapshots))
	for _, item := range snapshots {
		record := item.Record
		status := tokenStatus(record, now)
		var expires *string
		if record.ExpiresAt != "" {
			value := record.ExpiresAt
			expires = &value
		}
		rows = append(rows, TokenSummary{
			Id:          tokenID(item.Token),
			MaskedToken: maskToken(item.Token),
			ClientName:  record.ClientName,
			Enabled:     record.Enabled,
			Status:      status,
			EgressId:    record.Egress.Name,
			EgressName:  record.Egress.DisplayName,
			WgAddress:   record.WireGuard.Address,
			ExpiresAt:   expires,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].MaskedToken < rows[j].MaskedToken
	})
	return rows
}

func (s *Server) leaseSummaries() []LeaseSummary {
	now := time.Now()
	records := map[string]auth.TokenRecord{}
	for _, item := range s.tokens.Snapshot() {
		records[item.Token] = item.Record
	}
	leases := s.clientAuth.TokenLeasesSnapshot(now)
	rows := make([]LeaseSummary, 0, len(leases))
	for _, lease := range leases {
		record := records[lease.Token]
		var expires *time.Time
		if !lease.ExpiresAt.IsZero() {
			expires = &lease.ExpiresAt
		}
		rows = append(rows, LeaseSummary{
			MaskedToken: maskToken(lease.Token),
			ClientName:  record.ClientName,
			SourceIp:    lease.SourceIP,
			EgressId:    record.Egress.Name,
			SeenAt:      lease.SeenAt,
			ExpiresAt:   expires,
		})
		_ = s.store.q.UpsertTokenLease(context.Background(), storage.UpsertTokenLeaseParams{
			Token:       lease.Token,
			MaskedToken: maskToken(lease.Token),
			ClientName:  record.ClientName,
			SourceIp:    lease.SourceIP,
			EgressID:    record.Egress.Name,
			SeenAt:      formatTime(lease.SeenAt),
			ExpiresAt:   nullStringTime(lease.ExpiresAt),
		})
	}
	_ = s.store.q.DeleteExpiredTokenLeases(context.Background(), sql.NullString{String: formatTime(now), Valid: true})
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SeenAt.After(rows[j].SeenAt)
	})
	return rows
}

func (s *Server) egressSummaries(ctx context.Context) []EgressSummary {
	nodes := map[string]auth.Egress{}
	for _, item := range s.tokens.Snapshot() {
		if item.Record.Egress.Name != "" {
			nodes[item.Record.Egress.Name] = item.Record.Egress
		}
	}
	locks := map[string]time.Time{}
	for _, lock := range s.clientAuth.RotateLocksSnapshot(time.Now()) {
		locks[lock.Egress] = lock.Until
		_ = s.store.q.UpsertRotateLock(ctx, storage.UpsertRotateLockParams{
			EgressID:  lock.Egress,
			StartedAt: formatTime(lock.StartedAt),
			UntilAt:   formatTime(lock.Until),
		})
	}
	rows := make([]EgressSummary, 0, len(nodes))
	for id, node := range nodes {
		deprecated := id == "mac-mini" || strings.Contains(node.ProxyAddr, "10.66.0.100")
		status := Deprecated
		var rawHealth *map[string]interface{}
		var sessions *int
		var active *int
		if !deprecated && id == "jp-android-01" {
			raw, err := s.fetchReverseHealth(ctx)
			if err != nil {
				status = Offline
			} else {
				rawHealth = &raw
				sessionCount := intFromMap(raw, "session_count")
				activeConnections := intFromMap(raw, "active_proxy_connections")
				sessions = &sessionCount
				active = &activeConnections
				if sessionCount > 0 {
					status = Online
				} else {
					status = Degraded
				}
			}
		}
		var lockUntil *time.Time
		if until, ok := locks[id]; ok {
			lockUntil = &until
		}
		row := EgressSummary{
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
		_ = s.store.q.UpsertEgressNode(ctx, storage.UpsertEgressNodeParams{
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
	count, err := s.store.q.CountAuditEventsSince(ctx, formatTime(start))
	if err != nil {
		return 0
	}
	return int(count)
}

func tokenStatus(record auth.TokenRecord, now time.Time) TokenSummaryStatus {
	if !record.Enabled {
		return Disabled
	}
	if record.ExpiresAt != "" {
		expires, err := time.Parse("2006-01-02", record.ExpiresAt)
		if err != nil || now.After(expires.Add(24*time.Hour)) {
			return Expired
		}
		if expires.Sub(now) <= 3*24*time.Hour {
			return Expiring
		}
	}
	return Enabled
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	var msg *string
	if message != "" {
		msg = &message
	}
	writeJSON(w, status, ErrorResponse{Error: code, Message: msg})
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.Trim(host, "[]"))
	if remoteIP != nil && (remoteIP.IsLoopback() || remoteIP.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	return host
}

func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func maskToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 6 {
		return "***"
	}
	return token[:3] + "***" + token[len(token)-2:]
}

func tokenID(token string) string {
	sum := hashSecret(token)
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

func nullStringTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(t), Valid: true}
}

func intFromMap(m map[string]interface{}, key string) int {
	value, ok := m[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		i, _ := typed.Int64()
		return int(i)
	default:
		return 0
	}
}

func parseRotatePath(path string) (string, bool) {
	const prefix = "/admin/api/egress/"
	const suffix = "/rotate-ip"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}
