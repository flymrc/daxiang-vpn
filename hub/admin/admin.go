package admin

import (
	"net/http"

	"zongheng-vpn/hub/admin/internal/api"
	"zongheng-vpn/hub/internal/auth"
)

type Server struct {
	inner *api.Server
}

func NewServer(cfg Config, tokenStore *auth.TokenStore, clientAuth *auth.Server) (*Server, error) {
	inner, err := api.NewServer(cfg, tokenStore, clientAuth)
	if err != nil {
		return nil, err
	}
	return &Server{inner: inner}, nil
}

func (s *Server) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.inner.ServeHTTP(w, r)
}
