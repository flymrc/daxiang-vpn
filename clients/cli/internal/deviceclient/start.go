package deviceclient

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"zongheng-vpn/shared/config"
	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
)

type startOptions struct {
	server, caFile string
	generation     int64
	port           int
	timeout        time.Duration
}

func parseStart(args []string) (startOptions, error) {
	o := startOptions{}
	f := flag.NewFlagSet("device start", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.server, "server", "", "explicit v2 authority")
	f.StringVar(&o.caFile, "ca-file", "", "normally verified private CA")
	f.Int64Var(&o.generation, "expected-generation", 0, "bound desired generation")
	f.IntVar(&o.port, "port", 7890, "local loopback proxy port")
	f.DurationVar(&o.timeout, "timeout", 15*time.Second, "command budget")
	jsonOut := f.Bool("json", true, "v2 startup receipt")
	if !uniqueFlags(f, args) || f.Parse(args) != nil || f.NArg() != 0 || !*jsonOut || o.generation < 1 || o.generation > dc.ProxySafeInteger || o.port < 1 || o.port > 65535 || o.timeout <= 0 || o.timeout > 30*time.Second {
		return o, &failure{code: "invalid_arguments"}
	}
	return o, nil
}

// flag.FlagSet otherwise silently keeps the last repeated value. This scan
// rejects aliases/repetition and positional separators before parsing any IO.
func uniqueFlags(f *flag.FlagSet, args []string) bool {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			return false
		}
		arg = strings.TrimPrefix(arg, "-")
		arg = strings.TrimPrefix(arg, "-")
		name, _, eq := strings.Cut(arg, "=")
		entry := f.Lookup(name)
		if entry == nil || seen[name] {
			return false
		}
		seen[name] = true
		if !eq {
			boolean, ok := entry.Value.(interface{ IsBoolFlag() bool })
			if !ok || !boolean.IsBoolFlag() {
				i++
				if i >= len(args) {
					return false
				}
			}
		}
	}
	return true
}

// StartTimeout shares the exact start argument parser. The caller starts this
// budget before acquiring the home transaction and keeps it through Start.
func StartTimeout(args []string) (time.Duration, error) {
	o, e := parseStart(args)
	return o.timeout, e
}

// FailureCode never returns parser/transport/path/configuration text.
func FailureCode(err error) string {
	var f *failure
	if errors.As(err, &f) {
		return f.code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "command_timeout"
	}
	return "local_storage_failure"
}

// ReportSetupFailure gives device command dispatch a redacted v2 early error.
func ReportSetupFailure(out io.Writer) error {
	return encodeReceipt(out, resultError("unknown", errors.New("local setup failed"), nil))
}

