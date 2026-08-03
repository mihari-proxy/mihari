package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/platform"
)

func TestBuildRuntimeCreatesBootstrapAndSharedState(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	assembly, err := BuildRuntime(paths, settings, "test-version", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if assembly.Manager == nil || assembly.Store == nil {
		t.Fatalf("assembly=%#v", assembly)
	}
	if snapshot := assembly.Store.Load(); snapshot.Version != "test-version" || snapshot.Health != "ok" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	raw, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "external-controller: 127.0.0.1:9090") || !strings.Contains(string(raw), settings.ControllerSecret) {
		t.Fatalf("config=%s", raw)
	}
}

func TestBuildRuntimeRejectsExistingConfigWithoutManagedInvariants(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := os.MkdirAll(filepath.Dir(paths.RuntimeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("existing: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("b", 64)
	if _, err := BuildRuntime(paths, settings, "test-version", nil, nil); err == nil {
		t.Fatal("expected unmanaged runtime config to fail")
	}
}
