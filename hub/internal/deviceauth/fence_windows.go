//go:build windows

package deviceauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openRegular(path string, create bool) (*os.File, error) {
	volume := filepath.VolumeName(path)
	if strings.HasPrefix(path, `\\`) || len(volume) != 2 || volume[1] != ':' {
		return nil, fmt.Errorf("%w: only private local fixed volumes are supported", ErrPolicy)
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, err
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, fmt.Errorf("%w: remote/removable volumes are unsupported", ErrPolicy)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if create {
		disposition = windows.OPEN_ALWAYS
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, disposition,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	typ, err := windows.GetFileType(h)
	if err != nil || typ != windows.FILE_TYPE_DISK || info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("%w: database/fence must be a single-link regular file", ErrPolicy)
	}
	return os.NewFile(uintptr(h), path), nil
}

func regularPath(f *os.File) (string, error) {
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n == 0 || n > 32768 {
			return "", ErrPolicy
		}
		if n >= uint32(len(buffer)) {
			buffer = make([]uint16, n+1)
			continue
		}
		path := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(path, `\\?\UNC\`) {
			return "", ErrPolicy
		}
		return strings.TrimPrefix(path, `\\?\`), nil
	}
}

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func isLockBusy(err error) bool { return errors.Is(err, windows.ERROR_LOCK_VIOLATION) }
