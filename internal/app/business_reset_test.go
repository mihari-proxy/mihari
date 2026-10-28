package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/platform"
	"go.yaml.in/yaml/v3"
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
	settings, web, err := b.Reset(t.Context(), preview.Digest, nil)
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
	if _, _, err := b.Reset(t.Context(), preview.Digest, nil); err == nil {
		t.Fatal("stale reset plan accepted")
	}
	pending, pendingErr := b.Pending()
	if pendingErr != nil || pending {
		t.Fatal("stale plan created a durable reset marker")
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

func TestBusinessReset_CommittedSettingsWarningDoesNotAbortClear(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	syncWarning := errors.New("fixture committed sync warning")
	b := BusinessReset{Paths: paths, saveSettings: func(path string, s config.Settings) (config.CommitResult, error) {
		result, err := config.SaveWithCommit(path, s)
		if err == nil {
			result.Warning = syncWarning
		}
		return result, err
	}}
	preview, err := b.Preview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var warnings []error
	_, _, err = b.Reset(t.Context(), preview.Digest, func(e error) { warnings = append(warnings, e) })
	if err != nil || len(warnings) != 1 || !errors.Is(warnings[0], syncWarning) {
		t.Fatalf("committed settings warning failed reset: warnings=%v err=%v", warnings, err)
	}
	data, err := os.ReadFile(filepath.Join(paths.Root, "business-reset.state"))
	if err != nil || string(data) != "setup\n" {
		t.Fatal("reset did not reach Setup after committed warning")
	}
}
func TestBusinessReset_CompletedResumeDoesNotReturnLateCancellation(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	b := BusinessReset{Paths: paths}
	if err := os.WriteFile(filepath.Join(paths.Root, "business-reset.state"), []byte("clearing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("durable completed reset misreported as failed: %v", err)
	}
	if marker, err := os.ReadFile(b.marker()); err != nil || string(marker) != "setup\n" {
		t.Fatalf("resume did not reach Setup: %v", err)
	}
}

func TestBusinessReset_UnsafeChannelSidecarRejectedBeforeMarker(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Settings, []byte("malformed: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.Bin, "core-channel"), 0700); err != nil {
		t.Fatal(err)
	}
	b := BusinessReset{Paths: paths}
	preview, err := b.Preview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Reset(t.Context(), preview.Digest, nil); err == nil {
		t.Fatal("unsafe installed channel sidecar accepted")
	}
	if pending, err := b.Pending(); err != nil || pending {
		t.Fatalf("preflight failure left a durable reset marker: %v", err)
	}
}

func TestBusinessReset_CorruptAuthPreservesInstalledChannel(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	settings.CoreChannel = "alpha"
	settings.ProxyAuth = &config.ProxyAuthSettings{Mode: "broken"}
	data, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Settings, data, 0600); err != nil {
		t.Fatal(err)
	}
	b := BusinessReset{Paths: paths}
	preview, err := b.Preview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reset, _, err := b.Reset(t.Context(), preview.Digest, nil)
	if err != nil || reset.CoreChannel != "alpha" {
		t.Fatalf("reset lost installed channel from auth-corrupt settings: %v", err)
	}
}
