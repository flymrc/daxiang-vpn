//go:build !linux && !windows

package processbudget

import "os/exec"

func prepareDetachedFixture(*exec.Cmd) {}
func killOwnedFixtureSupervisor() bool { return false }
func fixtureProcessAlive(int) bool     { return false }
