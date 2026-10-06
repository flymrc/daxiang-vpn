// Package systemproxy is the CLI runtime's system-proxy lease state machine.
// It never infers OS-write authority from an Inspect/status snapshot.
package systemproxy

import (
	"context"
	"fmt"
	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/systemproxy"
)

type EngineIdentity = contracts.EngineIdentity

type Owner struct {
	UserScope string         `json:"user_scope"`
	Home      string         `json:"home"`
	Engine    EngineIdentity `json:"engine_identity"`
}

// Claim.UserScope must come from the original user's authenticated runtime
// identity. An elevated child must not substitute its own account's SID.
type Claim struct {
	UserScope string
	Home      string
	Engine    EngineIdentity
}

type ReadyEngine struct {
	Owner          Owner
	State          string
	ProxyAddr      string
	ProxyReachable bool
}

// Authority must hold the real engine phase/lifetime boundary throughout the
// callback. WithReady/WithRelease run inside the authenticated engine's phase
// gate; WithStopped holds the matching home's operations + free lifetime lock.
// None may be implemented as "Inspect then invoke callback". Implementations
// must never hold the user proxy lock while waiting for an engine to stop.
type Authority interface {
	WithReady(ctx context.Context, owner Owner, action func(ReadyEngine) error) error
	WithRelease(ctx context.Context, owner Owner, action func() error) error
	WithStopped(ctx context.Context, owner Owner, action func() error) error
}

type Receipt struct {
	LeaseID string `json:"lease_id,omitempty"`
	Owner   *Owner `json:"owner,omitempty"`
	Noop    bool   `json:"noop"`
	Owned   bool   `json:"owned"`
}

type Observation struct {
	State       string `json:"state"` // absent/recorded/foreign; never inferred active.
	LeaseID     string `json:"lease_id,omitempty"`
	Owner       *Owner `json:"owner,omitempty"`
	JournalPath string `json:"journal_path"`
}

type Error struct {
	Code        string
	Message     string
	JournalPath string
	Cause       error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	if e.JournalPath != "" {
		message += "\n恢复记录：" + e.JournalPath
	}
	return message
}
func (e *Error) Unwrap() error { return e.Cause }

func failure(code, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}

type Manager struct {
	Resolver     systemproxy.Resolver
	Transactions systemproxy.Transactions
	Factory      systemproxy.Factory
	Authority    Authority
}
