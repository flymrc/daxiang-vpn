//go:build darwin

package paths

import (
	"os"
	"path/filepath"
)

func defaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "ZonghengVPN"), nil
}

func clientBinaryName() string {
	return "zhvpn"
}
