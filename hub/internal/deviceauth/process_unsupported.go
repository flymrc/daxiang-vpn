//go:build !linux && !windows

package deviceauth

import "os/exec"

func requireBoundedSupervision(string) error { return ErrSupervision }
func trustedExecutionBinary(string) error    { return ErrSupervision }
func startBounded(*exec.Cmd) (func(), error) { return nil, ErrPolicy }
func waitBounded(*exec.Cmd, func()) error    { return ErrPolicy }
