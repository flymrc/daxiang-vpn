package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"zongheng-vpn/hub/internal/httpboundary"
	"zongheng-vpn/hub/internal/processbudget"
)

type Server struct {
	store             *TokenStore
	tokenLeases       map[string]tokenLease
	tokenLeasesMu     sync.Mutex
	tokenLeaseVersion uint64
	tokenLeaseTTL     time.Duration
	rotateLocks       map[string]rotateLock
	rotateLocksMu     sync.Mutex
	rotateLockExtra   time.Duration
	triggerRotateIP   func(context.Context, string, int) error
	carrierCache      map[string]carrierCacheEntry
	carrierCacheMu    sync.Mutex
	carrierCacheTTL   time.Duration
	carrierProbe      func(context.Context, string) string
	auditSink         func(AuditEvent)
	auditMu           sync.Mutex
	auditGate         sync.RWMutex
	auditOwner        uint64
	auditStopped      bool
	auditOnDetach     func(string)
	applyClientPeer   func(context.Context, string, string) error
	httpAdmission     *httpboundary.Admission
	resourcesOnce     sync.Once
	wgRunner          *processbudget.Runner
	sshRunner         *processbudget.Runner
	carrierFlights    map[string]*carrierFlight
}
type carrierFlight struct {
	done  chan struct{}
	value string
}

type tokenLease struct {
	sourceIP string
	seenAt   time.Time
	version  uint64
	node     *leaseClaimNode
}

// Pending claims share their predecessors so an out-of-order rejection cannot
// restore a lease that another request has already rejected. All node fields
// are protected by tokenLeasesMu; committed nodes discard their history.
type leaseClaimNode struct {
	previous  tokenLease
	existed   bool
	failed    bool
	committed bool
}
type tokenClaim struct {
	token   string
	current tokenLease
	changed bool
}

type rotateLock struct {
	startedAt time.Time
	until     time.Time
	unknown   bool
	inflight  bool
}

type carrierCacheEntry struct {
	value     string
	expiresAt time.Time
}

type AuditEvent struct {
	OccurredAt           time.Time
	Actor                string
	SourceIP             string
	EventType            string
	Target               string
	DetailJSON           string
	Result               string
	ErrorCode            string
	MigrationObservation *MigrationObservation
}

type MigrationObservation struct {
	TokenID            string
	OccurredAt         time.Time
	ClientProduct      string
	ClientVersion      string
	ProtocolVersion    int
	Ingress            string
	KeyMode            string
	PrivateKeyReturned bool
	MigrationClass     string
}

type ClientIngress string

const (
	ClientIngressCompat       ClientIngress = "compat"
	ClientIngressTrustedProxy ClientIngress = "trusted_proxy"
)

type TokenLeaseSnapshot struct {
	Token     string
	SourceIP  string
	SeenAt    time.Time
	ExpiresAt time.Time
}

type RotateLockSnapshot struct {
	Egress    string
	StartedAt time.Time
	Until     time.Time
	Unknown   bool
}

type RotateEgressResult struct {
	Status            string
	Egress            string
	DownSeconds       int
	RetryAfterSeconds int
	LockUntil         time.Time
}

var (
	ErrInvalidDownSeconds = errors.New("invalid_down_seconds")
	ErrUnsupportedEgress  = errors.New("unsupported_egress")
	ErrRotateBusy         = errors.New("rotate_busy")
)

type bootstrapRequest struct {
	Token              string `json:"token"`
	WireGuardPublicKey string `json:"wireguard_public_key"`
	ClientProduct      string `json:"client_product"`
	ClientVersion      string `json:"client_version"`
	ProtocolVersion    int    `json:"protocol_version"`
}

type rotateIPRequest struct {
	Token           string `json:"token"`
	DownSeconds     int    `json:"down_seconds"`
	ClientProduct   string `json:"client_product"`
	ClientVersion   string `json:"client_version"`
	ProtocolVersion int    `json:"protocol_version"`
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
	s := &Server{
		store:           store,
		tokenLeases:     map[string]tokenLease{},
		tokenLeaseTTL:   tokenLeaseTTLFromEnv(),
		rotateLocks:     map[string]rotateLock{},
		rotateLockExtra: rotateLockExtraFromEnv(),
		carrierCache:    map[string]carrierCacheEntry{},
		carrierCacheTTL: carrierCacheTTLFromEnv(),
	}
	s.ensureResources()
	return s
}

