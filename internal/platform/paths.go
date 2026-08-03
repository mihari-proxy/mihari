package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	Root                string
	Bin                 string
	CoreBinary          string
	RuntimeConfig       string
	Settings            string
	Log                 string
	Staging             string
	Subscriptions       string
	SubscriptionCatalog string
	SubscriptionCache   string
	SubscriptionStaging string
	TUIPreferences      string
}

func NewPaths(root string) Paths {
	coreName := "mihomo"
	if runtime.GOOS == "windows" {
		coreName += ".exe"
	}
	return Paths{
		Root:                root,
		Bin:                 filepath.Join(root, "bin"),
		CoreBinary:          filepath.Join(root, "bin", coreName),
		RuntimeConfig:       filepath.Join(root, "runtime", "config.yaml"),
		Settings:            filepath.Join(root, "mihari.yaml"),
		Log:                 filepath.Join(root, "logs", "mihari.log"),
		Staging:             filepath.Join(root, "staging"),
		Subscriptions:       filepath.Join(root, "subscriptions"),
		SubscriptionCatalog: filepath.Join(root, "subscriptions", "catalog.yaml"),
		SubscriptionCache:   filepath.Join(root, "subscriptions", "cache"),
		SubscriptionStaging: filepath.Join(root, "staging", "subscriptions"),
		TUIPreferences:      filepath.Join(root, "preferences", "tui.json"),
	}
}

func DefaultPaths() Paths {
	if root := os.Getenv("MIHARI_DATA"); root != "" {
		return NewPaths(root)
	}
	return NewPaths(defaultDataRoot())
}

func defaultDataRoot() string {
	if runtime.GOOS == "linux" {
		if root := os.Getenv("XDG_DATA_HOME"); root != "" {
			return filepath.Join(root, "mihari")
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "mihari")
		}
	}
	if root, err := os.UserConfigDir(); err == nil {
		return filepath.Join(root, "mihari")
	}
	return filepath.Join(os.TempDir(), "mihari")
}

func (p Paths) EnsureDirs() error {
	for _, path := range []string{p.Root, p.Bin, filepath.Dir(p.RuntimeConfig), filepath.Dir(p.Log), p.Staging, p.Subscriptions, p.SubscriptionCache, p.SubscriptionStaging, filepath.Dir(p.TUIPreferences)} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}
