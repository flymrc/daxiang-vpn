//go:build !windows

package proxy

import (
	"golang.org/x/sys/unix"
	"zongheng-vpn/shared/paths"
)

func engineOutputAliasHelper(paths.Context) int { return 2 }

func nativeEngineOutputCanary(value string) {
	_, _ = unix.Write(1, []byte(value+"\n"))
	_, _ = unix.Write(2, []byte(value+"\n"))
}
