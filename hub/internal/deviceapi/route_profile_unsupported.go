//go:build !linux

package deviceapi

import "zongheng-vpn/hub/internal/deviceauth"

func readProtectedProfile(string) ([]byte, error)       { return nil, deviceauth.ErrInvalid }
func readProtectedSource(string, int64) ([]byte, error) { return nil, deviceauth.ErrInvalid }