func (s *Server) ensureResources() {
	s.resourcesOnce.Do(func() {
		var err error
		if s.httpAdmission == nil {
			s.httpAdmission, err = httpboundary.NewAdmission(httpboundary.DefaultAdmissionConfig())
			if err != nil {
				panic("invalid internal HTTP admission")
			}
		}
		s.wgRunner = processbudget.New(1)
		s.sshRunner = processbudget.New(2)
		if s.carrierCache == nil {
			s.carrierCache = map[string]carrierCacheEntry{}
		}
		s.carrierFlights = map[string]*carrierFlight{}
	})
}
func (s *Server) HTTPAdmission() *httpboundary.Admission { s.ensureResources(); return s.httpAdmission }

func (s *Server) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) SetAuditSink(sink func(AuditEvent)) {
	s.RegisterAuditSink(sink, nil)
}

// RegisterAuditSink swaps generations only after all owned HTTP operations and
// callbacks drain. A stale owner cannot detach its successor. Closing capture
// gates future client mutations until a new prearmed observer is registered.
func (s *Server) RegisterAuditSink(sink func(AuditEvent), detached func(string)) func() bool {
	s.auditGate.Lock()
	s.auditMu.Lock()
	if s.auditOnDetach != nil {
		s.auditOnDetach("sink_replaced")
	}
	s.auditOwner++
	owner := s.auditOwner
	s.auditSink, s.auditOnDetach, s.auditStopped = sink, detached, false
	s.auditMu.Unlock()
	s.auditGate.Unlock()
	return func() bool {
		s.auditGate.Lock()
		defer s.auditGate.Unlock()
		s.auditMu.Lock()
		defer s.auditMu.Unlock()
		if s.auditOwner != owner {
			return false
		}
		s.auditStopped = true
		if s.auditOnDetach != nil {
			s.auditOnDetach("observer_closed")
		}
		s.auditSink, s.auditOnDetach = nil, nil
		return true
	}
}

