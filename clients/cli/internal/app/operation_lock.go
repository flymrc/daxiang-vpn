package app

import (
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

func withOperationLock(ctx paths.Context, fn func() error) error {
	return proxy.WithOperationLock(ctx, fn)
}
