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
		"log":             filepath.Join(root, "logs", "mihari.log"),
		"staging":         filepath.Join(root, "staging"),
		"subscriptions":   filepath.Join(root, "subscriptions"),
		"catalog":         filepath.Join(root, "subscriptions", "catalog.yaml"),
		"cache":           filepath.Join(root, "subscriptions", "cache"),
		"sub staging":     filepath.Join(root, "staging", "subscriptions"),
		"tui preferences": filepath.Join(root, "preferences", "tui.json"),
	}
	gots := map[string]string{
		"root":            paths.Root,
		"bin":             paths.Bin,
		"core":            paths.CoreBinary,
		"runtime config":  paths.RuntimeConfig,
		"settings":        paths.Settings,
		"log":             paths.Log,
		"staging":         paths.Staging,
		"subscriptions":   paths.Subscriptions,
		"catalog":         paths.SubscriptionCatalog,
		"cache":           paths.SubscriptionCache,
		"sub staging":     paths.SubscriptionStaging,
		"tui preferences": paths.TUIPreferences,
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