func (s *Server) observedHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.auditGate.TryRLock() {
			writeJSON(w, 503, map[string]string{"error": "observer_unavailable"})
			return
		}
		defer s.auditGate.RUnlock()
		if s.auditStopped {
			writeJSON(w, 503, map[string]string{"error": "observer_unavailable"})
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func (s *Server) SetRotateTrigger(trigger func(context.Context, string, int) error) {
	s.triggerRotateIP = trigger
}

func (s *Server) BootstrapHandler(ingress ClientIngress) http.HandlerFunc {
	if ingress != ClientIngressCompat && ingress != ClientIngressTrustedProxy {
		panic("invalid client ingress: " + string(ingress))
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.bootstrap(w, r, ingress)
	})
	policy := httpboundary.Direct
	if ingress == ClientIngressTrustedProxy {
		policy = httpboundary.LoopbackProxy
	}
	return s.HTTPAdmission().Middleware(s.observedHandler(handler), policy).ServeHTTP
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request, ingress ClientIngress) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	if ctx.Err() != nil {
		writeJSON(w, 503, map[string]string{"error": "request_cancelled"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	src, err := clientIPForIngress(r, ingress)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}

	var req bootstrapRequest
	if err := httpboundary.DecodeJSON(w, r, &req); err != nil {
		status, code := httpboundary.DecodeError(err)
		writeJSON(w, status, map[string]string{"error": code})
		return
	}

	record, ok := s.store.Resolve(req.Token, time.Now())
	if !ok {
		log.Printf("bootstrap 拒绝 src=%s token=%q reason=invalid_token", src, maskToken(req.Token))
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      maskToken(req.Token),
			SourceIP:   src,
			EventType:  "client.bootstrap",
			Target:     "token:" + maskToken(req.Token),
			DetailJSON: `{"reason":"invalid_token"}`,
			Result:     "denied",
			ErrorCode:  "invalid_token",
		})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	clientPublicKey := strings.TrimSpace(req.WireGuardPublicKey)
	failureResult := "error"
	defer func() {
		if failureResult == "" {
			return
		}
		occurred := time.Now()
		keyMode := "server_legacy"
		if clientPublicKey != "" {
			keyMode = "client_generated"
		}
		s.audit(AuditEvent{OccurredAt: occurred, Actor: record.ClientName, SourceIP: src, EventType: "client.bootstrap", Result: failureResult, ErrorCode: "bootstrap_attempt_failed", MigrationObservation: &MigrationObservation{TokenID: TokenID(req.Token), OccurredAt: occurred, ClientProduct: strings.TrimSpace(req.ClientProduct), ClientVersion: strings.TrimSpace(req.ClientVersion), ProtocolVersion: req.ProtocolVersion, Ingress: string(ingress), KeyMode: keyMode, MigrationClass: "unknown"}})
	}()
	if clientPublicKey != "" && validateWireGuardPublicKey(clientPublicKey) != nil {
		failureResult = "denied"
		s.audit(AuditEvent{OccurredAt: time.Now(), Actor: record.ClientName, SourceIP: src, EventType: "client.bootstrap", Target: "token:" + maskToken(req.Token), DetailJSON: `{"reason":"invalid_wireguard_public_key"}`, Result: "denied", ErrorCode: "invalid_wireguard_public_key"})
		writeJSON(w, 400, map[string]string{"error": "invalid_wireguard_public_key"})
		return
	}
	if ctx.Err() != nil {
		writeJSON(w, 503, map[string]string{"error": "request_cancelled"})
		return
	}
	claimed, claim := s.claimTokenReceipt(req.Token, src, time.Now())
	if !claimed {
		failureResult = "denied"
		log.Printf("bootstrap 拒绝 src=%s token=%q client=%s reason=token_in_use", src, maskToken(req.Token), record.ClientName)
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      record.ClientName,
			SourceIP:   src,
			EventType:  "client.bootstrap",
			Target:     "token:" + maskToken(req.Token),
			DetailJSON: `{"reason":"token_in_use"}`,
			Result:     "denied",
			ErrorCode:  "token_in_use",
		})
		writeJSON(w, http.StatusConflict, map[string]string{"error": "token_in_use"})
		return
	}
	if clientPublicKey != "" {
		applyPeer := s.applyClientPeer
		if applyPeer == nil {
			applyPeer = s.applyWireGuardPeer
		}
		if err := applyPeer(ctx, clientPublicKey, record.WireGuard.Address); err != nil {
			if processbudget.BeforeStart(err) {
				s.rollbackTokenClaim(claim)
			} else {
				s.commitTokenClaim(claim)
			}
			log.Printf("bootstrap 应用客户端 peer 失败 src=%s token=%q client=%s code=wireguard_peer_apply_failed", src, maskToken(req.Token), record.ClientName)
			s.audit(AuditEvent{
				OccurredAt: time.Now(),
				Actor:      record.ClientName,
				SourceIP:   src,
				EventType:  "client.bootstrap",
				Target:     "token:" + maskToken(req.Token),
				DetailJSON: `{"reason":"wireguard_peer_apply_failed"}`,
				Result:     "error",
				ErrorCode:  "wireguard_peer_apply_failed",
			})
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "wireguard_peer_apply_failed"})
			return
		}
		record.WireGuard.PrivateKey = ""
		record.WireGuard.PublicKey = clientPublicKey
	}
	s.commitTokenClaim(claim)

	occurredAt := time.Now()
	privateKeyReturned := strings.TrimSpace(record.WireGuard.PrivateKey) != ""
	classification := classifyBootstrapSecurity(
		ingress,
		clientPublicKey,
		req.ClientProduct,
		req.ClientVersion,
		req.ProtocolVersion,
		privateKeyReturned,
	)
	observation := &MigrationObservation{
		TokenID:            TokenID(req.Token),
		OccurredAt:         occurredAt,
		ClientProduct:      strings.TrimSpace(req.ClientProduct),
		ClientVersion:      strings.TrimSpace(req.ClientVersion),
		ProtocolVersion:    req.ProtocolVersion,
		Ingress:            classification.Ingress,
		KeyMode:            classification.KeyMode,
		PrivateKeyReturned: classification.PrivateKeyReturned,
		MigrationClass:     classification.MigrationClass,
	}
	log.Printf("bootstrap 通过 src=%s token=%q client=%s egress=%s", src, maskToken(req.Token), record.ClientName, record.Egress.Name)
	failureResult = ""
	s.audit(AuditEvent{
		OccurredAt:           occurredAt,
		Actor:                record.ClientName,
		SourceIP:             src,
		EventType:            "client.bootstrap",
		Target:               "token:" + maskToken(req.Token),
		DetailJSON:           bootstrapAuditDetailJSON(record.Egress.Name, observation),
		Result:               "ok",
		MigrationObservation: observation,
	})
	writeJSON(w, http.StatusOK, bootstrapResponse{
		Client:     clientResponse{Name: record.ClientName},
		Hub:        record.Hub,
		Egress:     s.egressWithCachedCarrierName(ctx, record.Egress),
		LocalProxy: record.LocalProxy,
		WireGuard:  record.WireGuard,
	})
}

