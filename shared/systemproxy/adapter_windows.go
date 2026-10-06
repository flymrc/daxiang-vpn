//go:build windows

package systemproxy

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"syscall"
	"unsafe"
)

const internetSettings = `SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings`

type windowsAdapter struct {
	scope   Scope
	keyPath string
	notify  bool
}

func (p *windowsPlatform) Open(scope Scope) (Adapter, error) {
	if _, err := p.validateScope(context.Background(), scope); err != nil {
		return nil, err
	}
	return &windowsAdapter{scope: scope, keyPath: p.keyPath, notify: p.notify}, nil
}

func allowedField(name string) bool {
	for _, field := range Fields {
		if name == field {
			return true
		}
	}
	return false
}

func (a *windowsAdapter) Read(name string) (*Value, error) {
	if !allowedField(name) {
		return nil, fmt.Errorf("registry field is outside the proxy allowlist")
	}
	if _, err := scopeSID(a.scope); err != nil {
		return nil, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, a.keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	n, kind, err := key.GetValue(name, nil)
	if err == registry.ErrNotExist {
		return nil, nil
	}
	if err != nil && err != registry.ErrShortBuffer {
		return nil, err
	}
	if n < 0 || n > 1<<20 {
		return nil, fmt.Errorf("registry value exceeds safety bound")
	}
	data := make([]byte, n)
	n, kind, err = key.GetValue(name, data)
	if err != nil {
		return nil, err
	}
	return &Value{Kind: kind, Bytes: data[:n]}, nil
}

func (a *windowsAdapter) Write(name string, value *Value) error {
	if !allowedField(name) {
		return fmt.Errorf("registry field is outside the proxy allowlist")
	}
	if _, err := scopeSID(a.scope); err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, a.keyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if value == nil {
		err := key.DeleteValue(name)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	if value.Kind > 11 || len(value.Bytes) > 1<<20 {
		return fmt.Errorf("raw registry value is unsupported")
	}
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var data *byte
	if len(value.Bytes) > 0 {
		data = &value.Bytes[0]
	}
	procedure := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW")
	result, _, _ := procedure.Call(uintptr(key), uintptr(unsafe.Pointer(name16)), 0,
		uintptr(value.Kind), uintptr(unsafe.Pointer(data)), uintptr(len(value.Bytes)))
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}

func (a *windowsAdapter) Notify() error {
	if _, err := scopeSID(a.scope); err != nil {
		return err
	}
	if !a.notify {
		return nil
	} // only package-private synthetic adapter tests.
	procedure := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	for _, option := range []uintptr{39, 37} {
		result, _, err := procedure.Call(0, option, 0, 0)
		if result == 0 {
			return fmt.Errorf("WinINET option %d failed: %w", option, err)
		}
	}
	return nil
}
