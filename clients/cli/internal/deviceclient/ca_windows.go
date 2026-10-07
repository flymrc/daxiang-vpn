//go:build windows

package deviceclient

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
)

func localCAPath(path string) bool {
	if path == "" || strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return false
	}
	start := 0
	if strings.Contains(path, ":") {
		if len(path) < 3 || path[1] != ':' || !((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) || (path[2] != '\\' && path[2] != '/') || strings.Contains(path[2:], ":") {
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
	absolute, e := filepath.Abs(path)
	if e != nil {
		return false
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	p, e := windows.UTF16PtrFromString(volume + `\`)
	if e != nil {
		return false
	}
	kind := windows.GetDriveType(p)
	return kind == windows.DRIVE_FIXED || kind == windows.DRIVE_REMOVABLE || kind == windows.DRIVE_CDROM || kind == windows.DRIVE_RAMDISK
}
func openCA(path string) (*os.File, error) {
	if !localCAPath(path) {
		return nil, errJSON
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, errJSON
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	var info windows.ByHandleFileInformation
	kind, ke := windows.GetFileType(h)
	if windows.GetFileInformationByHandle(h, &info) != nil || ke != nil || kind != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.FileSizeHigh != 0 || info.FileSizeLow > 1<<20 {
		windows.CloseHandle(h)
		return nil, errJSON
	}
	return os.NewFile(uintptr(h), path), nil
}