type bootstrapSecurityClassification struct {
	Ingress            string
	KeyMode            string
	PrivateKeyReturned bool
	MigrationClass     string
}

func classifyBootstrapSecurity(ingress ClientIngress, publicKey string, product string, version string, protocolVersion int, privateKeyReturned bool) bootstrapSecurityClassification {
	keyMode := "server_legacy"
	if strings.TrimSpace(publicKey) != "" {
		keyMode = "client_generated"
	}
	classification := bootstrapSecurityClassification{
		Ingress:            string(ingress),
		KeyMode:            keyMode,
		PrivateKeyReturned: privateKeyReturned,
		MigrationClass:     "legacy",
	}
	if ingress != ClientIngressTrustedProxy || keyMode != "client_generated" || privateKeyReturned {
		return classification
	}
	if !validClientMetadata(product, version, protocolVersion) {
		classification.MigrationClass = "unknown"
		return classification
	}
	classification.MigrationClass = "secure_bootstrap"
	return classification
}

func validClientMetadata(product string, version string, protocolVersion int) bool {
	switch strings.TrimSpace(product) {
	case "cli", "desktop-gui", "python-sdk":
	default:
		return false
	}
	return protocolVersion == 2 && validReleaseVersion(version)
}

func bootstrapAuditDetailJSON(egress string, observation *MigrationObservation) string {
	detail := struct {
		Egress             string `json:"egress"`
		ClientProduct      string `json:"client_product"`
		ClientVersion      string `json:"client_version"`
		ProtocolVersion    int    `json:"protocol_version"`
		Ingress            string `json:"ingress"`
		KeyMode            string `json:"key_mode"`
		PrivateKeyReturned bool   `json:"private_key_returned"`
		MigrationClass     string `json:"migration_class"`
	}{
		Egress:             egress,
		ClientProduct:      observation.ClientProduct,
		ClientVersion:      observation.ClientVersion,
		ProtocolVersion:    observation.ProtocolVersion,
		Ingress:            observation.Ingress,
		KeyMode:            observation.KeyMode,
		PrivateKeyReturned: observation.PrivateKeyReturned,
		MigrationClass:     observation.MigrationClass,
	}
	data, err := json.Marshal(detail)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func validateWireGuardPublicKey(publicKey string) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil {
		return err
	}
	if len(raw) != 32 {
		return fmt.Errorf("public key length = %d", len(raw))
	}
	return nil
}

func (s *Server) applyWireGuardPeer(ctx context.Context, publicKey string, address string) error {
	s.ensureResources()
	allowedIP, err := peerAllowedIP(address)
	if err != nil {
		return processbudget.Reject(processbudget.Invalid)
	}
	iface := strings.TrimSpace(os.Getenv("ZHHUB_WG_INTERFACE"))
	if iface == "" {
		iface = "wg0"
	}
	wgBin := strings.TrimSpace(os.Getenv("ZHHUB_WG_BIN"))
	if wgBin == "" {
		wgBin = "wg"
	}
	_, err = s.wgRunner.Run(ctx, processbudget.Spec{Executable: wgBin, Args: []string{"set", iface, "peer", publicKey, "allowed-ips", allowedIP}, Timeout: 3 * time.Second, OutputLimit: 4096})
	return err
}

func peerAllowedIP(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", errors.New("wireguard address is empty")
	}
	if ip, _, err := net.ParseCIDR(address); err == nil {
		if ip.To4() != nil {
			return ip.String() + "/32", nil
		}
		return ip.String() + "/128", nil
	}
	ip := net.ParseIP(address)
	if ip == nil {
		return "", fmt.Errorf("invalid wireguard address: %s", address)
	}
	if ip.To4() != nil {
		return ip.String() + "/32", nil
	}
	return ip.String() + "/128", nil
}

func (s *Server) egressWithCachedCarrierName(ctx context.Context, egress Egress) Egress {
	carrier := s.cachedAndroidCarrier(ctx, egress.ManagementAddr, time.Now())
	if carrier != "" {
		egress.DisplayName = carrier
	}
	return egress
}