// PrepareStartLocked requires the caller to hold the same home operation lock
// through this function, config persistence and proxy.Start. It never starts an
// engine, applies a peer, enables an OS proxy, reads legacy keys or returns a
// receipt containing secrets. Its returned Config contains a WG private key and
// must not be logged or returned to the CLI. File IO has the OS filesystem's
// availability boundary; the context bounds lock/network/cooperative work.
func PrepareStartLocked(ctx context.Context, home paths.Context, args []string) (config.Config, error) {
	o, e := parseStart(args)
	if e != nil {
		return config.Config{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if ctx.Err() != nil {
		return config.Config{}, &failure{code: "command_timeout"}
	}
	s := privateStore{home}
	st, e := s.load()
	if e != nil {
		return config.Config{}, e
	}
	if st.Pending != nil {
		return config.Config{}, &failure{code: "pending_resolution_required"}
	}
	if st.Credential == nil {
		return config.Config{}, &failure{code: "not_activated"}
	}
	if st.WireGuard == nil || st.WireGuard.Generation < 1 {
		return config.Config{}, &failure{code: "binding_required"}
	}
	if o.generation != st.WireGuard.Generation {
		return config.Config{}, &failure{code: "stale_generation"}
	}
	if st.Credential.ExpiresUnixSeconds <= time.Now().Unix() {
		return config.Config{}, &failure{code: "credential_expired"}
	}
	roots, e := loadRoots(ctx, o.caFile)
	if e != nil {
		return config.Config{}, e
	}
	server := o.server
	if server == "" {
		server = st.Server
	}
	c, e := New(server, roots)
	if e != nil {
		return config.Config{}, e
	}
	defer c.Close()
	if c.server != st.Server {
		return config.Config{}, &failure{code: "server_mismatch"}
	}
	key, e := privateKey(st.PrivateKey)
	if e != nil {
		return config.Config{}, e
	}
	body := dc.ProxyBootstrapRequest{ExpectedGeneration: o.generation, WireguardPublicKey: st.WireGuard.PublicKey}
	raw, _ := json.Marshal(body)
	const path = "/api/v2/proxy/bootstrap"
	ch, e := c.challenge(ctx, "proxy.bootstrap", "POST", path, raw, st.Credential, "", key)
	if e != nil {
		return config.Config{}, e
	}
	rid, e := opaque()
	if e != nil {
		return config.Config{}, e
	}
	h, _ := headers("proxy.bootstrap", "POST", path, raw, st.Credential, ch, rid, key)
	var projection dc.ProxyBootstrap
	if e = c.call(ctx, "POST", path, raw, h, 200, &projection, false); e != nil {
		return config.Config{}, e
	}
	if e = validateProjection(projection, st, o.generation, c.now().Unix()); e != nil {
		return config.Config{}, e
	}
	p := projection.Profile
	cfg := config.Config{
		Authorization: config.AuthorizationConfig{Source: "device-v2", DeviceID: projection.DeviceId, CredentialID: projection.CredentialId, DesiredGeneration: projection.DesiredGeneration, AppliedGeneration: projection.AppliedGeneration, Profile: p, ProfileSHA256: projection.ProfileSha256, IssuedUnixSeconds: projection.IssuedUnixSeconds, ExpiresUnixSeconds: projection.ExpiresUnixSeconds},
		Client:        config.ClientConfig{Name: projection.DeviceId},
		Hub:           config.HubConfig{Endpoint: p.WgEndpoint, PublicKey: p.WgPublicKey},
		Egress:        config.EgressConfig{Name: p.EgressId, DisplayName: p.EgressName, ProxyAddr: p.ProxyAddress},
		LocalProxy:    config.LocalProxyConfig{ListenAddr: "127.0.0.1", ListenPort: o.port},
		WireGuard:     config.WireGuardConfig{Address: projection.Address, PrivateKey: st.WireGuard.PrivateKey, PublicKey: st.WireGuard.PublicKey, AllowedIPs: append([]string(nil), p.AllowedIps...)},
	}
	if cfg.ValidateForProxyStart(c.now()) != nil {
		return config.Config{}, &failure{code: "invalid_response"}
	}
	if ctx.Err() != nil {
		return config.Config{}, &failure{code: "command_timeout"}
	}
	// A trusted TLS response establishes the first epoch/profile floor. A later
	// epoch change or same-revision-content change requires separate recovery,
	// not silent re-enrollment. No reusable projection/private key is cached here.
	st.ProxyProfile = &p
	st.WireGuard.AppliedGeneration = projection.AppliedGeneration
	if e = s.save(st); e != nil {
		return config.Config{}, e
	}
	if projection.ExpiresUnixSeconds <= c.now().Unix() {
		return config.Config{}, &failure{code: "projection_expired"}
	}
	return cfg, nil
}

func validateProjection(p dc.ProxyBootstrap, st state, generation, now int64) error {
	if p.Version != 1 || p.DeviceId != st.Credential.DeviceId || p.CredentialId != st.Credential.CredentialId || p.WireguardPublicKey != st.WireGuard.PublicKey || p.Address != st.WireGuard.Address || !dc.ValidProxyCustomerAddress(p.Address) || p.DesiredGeneration != generation || p.AppliedGeneration != generation || p.IssuedUnixSeconds < now-30 || p.IssuedUnixSeconds > now+5 || p.ExpiresUnixSeconds <= p.IssuedUnixSeconds || p.ExpiresUnixSeconds > p.IssuedUnixSeconds+30 || p.ExpiresUnixSeconds > now+30 || p.ExpiresUnixSeconds > st.Credential.ExpiresUnixSeconds {
		return &failure{code: "invalid_response"}
	}
	if p.ExpiresUnixSeconds <= now {
		return &failure{code: "projection_expired"}
	}
	digest, e := dc.ProxyRouteProfileDigest(p.Profile)
	if e != nil || digest != p.ProfileSha256 {
		return &failure{code: "invalid_response"}
	}
	if st.ProxyProfile != nil {
		old := *st.ProxyProfile
		if old.AuthorityEpoch != p.Profile.AuthorityEpoch {
			return &failure{code: "authority_epoch_mismatch"}
		}
		oldDigest, _ := dc.ProxyRouteProfileDigest(old)
		if p.Profile.Revision < old.Revision || (p.Profile.Revision == old.Revision && oldDigest != digest) {
			return &failure{code: "profile_rollback"}
		}
	}
	return nil
}

func loadRoots(ctx context.Context, path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, &failure{code: "command_timeout"}
	}
	f, e := openCA(path)
	if e != nil {
		return nil, &failure{code: "invalid_ca_file"}
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return nil, &failure{code: "invalid_ca_file"}
	}
	if ctx.Err() != nil {
		return nil, &failure{code: "command_timeout"}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, &failure{code: "invalid_ca_file"}
	}
	return roots, nil
}
