//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestStartupCleanup_LegacyJournalPreservesOldCoreBinary(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(paths.CoreBinary, []byte("current"), 0700); err != nil {
		t.Fatal(err)
	}
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test", nil, nil, RuntimeBuildOptions{SettingsPath: paths.Settings})
	if err != nil {
		t.Fatal(err)
	}
	old := paths.CoreBinary + ".old-123"
	if err := os.WriteFile(old, []byte("rollback"), 0700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(paths.Root, "staging", "core", "provenance-commit.json")
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, []byte("unresolved recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := assembly.StartupCleanup(t.Context()); err == nil {
		t.Fatal("missing retained recovery warning")
	}
	if raw, err := os.ReadFile(old); err != nil || string(raw) != "rollback" {
		t.Fatalf("recovery copy changed: %q %v", raw, err)
	}
}
