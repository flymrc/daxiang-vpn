package devicecontract

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/curve25519"
)

const ProxySafeInteger int64 = 9007199254740991

var ErrProxyProfile = errors.New("device contract: invalid proxy profile")

// Version 1 deliberately supports only a literal IPv4 endpoint and exactly one
// private IPv4 proxy /32. It does no DNS, IO, trust enrollment or WG mutation.
func ValidateProxyRouteProfile(p ProxyRouteProfile) error {
	if p.Version != 1 || p.Revision < 1 || p.Revision > ProxySafeInteger || !proxyIdentifier(p.AuthorityEpoch, 256) || !proxyIdentifier(p.ManagedBy, 256) || !proxyIdentifier(p.EgressId, 256) || !proxyIdentifier(p.EgressName, 128) || !proxyInterface(p.WgInterface) || !ValidWireGuardPublicKey(p.WgPublicKey) || len(p.AllowedIps) != 1 {
		return ErrProxyProfile
	}
	if _, ok := proxyIPv4Endpoint(p.WgEndpoint); !ok {
		return ErrProxyProfile
	}
	ip, ok := proxyIPv4Endpoint(p.ProxyAddress)
	if !ok || !proxyPrivateIPv4(ip) || p.AllowedIps[0] != netip.PrefixFrom(ip, 32).String() {
		return ErrProxyProfile
	}
	return nil
}

// ProxyRouteProfileDigest is content identity only, never a signature. The
// string array and Go JSON escaping are the canonical v1 OpenAPI byte contract.
func ProxyRouteProfileDigest(p ProxyRouteProfile) (string, error) {
	if err := ValidateProxyRouteProfile(p); err != nil {
		return "", err
	}
	b, _ := json.Marshal([]string{"zhvpn-device-route", "v1", strconv.FormatInt(int64(p.Version), 10), p.AuthorityEpoch, p.ManagedBy, p.WgInterface, strconv.FormatInt(p.Revision, 10), p.WgEndpoint, p.WgPublicKey, p.ProxyAddress, p.EgressId, p.EgressName, p.AllowedIps[0]})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// ValidProxyCustomerAddress excludes the legacy management subnet as well as
// public, mapped, zoned, unspecified, multicast and non-host prefixes.
func ValidProxyCustomerAddress(value string) bool {
	p, err := netip.ParsePrefix(value)
	return err == nil && p.String() == value && p.Bits() == 32 && proxyPrivateIPv4(p.Addr())
}

func proxyPrivateIPv4(ip netip.Addr) bool {
	return ip.Is4() && ip.IsPrivate() && !ip.Is4In6() && !netip.MustParsePrefix("10.66.0.0/24").Contains(ip)
}
func proxyIPv4Endpoint(value string) (netip.Addr, bool) {
	if len(value) > 64 {
		return netip.Addr{}, false
	}
	host, port, err := net.SplitHostPort(value)
	ip, pe := netip.ParseAddr(host)
	n, ne := strconv.Atoi(port)
	if err != nil || pe != nil || !ip.Is4() || ip.String() != host || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") || netip.MustParsePrefix("10.66.0.0/24").Contains(ip) || ne != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || net.JoinHostPort(host, port) != value {
		return netip.Addr{}, false
	}
	return ip, true
}
func proxyIdentifier(value string, max int) bool {
	return len(value) > 0 && len(value) <= max && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, unicode.ReplacementChar) && !strings.ContainsFunc(value, unicode.IsControl)
}
func proxyInterface(value string) bool {
	if len(value) < 1 || len(value) > 15 {
		return false
	}
	for _, b := range []byte(value) {
		if (b < 'a' || b > 'z') && (b < 'A' || b > 'Z') && (b < '0' || b > '9') && b != '_' && b != '-' && b != '.' {
			return false
		}
	}
	return true
}

// A syntactically valid WG key still does not prove possession. Reject
// noncanonical field encodings and low-order points before engine construction.
func ValidWireGuardPublicKey(value string) bool {
	b, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(b) != 32 || base64.StdEncoding.EncodeToString(b) != value || b[31]&128 != 0 {
		return false
	}
	// p = 2^255-19, encoded little endian; generated X25519 public keys are < p.
	canonical := false
	for i := 31; i >= 0; i-- {
		limit := byte(255)
		if i == 31 {
			limit = 127
		}
		if i == 0 {
			limit = 237
		}
		if b[i] < limit {
			canonical = true
			break
		}
		if b[i] > limit {
			return false
		}
	}
	if !canonical {
		return false
	}
	probe := [32]byte{9}
	_, err = curve25519.X25519(probe[:], b)
	return err == nil
}
