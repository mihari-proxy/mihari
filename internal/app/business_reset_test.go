package app

import (
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBusinessReset_PreservesResourcesAndRotatesManagementSecrets(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	saved := map[string]string{paths.SubscriptionCache + string(os.PathSeparator) + strings.Repeat("a", 32) + ".yaml": "old subscription cache", filepath.Join(paths.Root, "runtime", "core-home", "cache.db"): "old node selections", filepath.Join(paths.WebRoot, "panels", "fixture", "index.html"): "keep panel", filepath.Join(paths.LogExportDir, "fixture.zip"): "keep export", paths.Settings: "old settings", paths.SubscriptionCatalog: "old catalog", paths.RuntimeConfig: "old runtime", paths.TUIPreferences: "old preferences", paths.WebActive: "old panel selection", paths.WebCredential: "old credential", paths.ControlToken: "keep control", paths.CoreBinary: "keep core", paths.GeoIPCountry: "keep geo", paths.DaemonLog: "keep log", filepath.Join(paths.Root, "unrelated.txt"): "keep unknown"}
	for path, value := range saved {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b := BusinessReset{Paths: paths}
	preview, err := b.Preview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings, web, err := b.Reset(t.Context(), preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ControllerSecret == "" || web == "" || web == "old credential" {
		t.Fatal("management credentials were not rotated")
	}
	for _, path := range []string{paths.SubscriptionCatalog, paths.RuntimeConfig, paths.TUIPreferences, paths.WebActive, paths.SubscriptionCache + string(os.PathSeparator) + strings.Repeat("a", 32) + ".yaml", filepath.Join(paths.Root, "runtime", "core-home", "cache.db")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("not cleared: %s: %v", path, err)
		}
	}
	for _, path := range []string{paths.ControlToken, paths.CoreBinary, paths.GeoIPCountry, paths.DaemonLog, filepath.Join(paths.Root, "unrelated.txt"), filepath.Join(paths.WebRoot, "panels", "fixture", "index.html"), filepath.Join(paths.LogExportDir, "fixture.zip")} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != saved[path] {
			t.Fatalf("preserved target changed: %s", path)
		}
	}
}
func TestBusinessReset_StalePreviewNeverDeletes(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	b := BusinessReset{Paths: paths}
	preview, err := b.Preview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Settings, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Reset(t.Context(), preview.Digest); err == nil {
		t.Fatal("stale reset plan accepted")
	}
	got, err := os.ReadFile(paths.Settings)
	if err != nil || string(got) != "changed" {
		t.Fatal("stale plan wrote data")
	}
}

func TestBusinessReset_ResumesConfirmedInterruptedClear(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Root, "business-reset.state"), []byte("clearing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("retained partial state"), 0600); err != nil {
		t.Fatal(err)
	}
	b := BusinessReset{Paths: paths}
	if err := b.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	pending, err := b.Pending()
	if err != nil || !pending {
		t.Fatalf("missing Setup barrier: %v", err)
	}
	if _, err := os.Stat(paths.RuntimeConfig); !os.IsNotExist(err) {
		t.Fatal("partial runtime survived")
	}
	if err := b.Complete(); err != nil {
		t.Fatal(err)
	}
	pending, err = b.Pending()
	if err != nil || pending {
		t.Fatal("Setup barrier not cleared")
	}
}
func TestBusinessReset_RefusesLinkedTarget(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, paths.Settings); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	b := BusinessReset{Paths: paths}
	if _, err := b.Preview(t.Context()); err == nil {
		t.Fatal("linked reset target accepted")
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "keep" {
		t.Fatal("external file changed")
	}
}
