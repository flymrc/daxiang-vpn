//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func lockSecureRegistry(path string) (*os.File, error) {
	volume := filepath.VolumeName(path)
	drive, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, err
	}
	if windows.GetDriveType(drive) != windows.DRIVE_FIXED {
		return nil, errors.New("registry requires a local fixed drive")
	}
	name, err := windows.UTF16PtrFromString(path + ".lock")
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path+".lock")
	if err := validateSecureFileHandle(f); err != nil {
		f.Close()
		return nil, err
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func validateSecureFileHandle(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return errors.New("secure file must have one link and no reparse alias")
	}
	return nil
}

// Windows is supported for the isolated protocol tests. Production reverse
// endpoints are Linux/Android; Windows service key DACL provisioning is not
// part of this transport migration and must be separately validated.
func validateSecurePrivateKeyHandle(*os.File) error { return nil }

func validateSecureAuthorityHandle(*os.File) error { return nil }

func validateSecureDirectoryPath(string) error { return nil }

func syncSecureDirectory(string) error { return nil }

func replaceSecureFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