func (s *Server) cachedAndroidCarrier(ctx context.Context, managementAddr string, now time.Time) string {
	managementAddr = strings.TrimSpace(managementAddr)
	if managementAddr == "" || s == nil || s.carrierCacheTTL <= 0 || ctx.Err() != nil {
		return ""
	}

	s.ensureResources()
	s.carrierCacheMu.Lock()
	if cached, ok := s.carrierCache[managementAddr]; ok && now.Before(cached.expiresAt) {
		s.carrierCacheMu.Unlock()
		return cached.value
	}
	if flight := s.carrierFlights[managementAddr]; flight != nil {
		s.carrierCacheMu.Unlock()
		select {
		case <-ctx.Done():
			return ""
		case <-flight.done:
			if ctx.Err() != nil {
				return ""
			}
			return flight.value
		}
	}
	for key, entry := range s.carrierCache {
		if !now.Before(entry.expiresAt) {
			delete(s.carrierCache, key)
		}
	}
	if len(s.carrierFlights) >= 16 || len(s.carrierCache)+len(s.carrierFlights) >= 64 {
		s.carrierCacheMu.Unlock()
		return ""
	}
	flight := &carrierFlight{done: make(chan struct{})}
	s.carrierFlights[managementAddr] = flight
	s.carrierCacheMu.Unlock()

	probe := s.carrierProbe
	if probe == nil {
		probe = s.currentAndroidCarrier
	}
	bounded, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	carrier := probe(bounded, managementAddr)
	cancel()
	if ctx.Err() != nil {
		carrier = ""
	}
	s.carrierCacheMu.Lock()
	defer s.carrierCacheMu.Unlock()
	flight.value = carrier
	delete(s.carrierFlights, managementAddr)
	close(flight.done)
	s.carrierCache[managementAddr] = carrierCacheEntry{
		value:     carrier,
		expiresAt: now.Add(s.carrierCacheTTL),
	}
	return carrier
}

func (s *Server) currentAndroidCarrier(ctx context.Context, managementAddr string) string {
	s.ensureResources()
	keyPath := androidControlKeyPath()
	if _, err := os.Stat(keyPath); err != nil {
		return ""
	}
	host, port := splitHostPortDefault(managementAddr, "2022")
	if host == "" {
		return ""
	}
	result, err := s.sshRunner.Run(ctx, processbudget.Spec{Executable: "ssh", Timeout: 1500 * time.Millisecond, OutputLimit: 4096, Args: []string{
		"-i", keyPath,
		"-p", port,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=1",
		"-o", "StrictHostKeyChecking=" + androidControlHostKeyPolicy(),
		"-o", "UserKnownHostsFile=" + androidControlKnownHostsPath(),
		"root@" + host,
		"getprop gsm.operator.alpha; getprop gsm.sim.operator.alpha",
	}})
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
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
	ok, claim := s.claimTokenReceipt(token, sourceIP, now)
	if ok {
		s.commitTokenClaim(claim)
	}
	return ok
}
func (s *Server) claimTokenReceipt(token string, sourceIP string, now time.Time) (bool, tokenClaim) {
	token = strings.TrimSpace(token)
	sourceIP = strings.TrimSpace(sourceIP)
	if token == "" || sourceIP == "" || s.tokenLeaseTTL <= 0 {
		return true, tokenClaim{}
	}

	s.tokenLeasesMu.Lock()
	defer s.tokenLeasesMu.Unlock()

	if s.tokenLeases == nil {
		s.tokenLeases = map[string]tokenLease{}
	}
	lease, ok := s.tokenLeases[token]
	if ok && lease.sourceIP != sourceIP && now.Sub(lease.seenAt) < s.tokenLeaseTTL {
		return false, tokenClaim{}
	}
	s.tokenLeaseVersion++
	node := &leaseClaimNode{previous: lease, existed: ok}
	current := tokenLease{sourceIP: sourceIP, seenAt: now, version: s.tokenLeaseVersion, node: node}
	s.tokenLeases[token] = current
	return true, tokenClaim{token: token, current: current, changed: true}
}
func (s *Server) rollbackTokenClaim(claim tokenClaim) {
	if !claim.changed || claim.current.node == nil {
		return
	}
	s.tokenLeasesMu.Lock()
	defer s.tokenLeasesMu.Unlock()
	node := claim.current.node
	if node.committed || node.failed {
		return
	}
	node.failed = true
	if current, ok := s.tokenLeases[claim.token]; ok && current.version == claim.current.version {
		previous, existed := node.previous, node.existed
		for existed && previous.node != nil && previous.node.failed {
			previous, existed = previous.node.previous, previous.node.existed
		}
		if existed {
			if previous.node != nil && previous.node.committed {
				previous.node = nil
			}
			s.tokenLeases[claim.token] = previous
		} else {
			delete(s.tokenLeases, claim.token)
		}
	}
}
func (s *Server) commitTokenClaim(claim tokenClaim) {
	if !claim.changed || claim.current.node == nil {
		return
	}
	s.tokenLeasesMu.Lock()
	defer s.tokenLeasesMu.Unlock()
	node := claim.current.node
	if node.failed || node.committed {
		return
	}
	node.committed = true
	node.previous = tokenLease{}
	node.existed = false
	if current, ok := s.tokenLeases[claim.token]; ok && current.version == claim.current.version {
		current.node = nil
		s.tokenLeases[claim.token] = current
	}
}

