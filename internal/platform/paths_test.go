package platform

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewPathsBuildsRuntimeLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mihari-data")
	paths := NewPaths(root)
	coreName := "mihomo"
	if runtime.GOOS == "windows" {
		coreName += ".exe"
	}
	wants := map[string]string{
		"root":            root,
		"bin":             filepath.Join(root, "bin"),
		"core":            filepath.Join(root, "bin", coreName),
		"runtime config":  filepath.Join(root, "runtime", "config.yaml"),
		"settings":        filepath.Join(root, "mihari.yaml"),
		"onboarding":      filepath.Join(root, "onboarding.json"),
		"log":             filepath.Join(root, "logs", "mihari.log"),
		"staging":         filepath.Join(root, "staging"),
		"subscriptions":   filepath.Join(root, "subscriptions"),
		"catalog":         filepath.Join(root, "subscriptions", "catalog.yaml"),
		"cache":           filepath.Join(root, "subscriptions", "cache"),
		"sub staging":     filepath.Join(root, "staging", "subscriptions"),
		"tui preferences": filepath.Join(root, "preferences", "tui.json"),
		"geoip country":   filepath.Join(root, "geoip", "GeoLite2-Country.mmdb"),
		"geoip asn":       filepath.Join(root, "geoip", "GeoLite2-ASN.mmdb"),
		"geoip staging":   filepath.Join(root, "staging", "geoip"),
		"web root":        filepath.Join(root, "web"),
		"web active":      filepath.Join(root, "web", "active.json"),
		"web credential":  filepath.Join(root, "web", "credential"),
		"panel staging":   filepath.Join(root, "staging", "panels"),
	}
	gots := map[string]string{
		"root":            paths.Root,
		"bin":             paths.Bin,
		"core":            paths.CoreBinary,
		"runtime config":  paths.RuntimeConfig,
		"settings":        paths.Settings,
		"onboarding":      paths.Onboarding,
		"log":             paths.Log,
		"staging":         paths.Staging,
		"subscriptions":   paths.Subscriptions,
		"catalog":         paths.SubscriptionCatalog,
		"cache":           paths.SubscriptionCache,
		"sub staging":     paths.SubscriptionStaging,
		"tui preferences": paths.TUIPreferences,
		"geoip country":   paths.GeoIPCountry,
		"geoip asn":       paths.GeoIPASN,
		"geoip staging":   paths.GeoIPStaging,
		"web root":        paths.WebRoot,
		"web active":      paths.WebActive,
		"web credential":  paths.WebCredential,
		"panel staging":   paths.PanelStaging,
	}
	for name, want := range wants {
		if got := gots[name]; got != want {
			t.Errorf("%s=%q want=%q", name, got, want)
		}
	}
}

func TestDefaultPathsHonorsDataOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "override")
	t.Setenv("MIHARI_DATA", root)
	if got := DefaultPaths(); got.Root != root {
		t.Fatalf("root=%q want=%q", got.Root, root)
	}
}

func TestDefaultDataRootWindowsUsesProgramData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only path policy")
	}
	t.Setenv("MIHARI_DATA", "")
	t.Setenv("ProgramData", `C:\ProgramData`)
	got := defaultDataRoot()
	want := filepath.Join(`C:\ProgramData`, "mihari")
	if got != want {
		t.Fatalf("root=%q want=%q", got, want)
	}
}
