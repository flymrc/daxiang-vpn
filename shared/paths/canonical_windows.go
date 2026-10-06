//go:build windows

package paths

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalExistingRoot(root string) (string, error) {
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	// Resolve through an actual directory handle, including mount-point
	// junctions that filepath.EvalSymlinks can leave unresolved on Windows.
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n == 0 || n > 32768 {
			return "", fmt.Errorf("unexpected canonical directory length: %d", n)
		}
		if n >= uint32(len(buffer)) {
			buffer = make([]uint16, n+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			return `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`), nil
		}
		return strings.TrimPrefix(resolved, `\\?\`), nil
	}
}
