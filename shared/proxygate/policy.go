// Package proxygate implements a closed-by-default admission barrier for an
// immutable, operator-owned source scope. It does not authorize WireGuard peers.
package proxygate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"sort"
)

const (
	Version               = 1
	MaxPolicyBytes        = 32768
	MaxSources            = 512
	MaxAdmissions         = 4096
	MaxTrackedConnections = 4
	MaxCleanupWorkers     = 16
	MaxCleanupConnections = MaxAdmissions * MaxTrackedConnections
	MaxMessageBytes       = 1024
)

var (
	ErrPolicy         = errors.New("proxy_gate_invalid_policy")
	ErrClosed         = errors.New("proxy_gate_closed")
	ErrCapacity       = errors.New("proxy_gate_capacity")
	ErrProtocol       = errors.New("proxy_gate_protocol")
	ErrUnsupported    = errors.New("proxy_gate_unsupported")
	ErrCleanupUnknown = errors.New("proxy_gate_cleanup_unknown")
)
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// Policy is immutable after New. All source prefixes must be canonical and
// disjoint. Retained sources document protected/unknown hosting exclusions;
// every address outside ManagedSources is also retained.
type Policy struct {
	Version         int      `json:"version"`
	Epoch           string   `json:"epoch"`
	Interface       string   `json:"interface"`
	ManagedBy       string   `json:"managed_by"`
	PolicySHA256    string   `json:"policy_sha256"`
	ProfileSHA256   string   `json:"profile_sha256"`
	Listener        string   `json:"listener"`
	ControllerUID   uint32   `json:"controller_uid"`
	ManagedSources  []string `json:"managed_sources"`
	RetainedSources []string `json:"retained_sources"`
}

func (p Policy) Clone() Policy {
	p.ManagedSources = append([]string{}, p.ManagedSources...)
	p.RetainedSources = append([]string{}, p.RetainedSources...)
	return p
}
func digest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func (p Policy) Validate() error {
	if p.Version != Version || !identifier.MatchString(p.Epoch) || !identifier.MatchString(p.Interface) || len(p.Interface) > 15 || !identifier.MatchString(p.ManagedBy) || !digest(p.PolicySHA256) || !digest(p.ProfileSHA256) || len(p.ManagedSources) == 0 || len(p.ManagedSources) > MaxSources || p.RetainedSources == nil || len(p.RetainedSources) > MaxSources {
		return ErrPolicy
	}
	ap, e := netip.ParseAddrPort(p.Listener)
	if e != nil || ap.String() != p.Listener || !ap.Addr().Is4() || ap.Addr().IsUnspecified() || ap.Addr().IsMulticast() || ap.Port() == 0 {
		return ErrPolicy
	}
	prefixes := make([]netip.Prefix, 0, len(p.ManagedSources)+len(p.RetainedSources))
	for _, list := range [][]string{p.ManagedSources, p.RetainedSources} {
		for _, s := range list {
			pr, e := netip.ParsePrefix(s)
			if e != nil || pr.String() != s || pr != pr.Masked() || !pr.Addr().Is4() || pr.Addr().IsUnspecified() || pr.Addr().IsMulticast() || pr.Bits() < 8 {
				return ErrPolicy
			}
			for _, old := range prefixes {
				if old.Overlaps(pr) {
					return ErrPolicy
				}
			}
			prefixes = append(prefixes, pr)
		}
	}
	return nil
}

// SHA256 binds the complete validated source policy, including the authority
// and profile digests, controller UID, listener, and retained exclusions.
func (p Policy) SHA256() string {
	if p.Validate() != nil {
		return ""
	}
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func DecodePolicy(raw []byte) (Policy, error) {
	var p Policy
	if len(raw) == 0 || len(raw) > MaxPolicyBytes {
		return p, ErrPolicy
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || p.Validate() != nil {
		return Policy{}, ErrPolicy
	}
	canonical, _ := json.Marshal(p)
	// Exact canonical encoding also rejects duplicate, missing, null, case alias,
	// trailing, and alternate numeric representations without an unbounded tree.
	if !bytes.Equal(raw, canonical) {
		return Policy{}, ErrPolicy
	}
	return p, nil
}
func (p Policy) ValidateListener(actual net.Addr) error {
	if actual == nil || actual.String() != p.Listener {
		return ErrPolicy
	}
	return nil
}
func (p Policy) IsManaged(a netip.Addr) bool {
	for _, s := range p.ManagedSources {
		pr, e := netip.ParsePrefix(s)
		if e == nil && pr.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

// Covers verifies complete coverage, including adjacent fixed subprefixes.
func (p Policy) Covers(pr netip.Prefix) bool {
	if p.Validate() != nil || !pr.IsValid() || !pr.Addr().Is4() || pr != pr.Masked() {
		return false
	}
	span := func(q netip.Prefix) (uint64, uint64) {
		a := q.Addr().As4()
		start := uint64(binary.BigEndian.Uint32(a[:]))
		return start, start + (uint64(1) << uint(32-q.Bits())) - 1
	}
	type interval struct{ start, end uint64 }
	ranges := make([]interval, 0, len(p.ManagedSources))
	for _, s := range p.ManagedSources {
		a, b := span(netip.MustParsePrefix(s))
		ranges = append(ranges, interval{a, b})
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	next, end := span(pr)
	for _, r := range ranges {
		if r.end < next {
			continue
		}
		if r.start > next {
			return false
		}
		next = r.end + 1
		if next > end {
			return true
		}
	}
	return false
}
