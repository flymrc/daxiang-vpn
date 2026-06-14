//go:build !linux

package main

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"
)

func netDialer(timeout time.Duration, bindInterface string) (net.Dialer, error) {
	dialer := net.Dialer{Timeout: timeout}
	if strings.TrimSpace(bindInterface) != "" {
		return dialer, fmt.Errorf("interface binding is only supported on linux, not %s", runtime.GOOS)
	}
	return dialer, nil
}
