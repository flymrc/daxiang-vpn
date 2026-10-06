//go:build !windows

package systemproxy

import "fmt"

// macOS networksetup changes machine/service settings; UID locking alone has
// not been validated as a sufficient ownership boundary. Do not claim support.
func NewPlatform() (Platform, error) {
	return Platform{}, fmt.Errorf("system_proxy_platform_unsupported: CLI system proxy is not implemented on this platform")
}
