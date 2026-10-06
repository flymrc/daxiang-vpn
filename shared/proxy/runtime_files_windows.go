//go:build windows

package proxy

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/paths"
)

// Administrators and SYSTEM already control this machine. Allow them for the
// elevated TUN engine; all other accounts are excluded, including inherited
// Users/Everyone permissions. Preserve the launching home owner's access.
func runtimeOwner(ctx paths.Context) (*windows.SID, error) {
	sd, err := windows.GetNamedSecurityInfo(ctx.Root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return nil, err
	}
	if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) && !owner.IsWellKnown(windows.WinLocalSystemSid) {
		_, _, kind, err := owner.LookupAccount("")
		if err != nil || kind != windows.SidTypeUser {
			return nil, fmt.Errorf("客户端目录所有者必须是用户、SYSTEM 或 Administrators")
		}
	}
	return owner.Copy()
}

func runtimeDACL(ctx paths.Context, inherit bool) (*windows.ACL, error) {
	owner, err := runtimeOwner(ctx)
	if err != nil {
		return nil, err
	}
	flags := ""
	if inherit {
		flags = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)(A;%s;FA;;;BA)", flags, owner.String(), flags, flags))
	if err != nil {
		return nil, err
	}
	acl, _, err := sd.DACL()
	return acl, err
}

func secureDirectory(ctx paths.Context, path string) error {
	if err := validateRuntimePath(ctx, path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	rootSecurity, err := windows.GetNamedSecurityInfo(ctx.Root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if err := verifyDirectoryMutationACL(ctx, rootSecurity); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("运行目录必须是实际目录")
	}
	// A mount-point junction can look like an ordinary directory to Lstat.
	// Reject any reparse target using a held directory handle; don't follow it
	// and don't apply an ACL to an unrelated external directory.
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &attributes); err != nil {
		return err
	}
	if attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("运行目录不能是 junction 或其他重解析点")
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		return fmt.Errorf("无法验证运行目录的实际路径")
	}
	actual := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(actual, `\\?\UNC\`) {
		actual = `\\` + strings.TrimPrefix(actual, `\\?\UNC\`)
	} else {
		actual = strings.TrimPrefix(actual, `\\?\`)
	}
	root, err := paths.CanonicalRoot(ctx.Root)
	if err != nil {
		return err
	}
	if err := validateResolvedRuntimePath(root, actual); err != nil {
		return err
	}
	initial, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if err := verifyRuntimeOwner(ctx, initial); err != nil {
		return err
	}
	if err := verifyDirectoryMutationACL(ctx, initial); err != nil {
		return err
	}
	// Never rewrite a directory DACL: SetSecurityInfo can propagate inherited
	// ACE removal even when the replacement ACL contains no OI/CI entries,
	// changing external hardlink targets. Validate trusted ownership and
	// mutation rights, reject unsafe directories, and protect each new file
	// explicitly before writing a secret. No 0700 behavior is assumed here.
	return nil
}

func verifyDirectoryMutationACL(ctx paths.Context, sd *windows.SECURITY_DESCRIPTOR) error {
	if err := verifyRuntimeOwner(ctx, sd); err != nil {
		return err
	}
	owner, err := runtimeOwner(ctx)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return fmt.Errorf("客户端目录缺少明确的访问控制")
	}
	allowed := map[string]bool{owner.String(): true, "S-1-5-18": true, "S-1-5-32-544": true}
	const mutationRights = 0x2 | 0x4 | 0x10 | 0x40 | 0x100 | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&0x08 != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("客户端目录包含未支持的权限条目")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || (!allowed[sid.String()] && uint32(ace.Mask)&mutationRights != 0) {
			return fmt.Errorf("客户端目录允许其他账户修改运行对象，请使用用户专属目录")
		}
	}
	return nil
}

func protectFile(ctx paths.Context, file *os.File) error {
	original, err := singleLinkFileInfo(windows.Handle(file.Fd()))
	if err != nil {
		return err
	}
	initial, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if err := verifyRuntimeOwner(ctx, initial); err != nil {
		return err
	}
	acl, err := runtimeDACL(ctx, false)
	if err != nil {
		return err
	}
	// Hold a no-follow WRITE_DAC handle and verify that it still refers to the
	// original single-link file before changing ACLs. Never mutate by pathname.
	name, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	current, err := singleLinkFileInfo(handle)
	if err != nil {
		return err
	}
	if original.VolumeSerialNumber != current.VolumeSerialNumber || original.FileIndexHigh != current.FileIndexHigh || original.FileIndexLow != current.FileIndexLow {
		return fmt.Errorf("运行文件身份在权限设置前已改变")
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return err
	}
	return verifyPrivateFile(ctx, file)
}

func verifyPrivateFile(ctx paths.Context, file *os.File) error {
	if _, err := singleLinkFileInfo(windows.Handle(file.Fd())); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("运行凭据必须是普通文件")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return verifyRuntimeDACL(ctx, sd)
}

func singleLinkFileInfo(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		return info, fmt.Errorf("运行文件必须是非重解析的单链接普通文件")
	}
	return info, nil
}

func openLockFile(path string) (*os.File, error) {
	return openRuntimeFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_ALWAYS, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
}

func openExistingLockFile(path string) (*os.File, error) {
	return openRuntimeFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_EXISTING, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
}

func openPrivateRead(path string) (*os.File, error) {
	return openRuntimeFile(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE)
}

func openRuntimeFile(path string, access, disposition, sharing uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, access, sharing, nil, disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	if _, err := singleLinkFileInfo(handle); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func verifyRuntimeDACL(ctx paths.Context, sd *windows.SECURITY_DESCRIPTOR) error {
	if err := verifyRuntimeOwner(ctx, sd); err != nil {
		return err
	}
	owner, err := runtimeOwner(ctx)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("运行凭据必须禁用继承权限")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("运行凭据缺少受限访问列表")
	}
	allowed := map[string]bool{owner.String(): true, "S-1-5-18": true, "S-1-5-32-544": true}
	seenOwner := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.IsValid() || !allowed[sid.String()] {
			return fmt.Errorf("运行凭据允许未授权账户访问")
		}
		const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
		if sid.Equals(owner) && uint32(ace.Mask)&fileAllAccess == fileAllAccess {
			seenOwner = true
		}
	}
	if !seenOwner {
		return fmt.Errorf("运行凭据未保留客户端目录所有者的访问权限")
	}
	return nil
}

func verifyRuntimeOwner(ctx paths.Context, sd *windows.SECURITY_DESCRIPTOR) error {
	homeOwner, err := runtimeOwner(ctx)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("无法验证运行对象所有者")
	}
	if !owner.Equals(homeOwner) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) && !owner.IsWellKnown(windows.WinLocalSystemSid) {
		return fmt.Errorf("运行对象由其他账户拥有，拒绝读取凭据或修改权限")
	}
	return nil
}