func (s *Server) TokenLeasesSnapshot(now time.Time) []TokenLeaseSnapshot {
	if s == nil {
		return nil
	}
	s.tokenLeasesMu.Lock()
	defer s.tokenLeasesMu.Unlock()

	leases := make([]TokenLeaseSnapshot, 0, len(s.tokenLeases))
	for token, lease := range s.tokenLeases {
		expiresAt := time.Time{}
		if s.tokenLeaseTTL > 0 {
			expiresAt = lease.seenAt.Add(s.tokenLeaseTTL)
			if now.After(expiresAt) {
				continue
			}
		}
		leases = append(leases, TokenLeaseSnapshot{
			Token:     token,
			SourceIP:  lease.sourceIP,
			SeenAt:    lease.seenAt,
			ExpiresAt: expiresAt,
		})
	}
	return leases
}

func (s *Server) RotateLocksSnapshot(now time.Time) []RotateLockSnapshot {
	if s == nil {
		return nil
	}
	s.rotateLocksMu.Lock()
	defer s.rotateLocksMu.Unlock()

	locks := make([]RotateLockSnapshot, 0, len(s.rotateLocks))
	for egress, lock := range s.rotateLocks {
		if lock.unknown || lock.inflight || now.Before(lock.until) {
			locks = append(locks, RotateLockSnapshot{
				Egress:    egress,
				StartedAt: lock.startedAt,
				Until:     lock.until,
				Unknown:   lock.unknown,
			})
		}
	}
	return locks
}

func tokenLeaseTTLFromEnv() time.Duration {
	return boundedEnvSeconds("ZHHUB_TOKEN_LEASE_SECONDS", 30*time.Second)
}

func rotateLockExtraFromEnv() time.Duration {
	return boundedEnvSeconds("ZHHUB_ROTATE_LOCK_EXTRA_SECONDS", 45*time.Second)
}

func carrierCacheTTLFromEnv() time.Duration {
	return boundedEnvSeconds("ZHHUB_ANDROID_CARRIER_CACHE_SECONDS", 5*time.Minute)
}

