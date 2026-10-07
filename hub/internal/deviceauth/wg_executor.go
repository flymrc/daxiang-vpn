package deviceauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type WGExecutor struct {
	binary, iface string
	supervisor    string
	policy        Policy
	timeout       time.Duration
}

// NewWGExecutor requires an explicit absolute trusted executable; it never uses
// PATH, a shell, stdin configuration or wg dump (which exposes private keys).
// Installation owns this executable and parent directory. It is not allowed to
// delegate mutation to an independent background writer.
func NewWGExecutor(binary, iface string, policy Policy, timeout time.Duration) (*WGExecutor, error) {
	return newWGExecutor(binary, "", iface, policy, timeout)
}

// NewSupervisedWGExecutor uses an explicit trusted Linux helper. It must be
// installed with the same trust as wg; it is never resolved from PATH.
func NewSupervisedWGExecutor(binary, supervisor, iface string, policy Policy, timeout time.Duration) (*WGExecutor, error) {
	if !filepath.IsAbs(supervisor) {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(supervisor)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	return newWGExecutor(binary, supervisor, iface, policy, timeout)
}

type executionFenceKey struct{}

func newWGExecutor(binary, supervisor, iface string, policy Policy, timeout time.Duration) (*WGExecutor, error) {
	if !filepath.IsAbs(binary) || !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`).MatchString(iface) || timeout <= 0 || timeout > 30*time.Second {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	policy.AddressPools = append([]string(nil), policy.AddressPools...)
	policy.Protected = append([]Protection(nil), policy.Protected...)
	if err = normalizePolicy(&policy); err != nil {
		return nil, err
	}
	if err = trustedExecutionBinary(binary); err != nil {
		return nil, err
	}
	if err = requireBoundedSupervision(supervisor); err != nil {
		return nil, err
	}
	return &WGExecutor{binary: binary, supervisor: supervisor, iface: iface, policy: policy, timeout: timeout}, nil
}
func (e *WGExecutor) fence(f Fence) error {
	if f.Epoch != e.policy.Epoch || !identifier(f.DeviceID) || f.Generation <= 0 || f.Sequence <= 0 {
		return ErrPolicy
	}
	return nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}
func (e *WGExecutor) run(ctx context.Context, args ...string) ([]byte, error) {
	if e.supervisor != "" {
		return e.runSupervised(ctx, args...)
	}
	bounded, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	cmd := exec.Command(e.binary, args...)
	cmd.Stdin = nil
	cmd.Env = []string{}
	cmd.WaitDelay = 100 * time.Millisecond
	out := &cappedBuffer{limit: 2 << 20}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cleanup, err := startBounded(cmd)
	if err != nil {
		return nil, ErrExecutionUnknown
	}
	defer cleanup()
	finished := make(chan error, 1)
	go func() { finished <- waitBounded(cmd, cleanup) }()
	select {
	case err = <-finished:
		cleanup()
		if err != nil || bounded.Err() != nil {
			return nil, ErrExecutionUnknown
		}
		return append([]byte(nil), out.Bytes()...), nil
	case <-bounded.Done():
		cleanup()
		<-finished
		return nil, ErrExecutionUnknown
	}
}
func (e *WGExecutor) Snapshot(ctx context.Context, f Fence) ([]Peer, error) {
	if err := e.fence(f); err != nil {
		return nil, err
	}
	out, err := e.run(ctx, "show", e.iface, "allowed-ips")
	if err != nil {
		return nil, err
	}
	var peers []Peer
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 || !validKey(parts[0]) {
			return nil, ErrVerification
		}
		p := Peer{PublicKey: parts[0]}
		parts[1] = strings.TrimSpace(parts[1])
		if parts[1] != "(none)" {
			for _, v := range strings.Split(parts[1], ",") {
				prefix, err := netip.ParsePrefix(strings.TrimSpace(v))
				if err != nil || prefix.Addr().Is4In6() {
					return nil, ErrVerification
				}
				p.AllowedIPs = append(p.AllowedIPs, prefix.String())
			}
		}
		peers = append(peers, p)
		if len(peers) > 16384 {
			return nil, ErrVerification
		}
	}
	if _, err := runtimeMap(peers); err != nil {
		return nil, ErrVerification
	}
	return peers, nil
}
func (e *WGExecutor) Apply(ctx context.Context, f Fence, p Peer) error {
	if err := e.fence(f); err != nil {
		return err
	}
	if !validKey(p.PublicKey) || len(p.AllowedIPs) != 1 {
		return ErrInvalid
	}
	validator := &Store{opts: Options{Policy: e.policy}}
	addr, err := validator.address(p.PublicKey, p.AllowedIPs[0])
	if err != nil {
		return err
	}
	_, err = e.run(ctx, "set", e.iface, "peer", p.PublicKey, "allowed-ips", addr)
	return err
}
func (e *WGExecutor) Remove(ctx context.Context, f Fence, key string) error {
	if err := e.fence(f); err != nil {
		return err
	}
	if !validKey(key) {
		return ErrInvalid
	}
	for _, p := range e.policy.Protected {
		if p.PublicKey == key {
			return ErrProtected
		}
	}
	_, err := e.run(ctx, "set", e.iface, "peer", key, "remove")
	return err
}
