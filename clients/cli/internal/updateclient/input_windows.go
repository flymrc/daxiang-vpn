//go:build windows

package updateclient

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
)

// Pure syntax check runs before Lstat or any path access: UNC, NT/device paths,
// DOS device names, streams and drive-relative aliases are not public inputs.
func ordinaryWindowsInputPath(path string) bool {
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
	for _, part := range strings.FieldsFunc(path[start:], func(c rune) bool { return c == '\\' || c == '/' }) {
		name := strings.ToUpper(strings.TrimRight(part, " ."))
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || name == "CONIN$" || name == "CONOUT$" || (len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9') {
			return false
		}
	}
	return true
}

func inputLocationAllowed(path string) bool {
	if !ordinaryWindowsInputPath(path) {
		return false
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
	kind := windows.GetDriveType(root)
	return kind == windows.DRIVE_FIXED || kind == windows.DRIVE_REMOVABLE || kind == windows.DRIVE_CDROM || kind == windows.DRIVE_RAMDISK
}

func openRegularNoFollow(path string) (*os.File, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	var info windows.ByHandleFileInformation
	kind, kindErr := windows.GetFileType(h)
	if windows.GetFileInformationByHandle(h, &info) != nil || kindErr != nil || kind != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, fail("invalid_input_file")
	}
	return os.NewFile(uintptr(h), path), nil
}