// Bound configuration before multiplying; zero retains the existing disable /
// no-extra-delay semantics. Invalid values never reach operational logs.
func boundedEnvSeconds(key string, fallback time.Duration) time.Duration {
	text := strings.TrimSpace(os.Getenv(key))
	if text == "" {
		return fallback
	}
	seconds, err := strconv.ParseInt(text, 10, 64)
	if err != nil || seconds < 0 || seconds > 24*60*60 {
		log.Printf("%s 无效, 使用默认秒数 %d", key, int64(fallback/time.Second))
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func (s *Server) RotateIP(w http.ResponseWriter, r *http.Request) {
	s.RotateIPHandler(ClientIngressCompat)(w, r)
}

func (s *Server) RotateIPHandler(ingress ClientIngress) http.HandlerFunc {
	if ingress != ClientIngressCompat && ingress != ClientIngressTrustedProxy {
		panic("invalid client ingress: " + string(ingress))
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.rotateIP(w, r, ingress)
	})
	policy := httpboundary.Direct
	if ingress == ClientIngressTrustedProxy {
		policy = httpboundary.LoopbackProxy
	}
	return s.HTTPAdmission().Middleware(s.observedHandler(handler), policy).ServeHTTP
}

func (s *Server) rotateIP(w http.ResponseWriter, r *http.Request, ingress ClientIngress) {
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if ctx.Err() != nil {
		writeJSON(w, 503, map[string]string{"error": "request_cancelled"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	src, err := clientIPForIngress(r, ingress)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}

	var req rotateIPRequest
	if err := httpboundary.DecodeJSON(w, r, &req); err != nil {
		status, code := httpboundary.DecodeError(err)
		writeJSON(w, status, map[string]string{"error": code})
		return
	}
	if req.DownSeconds == 0 {
		req.DownSeconds = 8
	}
	if req.DownSeconds < 1 || req.DownSeconds > rotateDownSecondsMax {
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      maskToken(req.Token),
			SourceIP:   src,
			EventType:  "client.rotate_ip",
			Target:     "egress:unknown",
			DetailJSON: fmt.Sprintf(`{"down_seconds":%d}`, req.DownSeconds),
			Result:     "denied",
			ErrorCode:  "invalid_down_seconds",
		})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_down_seconds"})
		return
	}

	record, ok := s.store.Resolve(req.Token, time.Now())
	if !ok {
		log.Printf("rotate-ip 拒绝 src=%s token=%q reason=invalid_token", src, maskToken(req.Token))
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      maskToken(req.Token),
			SourceIP:   src,
			EventType:  "client.rotate_ip",
			Target:     "egress:unknown",
			DetailJSON: `{"reason":"invalid_token"}`,
			Result:     "denied",
			ErrorCode:  "invalid_token",
		})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	result, err := s.RotateEgress(ctx, record.Egress, req.DownSeconds)
	if errors.Is(err, ErrUnsupportedEgress) {
		log.Printf("rotate-ip 拒绝 src=%s token=%q client=%s egress=%s reason=unsupported_egress", src, maskToken(req.Token), record.ClientName, record.Egress.Name)
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      record.ClientName,
			SourceIP:   src,
			EventType:  "client.rotate_ip",
			Target:     "egress:" + record.Egress.Name,
			DetailJSON: `{"reason":"unsupported_egress"}`,
			Result:     "denied",
			ErrorCode:  "unsupported_egress",
		})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_egress"})
		return
	}
	if errors.Is(err, ErrRotateBusy) {
		log.Printf("rotate-ip 跳过 src=%s token=%q client=%s egress=%s reason=busy retry_after_seconds=%d", src, maskToken(req.Token), record.ClientName, record.Egress.Name, result.RetryAfterSeconds)
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      record.ClientName,
			SourceIP:   src,
			EventType:  "client.rotate_ip",
			Target:     "egress:" + record.Egress.Name,
			DetailJSON: fmt.Sprintf(`{"down_seconds":%d,"retry_after_seconds":%d}`, req.DownSeconds, result.RetryAfterSeconds),
			Result:     "busy",
			ErrorCode:  "rotate_busy",
		})
		writeJSON(w, http.StatusConflict, rotateIPResponse{
			Status:            "busy",
			Egress:            record.Egress.Name,
			DownSeconds:       req.DownSeconds,
			Message:           "换 IP 正在进行中，请稍后再试",
			RetryAfterSeconds: result.RetryAfterSeconds,
		})
		return
	}
	if err != nil {
		log.Printf("rotate-ip 失败 src=%s token=%q client=%s egress=%s code=control_failed", src, maskToken(req.Token), record.ClientName, record.Egress.Name)
		s.audit(AuditEvent{
			OccurredAt: time.Now(),
			Actor:      record.ClientName,
			SourceIP:   src,
			EventType:  "client.rotate_ip",
			Target:     "egress:" + record.Egress.Name,
			DetailJSON: fmt.Sprintf(`{"down_seconds":%d}`, req.DownSeconds),
			Result:     "error",
			ErrorCode:  "control_failed",
		})
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "control_failed"})
		return
	}

	log.Printf("rotate-ip 触发 src=%s token=%q client=%s egress=%s down_seconds=%d lock_until=%s", src, maskToken(req.Token), record.ClientName, record.Egress.Name, req.DownSeconds, result.LockUntil.Format(time.RFC3339))
	s.audit(AuditEvent{
		OccurredAt: time.Now(),
		Actor:      record.ClientName,
		SourceIP:   src,
		EventType:  "client.rotate_ip",
		Target:     "egress:" + record.Egress.Name,
		DetailJSON: fmt.Sprintf(`{"down_seconds":%d,"lock_until":%q}`, req.DownSeconds, result.LockUntil.Format(time.RFC3339)),
		Result:     "ok",
	})
	writeJSON(w, http.StatusOK, rotateIPResponse{
		Status:      "triggered",
		Egress:      record.Egress.Name,
		DownSeconds: req.DownSeconds,
	})
}

