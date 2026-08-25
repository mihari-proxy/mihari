package platform

import (
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
	Log                 string
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
	MihariChannel       string
}

// NewPaths builds the standard layout under root.
func NewPaths(root string) Paths {
	coreName := "mihomo"
	if runtime.GOOS == "windows" {
		coreName += ".exe"
	}
	return Paths{
		Root:                root,
		ControlToken:        filepath.Join(root, "control.token"),
		Bin:                 filepath.Join(root, "bin"),
		CoreBinary:          filepath.Join(root, "bin", coreName),
		RuntimeConfig:       filepath.Join(root, "runtime", "config.yaml"),
		Settings:            filepath.Join(root, "mihari.yaml"),
		Onboarding:          filepath.Join(root, "onboarding.json"),
		Log:                 filepath.Join(root, "logs", "mihari.log"),
		Staging:             filepath.Join(root, "staging"),
		Subscriptions:       filepath.Join(root, "subscriptions"),
		SubscriptionCatalog: filepath.Join(root, "subscriptions", "catalog.yaml"),
		SubscriptionCache:   filepath.Join(root, "subscriptions", "cache"),
		SubscriptionStaging: filepath.Join(root, "staging", "subscriptions"),
		TUIPreferences:      filepath.Join(root, "preferences", "tui.json"),
		GeoIPCountry:        filepath.Join(root, "geoip", "GeoLite2-Country.mmdb"),
		GeoIPASN:            filepath.Join(root, "geoip", "GeoLite2-ASN.mmdb"),
		GeoIPStaging:        filepath.Join(root, "staging", "geoip"),
		WebRoot:             filepath.Join(root, "web"),
		WebActive:           filepath.Join(root, "web", "active.json"),
		WebCredential:       filepath.Join(root, "web", "credential"),
		PanelStaging:        filepath.Join(root, "staging", "panels"),
		MihariChannel:       filepath.Join(root, "mihari-channel"),
	}
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
func (p Paths) EnsureDirs() error {
	for _, path := range []string{
		p.Root, p.Bin, filepath.Dir(p.RuntimeConfig), filepath.Dir(p.Log), p.Staging,
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
