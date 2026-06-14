//go:build linux

package main

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func netDialer(timeout time.Duration, bindInterface string) (net.Dialer, error) {
	dialer := net.Dialer{Timeout: timeout}
	bindInterface = strings.TrimSpace(bindInterface)
	if bindInterface == "" {
		return dialer, nil
	}
	dialer.Control = func(network string, address string, raw syscall.RawConn) error {
		var sockErr error
		if err := raw.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, bindInterface)
		}); err != nil {
			return err
		}
		if sockErr != nil {
			return fmt.Errorf("bind interface %s: %w", bindInterface, sockErr)
		}
		return nil
	}
	return dialer, nil
}
