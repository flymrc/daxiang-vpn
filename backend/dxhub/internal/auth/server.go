package auth

import (
	"encoding/json"
	"net/http"
	"time"
)

type Server struct {
	store *TokenStore
}

type bootstrapRequest struct {
	Token string `json:"token"`
}

type bootstrapResponse struct {
	Client     clientResponse `json:"client"`
	Hub        Hub            `json:"hub"`
	Egress     Egress         `json:"egress"`
	LocalProxy LocalProxy     `json:"local_proxy"`
	WireGuard  WireGuard      `json:"wireguard"`
}

type clientResponse struct {
	Name string `json:"name"`
}

func NewServer(store *TokenStore) *Server {
	return &Server{store: store}
}

func (s *Server) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) Bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}

	var req bootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}

	record, ok := s.store.Resolve(req.Token, time.Now())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}

	writeJSON(w, http.StatusOK, bootstrapResponse{
		Client:     clientResponse{Name: record.ClientName},
		Hub:        record.Hub,
		Egress:     record.Egress,
		LocalProxy: record.LocalProxy,
		WireGuard:  record.WireGuard,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
