//go:build !windows && !darwin

package paths

import (
	"os"
	"path/filepath"
)

func defaultRoot() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ZonghengVPN"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ZonghengVPN"), nil
}

func clientBinaryName() string {
	return "zhvpn"
}
