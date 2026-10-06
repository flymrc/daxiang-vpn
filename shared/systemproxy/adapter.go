// Package systemproxy contains raw OS adapters and protected user-scoped
// storage. Lease ownership and recovery decisions belong to the CLI runtime.
package systemproxy

import "context"

var Fields = [4]string{"ProxyEnable", "ProxyServer", "ProxyOverride", "AutoConfigURL"}

// Value preserves the registry type and uninterpreted bytes. A nil *Value
// means absent; an empty byte slice is still a present value.
type Value struct {
	Kind  uint32
	Bytes []byte
}

type Adapter interface {
	Read(name string) (*Value, error)
	Write(name string, value *Value) error
	Notify() error
}

type Scope struct {
	UserScope    string
	HomeIdentity string
	Root         string
	JournalPath  string
	LockPath     string
}

type Resolver interface {
	Resolve(ctx context.Context, home string) (Scope, error)
}

type Store interface {
	Load() ([]byte, error)    // nil means absent; all other failures must be errors.
	Create(data []byte) error // immutable, create-new, durable before OS writes.
	Remove() error
}

type Transactions interface {
	WithLocked(ctx context.Context, scope Scope, action func(Store) error) error
}

type Factory interface {
	Open(scope Scope) (Adapter, error)
}

type Platform struct {
	Resolver     Resolver
	Transactions Transactions
	Factory      Factory
}
