//go:build !windows

package app

import "zongheng-vpn/shared/paths"

func withOperationLock(_ paths.Context, fn func() error) error {
	return fn()
}
