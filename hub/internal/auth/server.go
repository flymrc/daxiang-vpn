package auth

import (
	"encoding/json"
	"log"
	"net"
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
		log.Printf("bootstrap 拒绝 src=%s token=%q reason=invalid_token", clientIP(r), maskToken(req.Token))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}

	log.Printf("bootstrap 通过 src=%s token=%q client=%s egress=%s", clientIP(r), maskToken(req.Token), record.ClientName, record.Egress.Name)
	writeJSON(w, http.StatusOK, bootstrapResponse{
		Client:     clientResponse{Name: record.ClientName},
		Hub:        record.Hub,
		Egress:     record.Egress,
		LocalProxy: record.LocalProxy,
		WireGuard:  record.WireGuard,
	})
}

// clientIP 优先取反向代理透传的 X-Forwarded-For 首段，否则用 TCP 远端地址。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexComma(xff); i >= 0 {
			return trimSpace(xff[:i])
		}
		return trimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// maskToken 只保留首尾，避免把完整授权码写进日志。
func maskToken(t string) string {
	t = trimSpace(t)
	if len(t) <= 6 {
		return "***"
	}
	return t[:3] + "***" + t[len(t)-2:]
}

func indexComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
