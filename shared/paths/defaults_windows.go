//go:build windows

package paths

import (
	"os"
	"path/filepath"
)

func defaultRoot() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		local = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(local, "ZonghengVPN"), nil
}

func clientBinaryName() string {
	return "zhvpn.exe"
}
