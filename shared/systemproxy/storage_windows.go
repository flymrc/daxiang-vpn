//go:build windows

package systemproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func openUserFile(path string, sid *windows.SID, disposition, access, sharing uint32, private bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	sd, err := userSecurity(sid, false)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, access|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, sharing,
		securityAttributes(sd), disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("proxy file must be a non-reparse single-link ordinary file")
	}
	if err := verifySecurity(handle, sid, private); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func (p *windowsPlatform) WithLocked(ctx context.Context, scope Scope, action func(Store) error) error {
	sid, err := p.validateScope(ctx, scope)
	if err != nil {
		return err
	}
	if err := ensureUserDirectory(scope.Root, sid); err != nil {
		return err
	}
	// Hold the parent directory without DELETE sharing as well as the file.
	// Renaming the directory must not create a second lock namespace.
	directory, err := openUserDirectory(scope.Root, sid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(directory)
	var directoryID windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(directory, &directoryID); err != nil {
		return err
	}
	file, err := openUserFile(scope.LockPath, sid, windows.OPEN_ALWAYS, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, false)
	if err != nil {
		return err
	}
	defer file.Close()
	var fileID windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &fileID); err != nil {
		return err
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("system_proxy_lock_busy: another session may be using the journal; no proxy writes: %w", err)
	}
	defer windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	// Do not remove or truncate the lock file. The held handle excludes DELETE
	// sharing; process death closes it and releases the lock through the OS.
	if err := ctx.Err(); err != nil {
		return err
	}
	// Revalidate after acquiring the kernel lock; pre-lock checks are not a
	// grant to act on a subsequently replaced directory or changed owner.
	if _, err := p.validateScope(ctx, scope); err != nil {
		return err
	}
	if err := verifySecurity(directory, sid, false); err != nil {
		return err
	}
	if err := verifySecurity(windows.Handle(file.Fd()), sid, false); err != nil {
		return err
	}
	// Re-open the names while the no-DELETE guards remain held and compare
	// volume/file IDs, so a replaced ancestor cannot split the physical fence.
	checkDir, err := openUserDirectory(scope.Root, sid)
	if err != nil {
		return err
	}
	var currentDir windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(checkDir, &currentDir)
	windows.CloseHandle(checkDir)
	if err != nil || !sameFile(directoryID, currentDir) {
		return fmt.Errorf("proxy directory identity changed after locking")
	}
	checkFile, err := openUserFile(scope.LockPath, sid, windows.OPEN_EXISTING, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, false)
	if err != nil {
		return err
	}
	var currentFile windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(windows.Handle(checkFile.Fd()), &currentFile)
	checkFile.Close()
	if err != nil || !sameFile(fileID, currentFile) {
		return fmt.Errorf("proxy lock file identity changed after locking")
	}
	return action(&windowsStore{scope: scope, sid: sid})
}

func sameFile(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}

type windowsStore struct {
	scope Scope
	sid   *windows.SID
}

func (s *windowsStore) Load() ([]byte, error) {
	if _, err := scopeSID(s.scope); err != nil {
		return nil, err
	}
	file, err := openUserFile(s.scope.JournalPath, s.sid, windows.OPEN_EXISTING, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("proxy journal exceeds safety bound")
	}
	return data, nil
}

func (s *windowsStore) Create(data []byte) error {
	if _, err := scopeSID(s.scope); err != nil {
		return err
	}
	file, err := openUserFile(s.scope.JournalPath, s.sid, windows.CREATE_NEW, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, true)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	// Preserve the file on all failures. A partial WAL is diagnosed failclosed,
	// never silently removed or reused as an ownership grant.
	return file.Sync()
}

func (s *windowsStore) Remove() error {
	if _, err := scopeSID(s.scope); err != nil {
		return err
	}
	// Project callers hold the shared transaction lock. Malicious programs
	// running as the same OS user are outside the ownership security boundary.
	file, err := openUserFile(s.scope.JournalPath, s.sid, windows.OPEN_EXISTING, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, true)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(s.scope.JournalPath)
}