func (s *Server) RotateEgress(ctx context.Context, egress Egress, downSeconds int) (RotateEgressResult, error) {
	if ctx.Err() != nil {
		return RotateEgressResult{}, processbudget.Reject(processbudget.Cancelled)
	}
	bounded, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	if downSeconds == 0 {
		downSeconds = 8
	}
	if downSeconds < 1 || downSeconds > rotateDownSecondsMax {
		return RotateEgressResult{DownSeconds: downSeconds}, ErrInvalidDownSeconds
	}
	if egress.Name != "jp-android-01" {
		return RotateEgressResult{Egress: egress.Name, DownSeconds: downSeconds}, ErrUnsupportedEgress
	}

	lock, retryAfterSeconds, ok := s.tryBeginRotate(egress.Name, downSeconds, time.Now())
	if !ok {
		return RotateEgressResult{
			Status:            "busy",
			Egress:            egress.Name,
			DownSeconds:       downSeconds,
			RetryAfterSeconds: retryAfterSeconds,
			LockUntil:         lock.until,
		}, ErrRotateBusy
	}

	trigger := s.triggerRotateIP
	if trigger == nil {
		trigger = s.triggerAndroidRotateIP
	}
	if err := trigger(bounded, egress.ManagementAddr, downSeconds); err != nil {
		if processbudget.BeforeStart(err) {
			s.releaseRotate(egress.Name, lock)
		} else {
			s.finishRotate(egress.Name, lock, true)
		}
		return RotateEgressResult{
			Status:      "error",
			Egress:      egress.Name,
			DownSeconds: downSeconds,
		}, err
	}
	if bounded.Err() != nil {
		s.finishRotate(egress.Name, lock, true)
		return RotateEgressResult{}, &processbudget.Failure{Kind: processbudget.Cancelled, Started: true}
	}
	s.finishRotate(egress.Name, lock, false)

	return RotateEgressResult{
		Status:      "triggered",
		Egress:      egress.Name,
		DownSeconds: downSeconds,
		LockUntil:   lock.until,
	}, nil
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
	if current, ok := s.rotateLocks[egress]; ok && (current.unknown || current.inflight || now.Before(current.until)) {
		retryAfter := int(current.until.Sub(now).Round(time.Second) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		return current, retryAfter, false
	}

	lock := rotateLock{startedAt: now, until: now.Add(hold), inflight: true}
	s.rotateLocks[egress] = lock
	return lock, 0, true
}

func (s *Server) finishRotate(egress string, lock rotateLock, unknown bool) {
	s.rotateLocksMu.Lock()
	defer s.rotateLocksMu.Unlock()
	if current, ok := s.rotateLocks[egress]; ok && current.startedAt.Equal(lock.startedAt) && current.until.Equal(lock.until) {
		current.inflight = false
		current.unknown = unknown
		s.rotateLocks[egress] = current
	}
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

func (s *Server) triggerAndroidRotateIP(ctx context.Context, managementAddr string, downSeconds int) error {
	s.ensureResources()
	host, port := splitHostPortDefault(managementAddr, "2022")
	if host == "" {
		host = "10.66.0.101"
	}
	keyPath := androidControlKeyPath()
	if _, err := os.Stat(keyPath); err != nil {
		return processbudget.Reject(processbudget.Unavailable)
	}

	remote := fmt.Sprintf("sh /data/adb/zhandroid/rotate-ip.sh %d", downSeconds)
	_, err := s.sshRunner.Run(ctx, processbudget.Spec{Executable: "ssh", Timeout: 15 * time.Second, OutputLimit: 4096, Args: []string{
		"-i", keyPath,
		"-p", port,
		"-o", "StrictHostKeyChecking=" + androidControlHostKeyPolicy(),
		"-o", "UserKnownHostsFile=" + androidControlKnownHostsPath(),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"root@" + host,
		remote,
	}})
	return err
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

// clientIP is the direct compatibility ingress: headers never choose its source.
func clientIP(r *http.Request) string {
	source, _ := httpboundary.ClientSource(r, httpboundary.Direct)
	return source
}

func clientIPForIngress(r *http.Request, ingress ClientIngress) (string, error) {
	policy := httpboundary.Direct
	if ingress == ClientIngressTrustedProxy {
		policy = httpboundary.LoopbackProxy
	} else if ingress != ClientIngressCompat {
		return "", errors.New("invalid client ingress")
	}
	return httpboundary.ClientSource(r, policy)
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

func (s *Server) audit(event AuditEvent) {
	if s == nil {
		return
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if s.auditSink == nil {
		return
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	s.auditSink(event)
}
