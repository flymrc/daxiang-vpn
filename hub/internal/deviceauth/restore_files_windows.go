//go:build windows

package deviceauth

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func restoreWindowsSID() (*windows.SID, error) {
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		return nil, e
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	if e != nil {
		return nil, e
	}
	return user.User.Sid.Copy()
}
func restoreWindowsSecurity(h windows.Handle, private bool) error {
	sid, err := restoreWindowsSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	allowed := func(s *windows.SID) bool {
		return s != nil && s.IsValid() && (s.Equals(sid) || s.IsWellKnown(windows.WinLocalSystemSid) || s.IsWellKnown(windows.WinBuiltinAdministratorsSid))
	}
	if err != nil || !allowed(owner) {
		return ErrRestorePlan
	}
	control, _, err := sd.Control()
	if err != nil || (private && control&windows.SE_DACL_PROTECTED == 0) {
		return ErrRestorePlan
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return ErrRestorePlan
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			return ErrRestorePlan
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return ErrRestorePlan
		}
		who := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !who.IsValid() {
			return ErrRestorePlan
		}
		const ownershipMutation = windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_ALL
		if !allowed(who) && (private || uint32(ace.Mask)&ownershipMutation != 0) {
			return ErrRestorePlan
		}
	}
	return nil
}
func restoreWindowsOpen(path string, dir bool) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	access := uint32(windows.GENERIC_READ | windows.READ_CONTROL)
	sharing := uint32(windows.FILE_SHARE_READ)
	if dir {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
		access = windows.READ_CONTROL | windows.FILE_READ_ATTRIBUTES
		sharing |= windows.FILE_SHARE_WRITE
	}
	h, err := windows.CreateFile(p, access, sharing, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (dir != (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0)) || (!dir && info.NumberOfLinks != 1) {
		windows.CloseHandle(h)
		return nil, ErrRestorePlan
	}
	return os.NewFile(uintptr(h), path), nil
}
func restoreOpenPrivate(path string) (*os.File, []*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil || strings.HasPrefix(abs, `\\`) {
		return nil, nil, ErrRestorePlan
	}
	volume := filepath.VolumeName(abs)
	p, _ := windows.UTF16PtrFromString(volume + `\`)
	if windows.GetDriveType(p) != windows.DRIVE_FIXED {
		return nil, nil, ErrRestorePlan
	}
	chain := []string{}
	for parent := filepath.Dir(abs); ; parent = filepath.Dir(parent) {
		chain = append(chain, parent)
		if filepath.Dir(parent) == parent {
			break
		}
	}
	parents := []*os.File{}
	fail := func() (*os.File, []*os.File, error) {
		for _, f := range parents {
			f.Close()
		}
		return nil, nil, ErrRestorePlan
	}
	// Pin root -> leaf without share-delete, before trusting any descendant.
	for i := len(chain) - 1; i >= 0; i-- {
		f, e := restoreWindowsOpen(chain[i], true)
		if e != nil {
			return fail()
		}
		parents = append(parents, f)
		if restoreWindowsSecurity(windows.Handle(f.Fd()), i == 0) != nil {
			return fail()
		}
	}
	f, err := restoreWindowsOpen(abs, false)
	if err != nil {
		return fail()
	}
	r := &restoreInput{file: f, parents: parents}
	r.info, err = f.Stat()
	if err != nil || restoreVerifyPinned(r) != nil {
		f.Close()
		return fail()
	}
	return f, parents, nil
}
func restoreVerifyPinned(r *restoreInput) error {
	h := windows.Handle(r.file.Fd())
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || restoreWindowsSecurity(h, true) != nil {
		return restoreFailure("unsafe_pinned_input")
	}
	held, err := r.file.Stat()
	current, e := os.Lstat(r.file.Name())
	if err != nil || e != nil || !os.SameFile(r.info, held) || !os.SameFile(held, current) || r.info.Size() != held.Size() || !r.info.ModTime().Equal(held.ModTime()) {
		return restoreFailure("input_identity_changed")
	}
	for i, f := range r.parents {
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || restoreWindowsSecurity(windows.Handle(f.Fd()), i == len(r.parents)-1) != nil {
			return restoreFailure("input_ancestor_changed")
		}
	}
	return nil
}
func restoreWindowsSD() (*windows.SECURITY_DESCRIPTOR, error) {
	sid, e := restoreWindowsSID()
	if e != nil {
		return nil, e
	}
	return windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
}
func restoreOwnedCopy(parent string, b []byte) (string, func(), error) {
	id, err := opaque()
	if err != nil {
		return "", nil, ErrRestorePlan
	}
	dir := filepath.Join(parent, ".zhvpn-restore-read-"+id)
	sd, err := restoreWindowsSD()
	if err != nil {
		return "", nil, ErrRestorePlan
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	dirName, _ := windows.UTF16PtrFromString(dir)
	if windows.CreateDirectory(dirName, &sa) != nil {
		return "", nil, restoreFailure("private_copy_directory")
	}
	path := filepath.Join(dir, "snapshot.sqlite")
	cleanup := func() { os.Remove(path); os.Remove(dir) }
	name, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.GENERIC_READ, windows.FILE_SHARE_READ, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		cleanup()
		return "", nil, restoreFailure("private_copy_file")
	}
	f := os.NewFile(uintptr(h), path)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil || ce != nil {
		cleanup()
		return "", nil, restoreFailure("private_copy_write")
	}
	return path, cleanup, nil
}
