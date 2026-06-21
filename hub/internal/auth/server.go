package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	store           *TokenStore
	tokenLeases     map[string]tokenLease
	tokenLeasesMu   sync.Mutex
	tokenLeaseTTL   time.Duration
	rotateLocks     map[string]rotateLock
	rotateLocksMu   sync.Mutex
	rotateLockExtra time.Duration
	triggerRotateIP func(string, int) error
	carrierCache    map[string]carrierCacheEntry
	carrierCacheMu  sync.Mutex
	carrierCacheTTL time.Duration
	carrierProbe    func(string) string
}

type tokenLease struct {
	sourceIP string
	seenAt   time.Time
}

type rotateLock struct {
	startedAt time.Time
	until     time.Time
}

type carrierCacheEntry struct {
	value     string
	expiresAt time.Time
}

type bootstrapRequest struct {
	Token string `json:"token"`
}

type rotateIPRequest struct {
	Token       string `json:"token"`
	DownSeconds int    `json:"down_seconds"`
}

type rotateIPResponse struct {
	Status            string `json:"status"`
	Egress            string `json:"egress"`
	DownSeconds       int    `json:"down_seconds"`
	Message           string `json:"message,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

const rotateDownSecondsMax = 60

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
	return &Server{
		store:           store,
		tokenLeases:     map[string]tokenLease{},
		tokenLeaseTTL:   tokenLeaseTTLFromEnv(),
		rotateLocks:     map[string]rotateLock{},
		rotateLockExtra: rotateLockExtraFromEnv(),
		triggerRotateIP: triggerAndroidRotateIP,
		carrierCache:    map[string]carrierCacheEntry{},
		carrierCacheTTL: carrierCacheTTLFromEnv(),
		carrierProbe:    currentAndroidCarrier,
	}
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
	src := clientIP(r)
	if !s.claimToken(req.Token, src, time.Now()) {
		log.Printf("bootstrap 拒绝 src=%s token=%q client=%s reason=token_in_use", src, maskToken(req.Token), record.ClientName)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "token_in_use"})
		return
	}

	log.Printf("bootstrap 通过 src=%s token=%q client=%s egress=%s", src, maskToken(req.Token), record.ClientName, record.Egress.Name)
	writeJSON(w, http.StatusOK, bootstrapResponse{
		Client:     clientResponse{Name: record.ClientName},
		Hub:        record.Hub,
		Egress:     s.egressWithCachedCarrierName(record.Egress),
		LocalProxy: record.LocalProxy,
		WireGuard:  record.WireGuard,
	})
}

func (s *Server) egressWithCachedCarrierName(egress Egress) Egress {
	carrier := s.cachedAndroidCarrier(egress.ManagementAddr, time.Now())
	if carrier != "" {
		egress.DisplayName = carrier
	}
	return egress
}

func (s *Server) cachedAndroidCarrier(managementAddr string, now time.Time) string {
	managementAddr = strings.TrimSpace(managementAddr)
	if managementAddr == "" || s == nil || s.carrierCacheTTL <= 0 {
		return ""
	}

	s.carrierCacheMu.Lock()
	defer s.carrierCacheMu.Unlock()

	if s.carrierCache == nil {
		s.carrierCache = map[string]carrierCacheEntry{}
	}
	if cached, ok := s.carrierCache[managementAddr]; ok && now.Before(cached.expiresAt) {
		return cached.value
	}

	probe := s.carrierProbe
	if probe == nil {
		probe = currentAndroidCarrier
	}
	carrier := probe(managementAddr)
	s.carrierCache[managementAddr] = carrierCacheEntry{
		value:     carrier,
		expiresAt: now.Add(s.carrierCacheTTL),
	}
	return carrier
}

func currentAndroidCarrier(managementAddr string) string {
	keyPath := androidControlKeyPath()
	if _, err := os.Stat(keyPath); err != nil {
		return ""
	}
	host, port := splitHostPortDefault(managementAddr, "2022")
	if host == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh",
		"-i", keyPath,
		"-p", port,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=1",
		"-o", "StrictHostKeyChecking="+androidControlHostKeyPolicy(),
		"-o", "UserKnownHostsFile="+androidControlKnownHostsPath(),
		"root@"+host,
		"getprop gsm.operator.alpha; getprop gsm.sim.operator.alpha",
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if carrier := firstCSVValue(line); carrier != "" {
			return carrier
		}
	}
	return ""
}

func androidControlKeyPath() string {
	if value := strings.TrimSpace(os.Getenv("ZHHUB_ANDROID_CONTROL_KEY")); value != "" {
		return value
	}
	return "/root/.ssh/zhandroid_control_hub"
}

func androidControlKnownHostsPath() string {
	if value := strings.TrimSpace(os.Getenv("ZHHUB_ANDROID_CONTROL_KNOWN_HOSTS")); value != "" {
		return value
	}
	return "/root/.ssh/zhandroid_control_known_hosts"
}

func androidControlHostKeyPolicy() string {
	if value := strings.TrimSpace(os.Getenv("ZHHUB_ANDROID_CONTROL_HOST_KEY_POLICY")); value != "" {
		return value
	}
	return "accept-new"
}

func firstCSVValue(value string) string {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func (s *Server) claimToken(token string, sourceIP string, now time.Time) bool {
	token = strings.TrimSpace(token)
	sourceIP = strings.TrimSpace(sourceIP)
	if token == "" || sourceIP == "" || s.tokenLeaseTTL <= 0 {
		return true
	}

	s.tokenLeasesMu.Lock()
	defer s.tokenLeasesMu.Unlock()

	lease, ok := s.tokenLeases[token]
	if ok && lease.sourceIP != sourceIP && now.Sub(lease.seenAt) < s.tokenLeaseTTL {
		return false
	}
	s.tokenLeases[token] = tokenLease{sourceIP: sourceIP, seenAt: now}
	return true
}

func tokenLeaseTTLFromEnv() time.Duration {
	text := strings.TrimSpace(os.Getenv("ZHHUB_TOKEN_LEASE_SECONDS"))
	if text == "" {
		return 30 * time.Second
	}
	seconds, err := strconv.Atoi(text)
	if err != nil || seconds < 0 {
		log.Printf("ZHHUB_TOKEN_LEASE_SECONDS 无效: %q, 使用默认 30s", text)
		return 30 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func rotateLockExtraFromEnv() time.Duration {
	text := strings.TrimSpace(os.Getenv("ZHHUB_ROTATE_LOCK_EXTRA_SECONDS"))
	if text == "" {
		return 45 * time.Second
	}
	seconds, err := strconv.Atoi(text)
	if err != nil || seconds < 0 {
		log.Printf("ZHHUB_ROTATE_LOCK_EXTRA_SECONDS 无效: %q, 使用默认 45s", text)
		return 45 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func carrierCacheTTLFromEnv() time.Duration {
	text := strings.TrimSpace(os.Getenv("ZHHUB_ANDROID_CARRIER_CACHE_SECONDS"))
	if text == "" {
		return 5 * time.Minute
	}
	seconds, err := strconv.Atoi(text)
	if err != nil || seconds < 0 {
		log.Printf("ZHHUB_ANDROID_CARRIER_CACHE_SECONDS 无效: %q, 使用默认 300s", text)
		return 5 * time.Minute
	}
	return time.Duration(seconds) * time.Second
}

func (s *Server) RotateIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}

	var req rotateIPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	if req.DownSeconds == 0 {
		req.DownSeconds = 8
	}
	if req.DownSeconds < 1 || req.DownSeconds > rotateDownSecondsMax {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_down_seconds"})
		return
	}

	record, ok := s.store.Resolve(req.Token, time.Now())
	if !ok {
		log.Printf("rotate-ip 拒绝 src=%s token=%q reason=invalid_token", clientIP(r), maskToken(req.Token))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	if record.Egress.Name != "jp-android-01" {
		log.Printf("rotate-ip 拒绝 src=%s token=%q client=%s egress=%s reason=unsupported_egress", clientIP(r), maskToken(req.Token), record.ClientName, record.Egress.Name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_egress"})
		return
	}

	lock, retryAfterSeconds, ok := s.tryBeginRotate(record.Egress.Name, req.DownSeconds, time.Now())
	if !ok {
		log.Printf("rotate-ip 跳过 src=%s token=%q client=%s egress=%s reason=busy retry_after_seconds=%d", clientIP(r), maskToken(req.Token), record.ClientName, record.Egress.Name, retryAfterSeconds)
		writeJSON(w, http.StatusConflict, rotateIPResponse{
			Status:            "busy",
			Egress:            record.Egress.Name,
			DownSeconds:       req.DownSeconds,
			Message:           "换 IP 正在进行中，请稍后再试",
			RetryAfterSeconds: retryAfterSeconds,
		})
		return
	}

	trigger := s.triggerRotateIP
	if trigger == nil {
		trigger = triggerAndroidRotateIP
	}
	if err := trigger(record.Egress.ManagementAddr, req.DownSeconds); err != nil {
		s.releaseRotate(record.Egress.Name, lock)
		log.Printf("rotate-ip 失败 src=%s token=%q client=%s egress=%s err=%v", clientIP(r), maskToken(req.Token), record.ClientName, record.Egress.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "control_failed"})
		return
	}

	log.Printf("rotate-ip 触发 src=%s token=%q client=%s egress=%s down_seconds=%d lock_until=%s", clientIP(r), maskToken(req.Token), record.ClientName, record.Egress.Name, req.DownSeconds, lock.until.Format(time.RFC3339))
	writeJSON(w, http.StatusOK, rotateIPResponse{
		Status:      "triggered",
		Egress:      record.Egress.Name,
		DownSeconds: req.DownSeconds,
	})
}

func (s *Server) tryBeginRotate(egress string, downSeconds int, now time.Time) (rotateLock, int, bool) {
	egress = strings.TrimSpace(egress)
	if egress == "" {
		egress = "default"
	}
	if downSeconds < 1 {
		downSeconds = 1
	}
	extra := s.rotateLockExtra
	if extra < 0 {
		extra = 0
	}
	hold := time.Duration(downSeconds)*time.Second + extra
	if hold <= 0 {
		hold = time.Second
	}

	s.rotateLocksMu.Lock()
	defer s.rotateLocksMu.Unlock()

	if s.rotateLocks == nil {
		s.rotateLocks = map[string]rotateLock{}
	}
	if current, ok := s.rotateLocks[egress]; ok && now.Before(current.until) {
		retryAfter := int(current.until.Sub(now).Round(time.Second) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		return current, retryAfter, false
	}

	lock := rotateLock{startedAt: now, until: now.Add(hold)}
	s.rotateLocks[egress] = lock
	return lock, 0, true
}

func (s *Server) releaseRotate(egress string, lock rotateLock) {
	s.rotateLocksMu.Lock()
	defer s.rotateLocksMu.Unlock()

	current, ok := s.rotateLocks[egress]
	if !ok {
		return
	}
	if current.startedAt.Equal(lock.startedAt) && current.until.Equal(lock.until) {
		delete(s.rotateLocks, egress)
	}
}

func triggerAndroidRotateIP(managementAddr string, downSeconds int) error {
	host, port := splitHostPortDefault(managementAddr, "2022")
	if host == "" {
		host = "10.66.0.101"
	}
	keyPath := androidControlKeyPath()
	if _, err := os.Stat(keyPath); err != nil {
		return fmt.Errorf("control key unavailable: %w", err)
	}

	remote := fmt.Sprintf("sh /data/adb/zhandroid/rotate-ip.sh %d", downSeconds)
	cmd := exec.Command("ssh",
		"-i", keyPath,
		"-p", port,
		"-o", "StrictHostKeyChecking="+androidControlHostKeyPolicy(),
		"-o", "UserKnownHostsFile="+androidControlKnownHostsPath(),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"root@"+host,
		remote,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text != "" {
			return fmt.Errorf("%w: %s", err, text)
		}
		return err
	}
	return nil
}

func splitHostPortDefault(value string, defaultPort string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", defaultPort
	}
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		return host, port
	}
	if strings.Contains(value, ":") {
		return value, defaultPort
	}
	if _, err := strconv.Atoi(defaultPort); err != nil {
		return value, "2022"
	}
	return value, defaultPort
}

// clientIP only trusts X-Forwarded-For from local/private reverse proxies.
// Public clients connect directly today, so a client-supplied XFF must not
// affect token lease ownership.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.Trim(host, "[]"))
	if remoteIP != nil && trustedForwarderIP(remoteIP) {
		xff := r.Header.Get("X-Forwarded-For")
		if i := indexComma(xff); i >= 0 {
			xff = xff[:i]
		}
		if forwarded := trimSpace(xff); forwarded != "" {
			return forwarded
		}
	}
	return host
}

func trustedForwarderIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate()
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
