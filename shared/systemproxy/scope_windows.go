//go:build windows

package systemproxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"zongheng-vpn/shared/paths"
)

const applicationID = "com.zongheng.vpn"

type windowsPlatform struct {
	// Test injections are deliberately package-private. The production
	// constructor has no user-selected directory or registry-path override.
	knownFolder func() (string, error)
	keyPath     string
	notify      bool
}

func NewPlatform() (Platform, error) {
	platform := &windowsPlatform{
		knownFolder: func() (string, error) { return windows.KnownFolderPath(windows.FOLDERID_RoamingAppData, 0) },
		keyPath:     internetSettings,
		notify:      true,
	}
	return Platform{Resolver: platform, Transactions: platform, Factory: platform}, nil
}

func currentUserSID() (*windows.SID, error) {
	// HKCU operations in this package are for the process token only. Reject
	// an impersonating thread instead of accidentally using/caching another hive.
	var threadToken windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &threadToken); err == nil {
		threadToken.Close()
		return nil, fmt.Errorf("impersonated thread cannot manage process HKCU")
	} else if err != windows.ERROR_NO_TOKEN {
		return nil, err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) || user.User.Sid.IsWellKnown(windows.WinLocalServiceSid) || user.User.Sid.IsWellKnown(windows.WinNetworkServiceSid) {
		return nil, fmt.Errorf("service accounts cannot claim an interactive user's system proxy")
	}
	return user.User.Sid.Copy()
}

func localPath(path string) bool {
	return fixedLocalPath(path, windows.GetDriveType)
}

func fixedLocalPath(path string, driveType func(*uint16) uint32) bool {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(filepath.VolumeName(path), `\\`) {
		return false
	}
	root, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	return err == nil && driveType(root) == windows.DRIVE_FIXED
}

func (p *windowsPlatform) Resolve(ctx context.Context, home string) (Scope, error) {
	if err := ctx.Err(); err != nil {
		return Scope{}, err
	}
	sid, err := currentUserSID()
	if err != nil {
		return Scope{}, err
	}
	if !localPath(home) {
		return Scope{}, fmt.Errorf("home must use a fixed local drive")
	}
	canonicalHome, err := paths.CanonicalRoot(home)
	if err != nil || !localPath(canonicalHome) {
		return Scope{}, fmt.Errorf("home must be a local canonical directory: %w", err)
	}
	if err := verifyDirectory(canonicalHome, sid); err != nil {
		return Scope{}, fmt.Errorf("home does not belong to the current token SID: %w", err)
	}
	folder, err := p.knownFolder()
	if err != nil || !localPath(folder) {
		return Scope{}, fmt.Errorf("user KnownFolder must be local: %w", err)
	}
	// Resolve aliases for identity, but explicitly reject reparse objects before
	// accepting any existing user directory. Never use environment-selected home.
	if err := verifyDirectory(folder, sid); err != nil {
		return Scope{}, err
	}
	folder, err = paths.CanonicalRoot(folder)
	if err != nil || !localPath(folder) {
		return Scope{}, fmt.Errorf("invalid resolved KnownFolder: %w", err)
	}
	root := filepath.Join(folder, applicationID)
	if _, err := os.Lstat(root); err == nil {
		if err := verifyDirectory(root, sid); err != nil {
			return Scope{}, err
		}
	} else if !os.IsNotExist(err) {
		return Scope{}, err
	}
	return Scope{UserScope: "windows:" + sid.String(), HomeIdentity: strings.ToLower(canonicalHome), Root: root,
		JournalPath: filepath.Join(root, "proxy-backup.json"), LockPath: filepath.Join(root, "proxy-operation.lock")}, nil
}

func scopeSID(scope Scope) (*windows.SID, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	if scope.UserScope != "windows:"+sid.String() {
		return nil, fmt.Errorf("actual token SID differs from original lease user; no HKCU access")
	}
	return sid, nil
}

func (p *windowsPlatform) validateScope(ctx context.Context, scope Scope) (*windows.SID, error) {
	resolved, err := p.Resolve(ctx, scope.HomeIdentity)
	if err != nil {
		return nil, err
	}
	if resolved != scope {
		return nil, fmt.Errorf("scope differs from the original user's fixed KnownFolder")
	}
	return scopeSID(scope)
}

func userSecurity(sid *windows.SID, inherit bool) (*windows.SECURITY_DESCRIPTOR, error) {
	flags := ""
	if inherit {
		flags = "OICI"
	}
	return windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)(A;%s;FA;;;BA)", sid.String(), flags, sid.String(), flags, flags))
}

func securityAttributes(sd *windows.SECURITY_DESCRIPTOR) *windows.SecurityAttributes {
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
}

func verifySecurity(handle windows.Handle, sid *windows.SID, private bool) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(sid) {
		return fmt.Errorf("object owner differs from the original user SID")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("object has no verifiable DACL")
	}
	allowed := map[string]bool{sid.String(): true, "S-1-5-18": true, "S-1-5-32-544": true}
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
			return fmt.Errorf("unsupported object ACL entry")
		}
		account := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !account.IsValid() || (!allowed[account.String()] && (private || uint32(ace.Mask)&mutationRights != 0)) {
			return fmt.Errorf("object is accessible to an unrelated account")
		}
	}
	return nil
}

func verifyDirectory(path string, sid *windows.SID) error {
	handle, err := openUserDirectory(path, sid)
	if err != nil {
		return err
	}
	return windows.CloseHandle(handle)
}

func openUserDirectory(path string, sid *windows.SID) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(handle)
		return 0, fmt.Errorf("user directory must not be a reparse point")
	}
	if err := verifySecurity(handle, sid, false); err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func ensureUserDirectory(path string, sid *windows.SID) error {
	sd, err := userSecurity(sid, true)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(name, securityAttributes(sd)); err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	return verifyDirectory(path, sid)
}
