//go:build windows

package deviceapi

import "errors"

func privateHostingDirectory(string) error {
	return errors.New("device authority production hosting requires Linux service permissions; Windows adapters are fixture-only")
}
