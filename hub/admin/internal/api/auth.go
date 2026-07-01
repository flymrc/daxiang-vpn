package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	dbgen "zongheng-vpn/hub/admin/internal/db/generated"
	"zongheng-vpn/hub/admin/internal/security"
	generated "zongheng-vpn/hub/admin/internal/spec/generated"
)

type sessionContext struct {
	session dbgen.AdminSession
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	var req generated.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	req.Username = truncateText(req.Username, maxAdminUsernameBytes)
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
	user, err := s.store.Queries().GetAdminUser(r.Context(), req.Username)
	if err != nil || !security.VerifyPassword(user.PasswordHash, req.Password) {
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
	if err := s.store.Queries().CreateAdminSession(r.Context(), dbgen.CreateAdminSessionParams{
		ID:         sessionID,
		Username:   user.Username,
		TokenHash:  hashSecret(sessionToken),
		CsrfToken:  csrfToken,
		SourceIp:   source,
		UserAgent:  truncateText(r.UserAgent(), maxUserAgentBytes),
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
	writeJSON(w, http.StatusOK, generated.AuthMeResponse{
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
		_ = s.store.Queries().DeleteAdminSessionByHash(r.Context(), hashSecret(cookie.Value))
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
	writeJSON(w, http.StatusOK, generated.AuthMeResponse{
		Username:  sc.session.Username,
		CsrfToken: sc.session.CsrfToken,
		ExpiresAt: parseTime(sc.session.ExpiresAt),
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

func requireCSRF(w http.ResponseWriter, r *http.Request, sc sessionContext) bool {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sc.session.CsrfToken)) != 1 {
		writeError(w, http.StatusForbidden, "bad_csrf", "")
		return false
	}
	return true
}

func (s *Server) sessionFromRequest(w http.ResponseWriter, r *http.Request) (dbgen.AdminSession, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return dbgen.AdminSession{}, false
	}
	tokenHash := hashSecret(cookie.Value)
	session, err := s.store.Queries().GetAdminSessionByHash(r.Context(), tokenHash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return dbgen.AdminSession{}, false
	}
	if expires := parseTime(session.ExpiresAt); !expires.IsZero() && time.Now().After(expires) {
		_ = s.store.Queries().DeleteAdminSessionByHash(r.Context(), tokenHash)
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return dbgen.AdminSession{}, false
	}
	_ = s.store.Queries().TouchAdminSession(r.Context(), dbgen.TouchAdminSessionParams{
		TokenHash:  tokenHash,
		LastSeenAt: formatTime(time.Now()),
	})
	return session, true
}

func (s *Server) loginLimited(ctx context.Context, username string, sourceIP string, now time.Time) (bool, error) {
	count, err := s.store.Queries().CountRecentFailedLoginAttempts(ctx, dbgen.CountRecentFailedLoginAttemptsParams{
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
	_ = s.store.Queries().InsertLoginAttempt(ctx, dbgen.InsertLoginAttemptParams{
		OccurredAt: formatTime(time.Now()),
		Username:   truncateText(username, maxAdminUsernameBytes),
		SourceIp:   truncateText(sourceIP, maxSourceIPBytes),
		Success:    ok,
		ErrorCode:  truncateText(errorCode, maxErrorCodeBytes),
	})
}
