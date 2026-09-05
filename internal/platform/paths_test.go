package platform

import (
	"os"
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
		"control token":   filepath.Join(root, "control.token"),
		"bin":             filepath.Join(root, "bin"),
		"core":            filepath.Join(root, "bin", coreName),
		"runtime config":  filepath.Join(root, "runtime", "config.yaml"),
		"settings":        filepath.Join(root, "mihari.yaml"),
		"onboarding":      filepath.Join(root, "onboarding.json"),
		"log dir":         filepath.Join(root, "logs"),
		"daemon log":      filepath.Join(root, "logs", "mihari-daemon.log"),
		"tui log":         filepath.Join(root, "logs", "mihari-tui.log"),
		"mihomo log":      filepath.Join(root, "logs", "mihomo.log"),
		"log export dir":  filepath.Join(root, "logs-export"),
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
		"control token":   paths.ControlToken,
		"bin":             paths.Bin,
		"core":            paths.CoreBinary,
		"runtime config":  paths.RuntimeConfig,
		"settings":        paths.Settings,
		"onboarding":      paths.Onboarding,
		"log dir":         paths.LogDir,
		"daemon log":      paths.DaemonLog,
		"tui log":         paths.TUILog,
		"mihomo log":      paths.MihomoLog,
		"log export dir":  paths.LogExportDir,
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
	if got := DefaultDataRoot(); got != root {
		t.Fatalf("DefaultDataRoot=%q want=%q", got, root)
	}
	if got := DefaultPaths().ControlToken; got != filepath.Join(root, "control.token") {
		t.Fatalf("token=%q", got)
	}
}

func TestDefaultDataRootUsesHomeDotMihari(t *testing.T) {
	t.Setenv("MIHARI_DATA", "")
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("UserHomeDir unavailable")
	}
	got := defaultDataRoot()
	want := filepath.Join(home, ".mihari")
	if got != want {
		t.Fatalf("root=%q want=%q", got, want)
	}
	// DefaultPaths without override must match.
	if root := DefaultPaths().Root; root != want {
		t.Fatalf("DefaultPaths.Root=%q want=%q", root, want)
	}
}

func TestAbsoluteDataRootResolvesRelativeAndAbsolute(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "abs-data")
	t.Setenv("MIHARI_DATA", abs)
	got, err := AbsoluteDataRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(abs) {
		t.Fatalf("got=%q want=%q", got, abs)
	}

	rel := filepath.Join(".", "rel-mihari-data")
	t.Setenv("MIHARI_DATA", rel)
	got, err = AbsoluteDataRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
	wantAbs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantAbs {
		t.Fatalf("got=%q want=%q", got, wantAbs)
	}
}

func TestEnsureDirsCreatesDurableLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ensure-root")
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		paths.Root,
		paths.Bin,
		filepath.Dir(paths.RuntimeConfig),
		paths.LogDir,
		paths.Staging,
		paths.Subscriptions,
		paths.SubscriptionCache,
		paths.SubscriptionStaging,
		filepath.Dir(paths.TUIPreferences),
		filepath.Dir(paths.GeoIPCountry),
		paths.GeoIPStaging,
		paths.WebRoot,
		paths.PanelStaging,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
}

func TestEnsureDirsLoggingLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ensure-logging")
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths.LogDir)
	if err != nil {
		t.Fatalf("LogDir missing: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("LogDir %q is not a directory", paths.LogDir)
	}
	if _, err := os.Stat(paths.LogExportDir); !os.IsNotExist(err) {
		t.Fatalf("LogExportDir %q should be absent, err=%v", paths.LogExportDir, err)
	}
}

func TestPathsAbsoluteRebuildsDerivedFields(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	relative := NewPaths(filepath.Join(".", "relative-data"))
	abs, err := relative.Absolute()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(abs.Root) {
		t.Fatalf("Root=%q want absolute", abs.Root)
	}
	wantRoot, err := filepath.Abs(filepath.Join(".", "relative-data"))
	if err != nil {
		t.Fatal(err)
	}
	wantRoot = filepath.Clean(wantRoot)
	if abs.Root != wantRoot {
		t.Fatalf("Root=%q want=%q", abs.Root, wantRoot)
	}
	want := map[string]string{
		"LogDir":       filepath.Join(wantRoot, "logs"),
		"DaemonLog":    filepath.Join(wantRoot, "logs", "mihari-daemon.log"),
		"TUILog":       filepath.Join(wantRoot, "logs", "mihari-tui.log"),
		"MihomoLog":    filepath.Join(wantRoot, "logs", "mihomo.log"),
		"LogExportDir": filepath.Join(wantRoot, "logs-export"),
	}
	gots := map[string]string{
		"LogDir":       abs.LogDir,
		"DaemonLog":    abs.DaemonLog,
		"TUILog":       abs.TUILog,
		"MihomoLog":    abs.MihomoLog,
		"LogExportDir": abs.LogExportDir,
	}
	for name, wantPath := range want {
		if got := gots[name]; got != wantPath {
			t.Errorf("%s=%q want=%q", name, got, wantPath)
		}
	}
	if relative.Root == abs.Root {
		t.Fatalf("Absolute mutated original Root: %q", relative.Root)
	}
	if relative.LogDir == abs.LogDir {
		t.Fatalf("Absolute mutated original LogDir: %q", relative.LogDir)
	}

	absoluteRoot := filepath.Join(t.TempDir(), "abs-data")
	original := NewPaths(absoluteRoot)
	roundTrip, err := original.Absolute()
	if err != nil {
		t.Fatal(err)
	}
	wantAbs := NewPaths(filepath.Clean(absoluteRoot))
	if roundTrip != wantAbs {
		t.Fatalf("absolute round-trip=%#v want=%#v", roundTrip, wantAbs)
	}
	if original.Root != absoluteRoot {
		t.Fatalf("Absolute mutated absolute original Root: got=%q want=%q", original.Root, absoluteRoot)
	}
}

func TestPathsAbsolute_PreservesLegacySingleRoot(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	got, err := NewPaths("portable").Absolute()
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(cwd, "portable")
	if got.Root != wantRoot || got.ControlToken != filepath.Join(wantRoot, "control.token") || got.TUILog != filepath.Join(wantRoot, "logs", "mihari-tui.log") {
		t.Fatalf("Absolute changed legacy single-root layout: %+v", got)
	}
}
