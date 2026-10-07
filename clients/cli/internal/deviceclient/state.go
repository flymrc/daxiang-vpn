package deviceclient

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/curve25519"
	"math"
	"os"

	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

// The whole state is private, including pending rotation material. It is never
// returned as a receipt or exposed through a diagnostic error.
type state struct {
	Version              int                   `json:"version"`
	Server               string                `json:"server"`
	PrivateKey           string                `json:"private_key"`
	Credential           *dc.Credential        `json:"credential,omitempty"`
	Pending              *intent               `json:"pending,omitempty"`
	WireGuardInitialized bool                  `json:"wireguard_initialized,omitempty"`
	WireGuard            *localBinding         `json:"wireguard,omitempty"`
	ProxyProfile         *dc.ProxyRouteProfile `json:"proxy_profile,omitempty"`
}

// Authentication and WireGuard keys have independent lifetimes and purposes.
// A missing key after initialization is corruption, never a rotation request.
type localBinding struct {
	PrivateKey        string `json:"private_key"`
	PublicKey         string `json:"public_key"`
	Address           string `json:"address"`
	Generation        int64  `json:"generation"`
	AppliedGeneration int64  `json:"applied_generation"`
	OperationID       string `json:"operation_id"`
}
type intent struct {
	Kind               string `json:"kind"`
	RequestID          string `json:"request_id"`
	IdempotencyKey     string `json:"idempotency_key,omitempty"`
	NewPrivateKey      string `json:"new_private_key,omitempty"`
	ExpectedGeneration *int64 `json:"expected_generation,omitempty"`
	BindAddress        string `json:"bind_address,omitempty"`
}
type storage interface {
	load() (state, error)
	save(state) error
}
type privateStore struct{ home paths.Context }

func (p privateStore) load() (state, error) {
	store, e := proxy.NewPrivateState(p.home, "device-v2-state.json")
	if e != nil {
		return state{}, e
	}
	b, e := store.Read()
	if errors.Is(e, os.ErrNotExist) {
		return state{}, nil
	}
	if e != nil {
		return state{}, e
	}
	var s state
	if strictDecode(b, &s, "version", "server", "private_key") != nil || s.Version != Version {
		return s, errJSON
	}
	var key ed25519.PrivateKey
	if s.PrivateKey != "" {
		key, e = privateKey(s.PrivateKey)
		if e != nil {
			return s, e
		}
	} else if s.Credential != nil || s.Pending != nil {
		return s, errJSON
	}
	if s.Credential != nil && (s.Credential.AuthPublicKey != publicKey(key) || !hexID(s.Credential.DeviceId) || !hexID(s.Credential.CredentialId) || s.Credential.ExpiresUnixSeconds < 1) {
		return s, errJSON
	}
	if s.WireGuardInitialized != (s.WireGuard != nil) {
		return s, errJSON
	}
	if s.WireGuard != nil {
		w := s.WireGuard
		_, pub, ke := wireGuardKey(w.PrivateKey)
		if s.Credential == nil || ke != nil || w.PublicKey != pub || w.Generation < 0 || w.Generation > dc.ProxySafeInteger || w.AppliedGeneration < 0 || w.AppliedGeneration > w.Generation {
			return s, errJSON
		}
		if w.Generation == 0 {
			if w.Address != "" || w.OperationID != "" || w.AppliedGeneration != 0 {
				return s, errJSON
			}
		} else if !hostAddress(w.Address) || !hexID(w.OperationID) {
			return s, errJSON
		}
	}
	if s.ProxyProfile != nil && (s.WireGuard == nil || s.WireGuard.Generation == 0 || dc.ValidateProxyRouteProfile(*s.ProxyProfile) != nil) {
		return s, errJSON
	}
	if s.Pending != nil {
		p := s.Pending
		if !validCommand(p.Kind) || p.Kind == "bind" || p.Kind == "status" || p.Kind == "recover" || p.Kind == "cancel-pending" || !hexID(p.RequestID) {
			return s, errJSON
		}
		if p.Kind == "rotate-credential" {
			if s.Credential == nil {
				return s, errJSON
			}
			if _, e = privateKey(p.NewPrivateKey); e != nil {
				return s, e
			}
		} else if p.NewPrivateKey != "" {
			return s, errJSON
		}
		if p.Kind == "activate" && s.Credential != nil {
			return s, errJSON
		}
		if p.Kind == "apply" || p.Kind == "disable" || p.Kind == "revoke" {
			if s.Credential == nil || !identifier(p.IdempotencyKey, 128) || p.ExpectedGeneration == nil || *p.ExpectedGeneration < 0 || *p.ExpectedGeneration == math.MaxInt64 {
				return s, errJSON
			}
		} else if p.IdempotencyKey != "" || p.ExpectedGeneration != nil {
			return s, errJSON
		}
		if p.BindAddress != "" && (p.Kind != "apply" || !hostAddress(p.BindAddress) || s.WireGuard == nil || p.ExpectedGeneration == nil || *p.ExpectedGeneration < s.WireGuard.Generation) {
			return s, errJSON
		}
	}
	return s, nil
}
func hostAddress(address string) bool {
	return dc.ValidProxyCustomerAddress(address)
}
func wireGuardKey(value string) ([]byte, string, error) {
	b, e := base64.StdEncoding.DecodeString(value)
	if e != nil || len(b) != 32 || base64.StdEncoding.EncodeToString(b) != value {
		return nil, "", errJSON
	}
	var nonzero byte
	for _, v := range b {
		nonzero |= v
	}
	if nonzero == 0 || b[0]&7 != 0 || b[31]&0x80 != 0 || b[31]&0x40 == 0 {
		return nil, "", errJSON
	}
	pub, e := curve25519.X25519(b, curve25519.Basepoint)
	if e != nil {
		return nil, "", errJSON
	}
	return b, base64.StdEncoding.EncodeToString(pub), nil
}
func intentCommand(p *intent) string {
	if p.BindAddress != "" {
		return "bind"
	}
	return p.Kind
}
func acceptBinding(st *state, p *intent, op dc.Operation) error {
	if p.BindAddress == "" {
		return nil
	}
	if st.WireGuard == nil || p.ExpectedGeneration == nil || op.Action != "apply" || op.Generation != *p.ExpectedGeneration+1 {
		return errJSON
	}
	st.WireGuard.Address = p.BindAddress
	st.WireGuard.Generation = op.Generation
	st.WireGuard.OperationID = op.OperationId
	st.WireGuard.AppliedGeneration = 0
	if op.Effective {
		st.WireGuard.AppliedGeneration = op.Generation
	}
	return nil
}
func (p privateStore) save(s state) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	store, e := proxy.NewPrivateState(p.home, "device-v2-state.json")
	if e != nil {
		return e
	}
	return store.Write(b)
}
func privateKey(value string) (ed25519.PrivateKey, error) {
	b, e := base64.StdEncoding.DecodeString(value)
	if e != nil || len(b) != ed25519.PrivateKeySize || base64.StdEncoding.EncodeToString(b) != value || !bytes.Equal(ed25519.NewKeyFromSeed(b[:ed25519.SeedSize]), b) {
		return nil, errJSON
	}
	return ed25519.PrivateKey(b), nil
}
func publicKey(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}
func validPublic(v string) bool {
	b, e := base64.StdEncoding.DecodeString(v)
	if e != nil || len(b) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(b) != v {
		return false
	}
	for _, x := range b {
		if x != 0 {
			return true
		}
	}
	return false
}
