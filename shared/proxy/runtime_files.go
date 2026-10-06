package proxy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"zongheng-vpn/shared/paths"
)

func validateRuntimePath(ctx paths.Context, path string) error {
	root, err := paths.CanonicalRoot(ctx.Root)
	if err != nil {
		return err
	}
	actual, err := paths.CanonicalRoot(path)
	if err != nil {
		return err
	}
	return validateResolvedRuntimePath(root, actual)
}

func validateResolvedRuntimePath(root, actual string) error {
	relative, err := filepath.Rel(homeIdentity(root), homeIdentity(actual))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("运行目录越过客户端目录边界，拒绝修改权限或运行状态")
	}
	return nil
}

// Runtime secrets are created in an access-controlled directory before data
// is written, then committed atomically. Go mode bits alone are insufficient
// on Windows; protectFile applies and verifies the platform access policy.
func writePrivateFile(ctx paths.Context, path string, data []byte) error {
	if err := secureDirectory(ctx, filepath.Dir(path)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".zhvpn-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := protectFile(ctx, file); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func readPrivateFile(ctx paths.Context, path string) ([]byte, error) {
	if err := validateRuntimePath(ctx, filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("运行状态必须是普通文件")
	}
	file, err := openPrivateRead(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := verifyPrivateFile(ctx, file); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, 1<<20))
}
