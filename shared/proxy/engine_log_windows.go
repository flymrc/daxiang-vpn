//go:build windows

package proxy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/paths"
)

type engineLogDirectory struct {
	file   *os.File
	path   string
	info   windows.ByHandleFileInformation
	strict bool
	home   bool
}
type engineLogNamespace struct {
	ctx          paths.Context
	held         []engineLogDirectory
	createdFiles map[*os.File]bool
}

func logNamespaceError() error { return errors.New(string(EngineLogCodeNamespace)) }
func engineLogSA(ctx paths.Context, directory bool) (*windows.SecurityAttributes, error) {
	owner, e := runtimeOwner(ctx)
	if e != nil {
		return nil, e
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;" + flags + ";FA;;;" + owner.String() + ")(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)")
	if e != nil {
		return nil, e
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func logWindowsDir(path string) (*os.File, windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, info, e
	}
	h, e := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, info, e
	}
	if e = windows.GetFileInformationByHandle(h, &info); e != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		windows.CloseHandle(h)
		return nil, info, logNamespaceError()
	}
	return os.NewFile(uintptr(h), path), info, nil
}
func openEngineLogNamespace(ctx paths.Context) (_ *engineLogNamespace, fresh bool, result error) {
	if ctx.Root == "" || !filepath.IsAbs(ctx.Root) || filepath.Clean(ctx.Root) != ctx.Root || filepath.Clean(ctx.LogDir) != filepath.Join(ctx.Root, "logs") || strings.HasPrefix(ctx.Root, `\\`) {
		return nil, false, logNamespaceError()
	}
	n := &engineLogNamespace{ctx: ctx, createdFiles: make(map[*os.File]bool)}
	defer func() {
		if result != nil {
			n.close()
		}
	}()
	volume := filepath.VolumeName(ctx.Root)
	current := volume + string(filepath.Separator)
	f, info, e := logWindowsDir(current)
	if e != nil {
		return nil, false, e
	}
	n.held = append(n.held, engineLogDirectory{file: f, path: current, info: info})
	for _, part := range strings.Split(strings.TrimPrefix(ctx.Root, current), string(filepath.Separator)) {
		if part == "" {
			return nil, false, logNamespaceError()
		}
		current = filepath.Join(current, part)
		f, info, e = logWindowsDir(current)
		if e != nil {
			return nil, false, e
		}
		n.held = append(n.held, engineLogDirectory{file: f, path: current, info: info, home: strings.EqualFold(current, ctx.Root)})
	}
	// Pure validation only. Existing home/log ACLs are never rewritten.
	if e = n.valid(); e != nil {
		return nil, false, e
	}
	sa, e := engineLogSA(ctx, true)
	if e != nil {
		return nil, false, e
	}
	for _, part := range []string{"logs", "engine-events-v1"} {
		current = filepath.Join(current, part)
		name, e := windows.UTF16PtrFromString(current)
		if e != nil {
			return nil, false, e
		}
		created := windows.CreateDirectory(name, sa) == nil
		f, info, e = logWindowsDir(current)
		if e != nil {
			return nil, false, e
		}
		strict := part == "engine-events-v1"
		n.held = append(n.held, engineLogDirectory{file: f, path: current, info: info, strict: strict, home: true})
		if strict {
			fresh = created
		}
		if e = n.valid(); e != nil {
			return nil, false, e
		}
	}
	return n, fresh, nil
}
func sameLogWindowsID(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}
func (n *engineLogNamespace) valid() error {
	for _, d := range n.held {
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(windows.Handle(d.file.Fd()), &info) != nil || !sameLogWindowsID(d.info, info) || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return logNamespaceError()
		}
		// All directories are held without FILE_SHARE_DELETE, preventing a
		// path replacement while the writer can access its descendant files.
		if d.home {
			sd, e := windows.GetSecurityInfo(windows.Handle(d.file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
			if e != nil {
				return e
			}
			if d.strict {
				e = verifyRuntimeDACL(n.ctx, sd)
			} else {
				e = verifyDirectoryMutationACL(n.ctx, sd)
			}
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func (n *engineLogNamespace) leaf() *os.File { return n.held[len(n.held)-1].file }
func (n *engineLogNamespace) open(name string, create bool) (*os.File, error) {
	if n.valid() != nil {
		return nil, logNamespaceError()
	}
	path := filepath.Join(n.leaf().Name(), name)
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	sa, e := engineLogSA(n.ctx, false)
	if e != nil {
		return nil, e
	}
	sharing := uint32(windows.FILE_SHARE_READ)
	if name == "owner.v1" {
		sharing |= windows.FILE_SHARE_WRITE
	}
	var h windows.Handle
	created := false
	if create {
		h, e = windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, sharing, sa, windows.CREATE_NEW, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e == nil {
			created = true
		}
	}
	if !create || errors.Is(e, windows.ERROR_FILE_EXISTS) || errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		h, e = windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, sharing, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(h), path)
	if e = n.verify(f, name); e != nil {
		f.Close()
		return nil, e
	}
	n.createdFiles[f] = created
	return f, nil
}
func (n *engineLogNamespace) created(f *os.File) bool { return n.createdFiles[f] }
func (n *engineLogNamespace) verify(f *os.File, name string) error {
	if n.valid() != nil {
		return logNamespaceError()
	}
	original, e := singleLinkFileInfo(windows.Handle(f.Fd()))
	if e != nil {
		return e
	}
	if e = verifyPrivateFile(n.ctx, f); e != nil {
		return e
	}
	p, e := windows.UTF16PtrFromString(filepath.Join(n.leaf().Name(), name))
	if e != nil {
		return e
	}
	h, e := windows.CreateFile(p, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	actual, e := singleLinkFileInfo(h)
	if e != nil || !sameLogWindowsID(original, actual) {
		return logNamespaceError()
	}
	return nil
}
func (n *engineLogNamespace) checkEntries() error {
	if n.valid() != nil {
		return logNamespaceError()
	}
	// Windows directory os.File.ReadDir uses its pathname. The held directory
	// and every ancestor forbid deletion/replacement for this writer lifetime.
	f, e := os.Open(n.leaf().Name())
	if e != nil {
		return e
	}
	defer f.Close()
	names, e := f.Readdirnames(EngineLogSlots + 2)
	if e != nil && !errors.Is(e, io.EOF) {
		return e
	}
	if len(names) > EngineLogSlots+1 {
		return logNamespaceError()
	}
	for _, name := range names {
		valid := name == "owner.v1"
		for i := 0; i < EngineLogSlots; i++ {
			valid = valid || name == engineLogSlotName(i)
		}
		if !valid {
			return logNamespaceError()
		}
	}
	return nil
}
func (n *engineLogNamespace) close() {
	for i := len(n.held) - 1; i >= 0; i-- {
		n.held[i].file.Close()
	}
	n.held = nil
}
