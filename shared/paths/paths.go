package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Context struct {
	Root             string
	ConfigPath       string
	SingBoxConfig    string
	RunDir           string
	BinDir           string
	SingBoxPath      string
	WireGuardKeyPath string
	PIDPath          string
	LogDir           string
	SingBoxLogPath   string
	SingBoxErrorPath string
}

func NewContext() (Context, error) {
	root := os.Getenv("ZHVPN_HOME")
	if root == "" {
		var err error
		root, err = defaultRoot()
		if err != nil {
			return Context{}, err
		}
	}

	root, err := CanonicalRoot(root)
	if err != nil {
		return Context{}, err
	}
	return FromRoot(root), nil
}

// CanonicalRoot resolves aliases even when the final application directory has
// not been created yet. It preserves the path's actual casing for filesystem
// access; platform locks may compare Windows identities case-insensitively.
func CanonicalRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("客户端目录不能为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析客户端目录失败：%w", err)
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("客户端目录的现有祖先不是目录：%s", current)
			}
			resolved, err := canonicalExistingRoot(current)
			if err != nil {
				return "", fmt.Errorf("解析客户端目录别名失败：%w", err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("读取客户端目录失败：%w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("客户端目录没有可访问的现有祖先：%w", err)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// FromRoot builds a Context rooted at an explicit directory. Used to pass the
// resolved root to the (possibly elevated) engine child via the --home flag.
func FromRoot(root string) Context {
	return Context{
		Root:             root,
		ConfigPath:       filepath.Join(root, "config.yaml"),
		SingBoxConfig:    filepath.Join(root, "runtime", "session.json"),
		RunDir:           filepath.Join(root, "run"),
		BinDir:           filepath.Join(root, "bin"),
		SingBoxPath:      filepath.Join(root, "bin", clientBinaryName()),
		WireGuardKeyPath: filepath.Join(root, "wireguard", "client.key"),
		PIDPath:          filepath.Join(root, "run", "zhvpn.pid"),
		LogDir:           filepath.Join(root, "logs"),
		SingBoxLogPath:   filepath.Join(root, "logs", "zhvpn.log"),
		SingBoxErrorPath: filepath.Join(root, "logs", "zhvpn.err.log"),
	}
}

func (c Context) EnsureDirs() error {
	for _, dir := range []string{
		c.Root,
		filepath.Dir(c.SingBoxConfig),
		c.RunDir,
		c.BinDir,
		c.LogDir,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func (c Context) RemoveLegacyArtifacts() error {
	legacyPaths := []string{
		filepath.Join(c.Root, "sing-box"),
		filepath.Join(c.RunDir, "sing-box.pid"),
		filepath.Join(c.LogDir, "sing-box.log"),
		filepath.Join(c.LogDir, "sing-box.err.log"),
	}
	for _, path := range legacyPaths {
		// Legacy cleanup is explicit and limited to files. Initialization must
		// never unlink the active session configuration or an open engine log.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
