// Package httpboundary defines the resource and source-address boundaries of
// the legacy Hub listeners. It does not grant device or token authority.
package httpboundary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const (
	MaxBodyBytes   = 16 << 10
	MaxHeaderBytes = 16 << 10
	MaxXFFBytes    = 512
	MaxXFFHops     = 8
)

// NewServer is shared by the compat, Caddy and admin listeners. These socket
// deadlines do not cancel handler work or its subprocesses.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    MaxHeaderBytes,
	}
}

// DecodeJSON reads the entire bounded body before parsing or authenticating.
// Unknown fields remain compatible, but a second value or trailing garbage is
// rejected. ReadAll is bounded by MaxBytesReader even for chunked requests.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func DecodeError(err error) (int, string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge, "request_too_large"
	}
	return http.StatusBadRequest, "bad_request"
}

type SourcePolicy int

const (
	Direct SourcePolicy = iota
	LoopbackProxy
)

var errSource = errors.New("invalid client source")

// ClientSource trusts XFF only under the listener's fixed LoopbackProxy policy
// and only from the exact Caddy addresses 127.0.0.1 and ::1. Compat always uses
// RemoteAddr, including requests from private and loopback addresses.
//
// XFF is validated in full, then traversed right-to-left past exact known proxy
// hops. The first non-proxy hop is the source. If every hop is a known proxy,
// the leftmost hop is the source. Untrusted hops prevent attacker-controlled
// values farther left from taking ownership of a lease.
func ClientSource(r *http.Request, policy SourcePolicy) (string, error) {
	if policy != Direct && policy != LoopbackProxy {
		return "", errSource
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := parseAddress(host)
	if err != nil {
		return "", errSource
	}
	if policy == Direct || !knownProxy(remote) {
		return remote.String(), nil
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return remote.String(), nil
	}
	size := 0
	for i, v := range values {
		size += len(v)
		if i > 0 {
			size++
		}
		if size > MaxXFFBytes {
			return "", errSource
		}
	}
	parts := strings.Split(strings.Join(values, ","), ",")
	if len(parts) > MaxXFFHops {
		return "", errSource
	}
	hops := make([]netip.Addr, len(parts))
	for i, part := range parts {
		hops[i], err = parseAddress(strings.TrimSpace(part))
		if err != nil {
			return "", errSource
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !knownProxy(hops[i]) || i == 0 {
			return hops[i].String(), nil
		}
	}
	return "", errSource
}

func parseAddress(value string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, errSource
	}
	return ip.Unmap(), nil
}

func knownProxy(ip netip.Addr) bool {
	return ip == netip.MustParseAddr("127.0.0.1") || ip == netip.IPv6Loopback()
}

type sourceKey struct{}

// RequestWithSource binds a verified source under the listener's fixed policy.
func RequestWithSource(r *http.Request, policy SourcePolicy) (*http.Request, error) {
	source, err := ClientSource(r, policy)
	if err != nil {
		return nil, err
	}
	return r.WithContext(context.WithValue(r.Context(), sourceKey{}, source)), nil
}

func SourceFromContext(r *http.Request) string {
	value, _ := r.Context().Value(sourceKey{}).(string)
	return value
}
