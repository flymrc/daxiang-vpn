package proxy

import (
	"fmt"
	"os"
	"path/filepath"
)

// Create commits a new protected file without replacing any existing target.
// A hard-link publication is atomic and refuses unsupported filesystems. The
// caller still owns transaction coordination; this does not fsync the directory
// or defend against rollback by the OS account which owns the entire home.
func (s *PrivateState) Create(data []byte) error {
	if len(data) == 0 || len(data) > 64<<10 {
		return fmt.Errorf("private state size outside limit")
	}
	if err := secureDirectory(s.home, filepath.Dir(s.path)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".zhvpn-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := protectFile(s.home, file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), s.path); err != nil {
		return err
	}
	// Drop the temporary link before a reader verifies the single-link policy.
	// Failure here is an uncertain commit: the caller must not report success.
	return os.Remove(file.Name())
}
