package app

import (
	"errors"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestBuildRuntime_StartupCleanupUsesOwnedCoreStore(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	store, err := core.NewUpdateStore(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	tx := "1234567890abcdef1234567890abcdef"
	for role, body := range map[core.ProvenanceRole]string{core.UpdateCandidate: "inert fixture", core.UpdateMarker: tx} {
		if err := store.Save(t.Context(), role, tx, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	candidate, err := store.Inspect(t.Context(), core.UpdateCandidate, tx)
	if err != nil {
		t.Fatal(err)
	}
	u, err := core.BeginUpdate(t.Context(), store, tx, candidate, core.UpdateIntent{Previous: core.CoreSelection{Channel: "stable"}, Next: core.CoreSelection{Channel: "stable"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := u.Commit(t.Context(), func(core.CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test", nil, nil, RuntimeBuildOptions{SettingsPath: paths.Settings})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(t.Context(), core.UpdateMarker, tx); err != nil {
		t.Fatalf("assembly cleaned before ownership: %v", err)
	}
	if assembly.StartupCleanup == nil {
		t.Fatal("missing owned startup cleaner")
	}
	if err := assembly.StartupCleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(t.Context(), core.UpdateMarker, tx); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup retained marker: %v", err)
	}
	if raw, err := os.ReadFile(paths.CoreBinary); err != nil || string(raw) != "inert fixture" {
		t.Fatalf("current core changed: %q %v", raw, err)
	}
}
