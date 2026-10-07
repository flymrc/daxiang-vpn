package proxy

import (
	"fmt"
	"path/filepath"

	"zongheng-vpn/shared/paths"
)

// PrivateState reuses runtime Owner/DACL, no-reparse and atomic file policy.
// Callers must hold WithOperationLock across read/intent/network/commit.
// Only fixed client credential/state paths are exposed, never engine keys.
type PrivateState struct {
	home paths.Context
	path string
}

func NewPrivateState(home paths.Context, name string) (*PrivateState, error) {
	if name != "device-v2-state.json" && name != "config.yaml" && name != "wireguard/client.key" && name != "update-v1-registration.json" && name != "update-v1-state.json" {
		return nil, fmt.Errorf("unsupported private state name")
	}
	root, err := paths.CanonicalRoot(home.Root)
	if err != nil {
		return nil, err
	}
	home = paths.FromRoot(root)
	return &PrivateState{home: home, path: filepath.Join(root, name)}, nil
}

func (s *PrivateState) Read() ([]byte, error) {
	data, err := readPrivateFile(s.home, s.path)
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<10 {
		return nil, fmt.Errorf("private state exceeds size limit")
	}
	return data, nil
}

func (s *PrivateState) Write(data []byte) error {
	if len(data) == 0 || len(data) > 64<<10 {
		return fmt.Errorf("private state size outside limit")
	}
	return writePrivateFile(s.home, s.path, data)
}
