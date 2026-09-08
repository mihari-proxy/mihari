package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Paths is the on-disk layout under a single data root (default: $HOME/.mihari).
type Paths struct {
	Root                string
	ControlToken        string
	Bin                 string
	CoreBinary          string
	RuntimeConfig       string
	Settings            string
	Onboarding          string
	LogDir              string
	DaemonLog           string
	TUILog              string
	MihomoLog           string
	LogExportDir        string
	Staging             string
	Subscriptions       string
	SubscriptionCatalog string
	SubscriptionCache   string
	SubscriptionStaging string
	TUIPreferences      string
	GeoIPCountry        string
	GeoIPASN            string
	GeoIPStaging        string
	WebRoot             string
	WebActive           string
	WebCredential       string
	PanelStaging        string
}

type pathsJoinFunc func(...string) string

func buildPaths(root string, join pathsJoinFunc, coreName string) Paths {
	return Paths{
		Root:                root,
		ControlToken:        join(root, "control.token"),
		Bin:                 join(root, "bin"),
		CoreBinary:          join(root, "bin", coreName),
		RuntimeConfig:       join(root, "runtime", "config.yaml"),
		Settings:            join(root, "mihari.yaml"),
		Onboarding:          join(root, "onboarding.json"),
		LogDir:              join(root, "logs"),
		DaemonLog:           join(root, "logs", "mihari-daemon.log"),
		TUILog:              join(root, "logs", "mihari-tui.log"),
		MihomoLog:           join(root, "logs", "mihomo.log"),
		LogExportDir:        join(root, "logs-export"),
		Staging:             join(root, "staging"),
		Subscriptions:       join(root, "subscriptions"),
		SubscriptionCatalog: join(root, "subscriptions", "catalog.yaml"),
		SubscriptionCache:   join(root, "subscriptions", "cache"),
		SubscriptionStaging: join(root, "staging", "subscriptions"),
		TUIPreferences:      join(root, "preferences", "tui.json"),
		GeoIPCountry:        join(root, "geoip", "GeoLite2-Country.mmdb"),
		GeoIPASN:            join(root, "geoip", "GeoLite2-ASN.mmdb"),
		GeoIPStaging:        join(root, "staging", "geoip"),
		WebRoot:             join(root, "web"),
		WebActive:           join(root, "web", "active.json"),
		WebCredential:       join(root, "web", "credential"),
		PanelStaging:        join(root, "staging", "panels"),
	}
}

// NewPaths builds the standard layout under root.
func NewPaths(root string) Paths {
	coreName := "mihomo"
	if runtime.GOOS == "windows" {
		coreName += ".exe"
	}
	return buildPaths(root, filepath.Join, coreName)
}

// Absolute resolves Root with filepath.Abs/Clean and rebuilds every derived field
// via NewPaths. It does not EvalSymlinks; callers that need a trusted data-root
// identity must open NewPrivateFS on the absolute Root before EnsureDirs or
// default token IO. --help/--version must not call Absolute. root/LocalSystem
// missing-root failures fail closed with zero directory IO and must not be
// written into CLI SetupError; TUI may continue with PrivateFS=nil, daemon must
// not continue directory IO. Callers reuse one process-level PrivateFS and must
// not call NewPrivateFS again after EnsureDirs/Settings.
func (p Paths) Absolute() (Paths, error) {
	absRoot, err := filepath.Abs(p.Root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve absolute data root: %w", err)
	}
	return NewPaths(filepath.Clean(absRoot)), nil
}

// DefaultDataRoot returns MIHARI_DATA when set, otherwise the platform default
// data root under the current user's home directory ($HOME/.mihari /
// %USERPROFILE%\.mihari).
func DefaultDataRoot() string {
	if root := os.Getenv("MIHARI_DATA"); root != "" {
		return root
	}
	return defaultDataRoot()
}

// AbsoluteDataRoot returns DefaultDataRoot as a cleaned absolute path.
// Used when installing the OS service so LocalSystem/root inherits a fixed path
// instead of resolving systemprofile or /root home.
func AbsoluteDataRoot() (string, error) {
	root := DefaultDataRoot()
	if filepath.IsAbs(root) {
		return filepath.Clean(root), nil
	}
	return filepath.Abs(root)
}

// DefaultPaths returns the layout under DefaultDataRoot.
func DefaultPaths() Paths {
	return NewPaths(DefaultDataRoot())
}

func defaultDataRoot() string {
	// Single cross-platform convention: <user home>/.mihari
	// Service installs pin MIHARI_DATA to the installer's absolute home path so
	// LocalSystem/root daemons share the same tree as the desktop client.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".mihari")
	}
	return filepath.Join(os.TempDir(), "mihari")
}

// EnsureDirs creates the durable directories under this layout.
// It may pre-create LogDir with MkdirAll but does not harden permissions or
// reject intermediate symlinks, and it does not create LogExportDir. Callers
// that need a local data root must establish NewPrivateFS(absolutePaths.Root)
// before EnsureDirs or default token IO; EnsureDirs is not the root create or
// harden entrypoint. The process-level PrivateFS from that call is reused by
// daemon/TUI; do not call NewPrivateFS again after EnsureDirs/Settings.
func (p Paths) EnsureDirs() error {
	for _, path := range []string{
		p.Root, p.Bin, filepath.Dir(p.RuntimeConfig), p.LogDir, p.Staging,
		p.Subscriptions, p.SubscriptionCache, p.SubscriptionStaging,
		filepath.Dir(p.TUIPreferences), filepath.Dir(p.GeoIPCountry), p.GeoIPStaging,
		p.WebRoot, p.PanelStaging,
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}
