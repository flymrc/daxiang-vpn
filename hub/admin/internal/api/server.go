package api

import (
	"context"
	"log"
	"net/http"
	"time"

	dbstore "zongheng-vpn/hub/admin/internal/db"
	webui "zongheng-vpn/hub/admin/web"
	"zongheng-vpn/hub/internal/auth"
)

const sessionCookieName = "zhhub_admin_session"

type Server struct {
	cfg        Config
	store      *dbstore.Store
	tokens     *auth.TokenStore
	clientAuth *auth.Server
	mux        *http.ServeMux
	startedAt  time.Time
	httpClient *http.Client
}

func NewServer(cfg Config, tokenStore *auth.TokenStore, clientAuth *auth.Server) (*Server, error) {
	store, err := dbstore.OpenStore(cfg.DBPath)
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
	s.mux.Handle("/admin/", webui.Handler())
}
