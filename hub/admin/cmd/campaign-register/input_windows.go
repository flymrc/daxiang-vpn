//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
)

func locationAllowed(path string) bool {
	if path == "" || strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return false
	}
	start := 0
	if strings.Contains(path, ":") {
		if len(path) < 3 || path[1] != ':' || !(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z') || (path[2] != '\\' && path[2] != '/') || strings.Contains(path[2:], ":") {
			return false
		}
		start = 2
	}
	for _, part := range strings.FieldsFunc(path[start:], func(r rune) bool { return r == '\\' || r == '/' }) {
		if part != "." && part != ".." && strings.TrimRight(part, " .") != part {
			return false
		}
		name := strings.ToUpper(part)
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || name == "CONIN$" || name == "CONOUT$" || (len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9') {
			return false
		}
	}
	absolute, e := filepath.Abs(path)
	if e != nil {
		return false
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	root, e := windows.UTF16PtrFromString(volume + `\`)
	if e != nil {
		return false
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK:
		return true
	}
	return false
}
func ordinaryDirectory(path string) bool {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return false
	}
	a, e := windows.GetFileAttributes(p)
	return e == nil && a&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 && a&windows.FILE_ATTRIBUTE_DIRECTORY != 0
}
func openOrdinary(path string) (*os.File, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return nil, invalidInput
	}
	parents := []string{}
	for parent := filepath.Dir(absolute); ; parent = filepath.Dir(parent) {
		parents = append(parents, parent)
		if filepath.Dir(parent) == parent {
			break
		}
	}
	handles := []windows.Handle{}
	defer func() {
		for _, h := range handles {
			windows.CloseHandle(h)
		}
	}()
	for index := len(parents) - 1; index >= 0; index-- {
		p, e := windows.UTF16PtrFromString(parents[index])
		if e != nil {
			return nil, invalidInput
		}
		h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			return nil, invalidInput
		}
		handles = append(handles, h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return nil, invalidInput
		}
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	var info windows.ByHandleFileInformation
	kind, ke := windows.GetFileType(h)
	if windows.GetFileInformationByHandle(h, &info) != nil || ke != nil || kind != windows.FILE_TYPE_DISK || info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, invalidInput
	}
	return os.NewFile(uintptr(h), path), nil
}
