package paths

import (
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

	return FromRoot(root), nil
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
	_ = c.RemoveLegacyArtifacts()
	return nil
}

func (c Context) RemoveLegacyArtifacts() error {
	legacyPaths := []string{
		filepath.Join(c.Root, "sing-box"),
		filepath.Join(c.RunDir, "sing-box.pid"),
		filepath.Join(c.LogDir, "sing-box.log"),
		filepath.Join(c.LogDir, "sing-box.err.log"),
		c.SingBoxConfig,
		c.SingBoxLogPath,
		c.SingBoxErrorPath,
	}
	for _, path := range legacyPaths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
