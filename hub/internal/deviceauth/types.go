// Package deviceauth implements persisted customer authority and exact-peer
// reconciliation. Only the explicit isolated TLS v2 opt-in path uses it; legacy
// TokenStore/YAML production authority and its interface are never adopted.
// Campaign migration, authority cutover and backup revocation merge are pending.
package deviceauth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid          = errors.New("deviceauth: invalid input")
	ErrConflict         = errors.New("deviceauth: ownership or idempotency conflict")
	ErrUnauthorized     = errors.New("deviceauth: owner or device is not authorized")
	ErrGeneration       = errors.New("deviceauth: generation conflict")
	ErrSuperseded       = errors.New("deviceauth: operation superseded")
	ErrProtected        = errors.New("deviceauth: protected or unknown runtime peer")
	ErrExpired          = errors.New("deviceauth: device authorization expired")
	ErrOverdue          = errors.New("deviceauth: unconverged authorization exceeded deadline")
	ErrVerification     = errors.New("deviceauth: runtime verification failed")
	ErrExecutionUnknown = errors.New("deviceauth: external execution result unknown")
	ErrPolicy           = errors.New("deviceauth: authority policy does not match")
	ErrSupervision      = errors.New("deviceauth: crash-safe external execution supervision is required")
	ErrQuota            = errors.New("deviceauth: retained authority resource ceiling reached")
)

type Protection struct {
	PublicKey string `json:"public_key,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

type Policy struct {
	Epoch        string       `json:"epoch"`
	ManagedBy    string       `json:"managed_by"`
	AddressPools []string     `json:"address_pools"`
	Protected    []Protection `json:"protected"`
}

type Options struct {
	Policy Policy
	// FencePath, if given, must equal canonicalDatabasePath+".deviceauth.lock".
	// All writers share this persistent local file; it is never unlinked/renamed.
	// The hosting service must protect its directory from untrusted writers;
	// this offline package does not install service ACLs or production privileges.
	FencePath        string
	Now              func() time.Time
	ActionTimeout    time.Duration
	RevocationBudget time.Duration
	// Positive lower ceilings are useful for controlled fixtures. The hosting
	// service uses the fixed defaults and exposes no API/env to relax them.
	Resources ResourceLimits
}

type ResourceLimits struct {
	GrantHighWaterBytes                                                int64
	Devices, Credentials, Activations, Requests, Audits, Cancellations int64
}

// Actor is already authenticated by a trusted local caller, not a credential accepted
// from an HTTP body. This module checks persisted ownership but does not prove
// that ID/OwnerID came from a live, authorized credential. HTTP uses SubmitSigned.
type Actor struct{ ID, OwnerID string }

type Enrollment struct {
	DeviceID   string
	OwnerID    string
	Role       string
	ValidUntil time.Time
}

type Command struct {
	Actor              Actor
	DeviceID           string
	Action             string
	IdempotencyKey     string
	ExpectedGeneration int64
	PublicKey          string
	Address            string
	Reason             string
}

type Device struct {
	ID, OwnerID, Role, State      string
	ValidUntil                    time.Time
	Generation, AppliedGeneration int64
	PublicKey, Address            string
}

type Operation struct {
	ID, DeviceID, Action, Epoch, State string
	Generation, Fence, Attempts        int64
	LastError                          string
	Deadline                           time.Time
}

type Peer struct {
	PublicKey  string
	AllowedIPs []string
}

type Fence struct {
	Epoch, DeviceID      string
	Generation, Sequence int64
}

// Executor is a synchronous, exact-peer boundary, not a generic wg command hook.
// Methods must finish ALL side effects before returning (including on cancellation).
// A future subprocess adapter must kill and reap its child before returning and
// cannot transfer mutation privileges to a detached child. The store keeps its
// process-crash-released file fence for snapshot/action/verification/result commit.
// No cooperative database protocol fences independent root/wg writers: deployment
// must make this the only mutation boundary before authority can be switched.
type Executor interface {
	Snapshot(context.Context, Fence) ([]Peer, error)
	Apply(context.Context, Fence, Peer) error
	Remove(context.Context, Fence, string) error
}
