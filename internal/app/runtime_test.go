package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/platform"
)

func TestBuildRuntimeCreatesBootstrapAndSharedState(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	settings := testRuntimeSettings(t)
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
	if columns := assembly.Manager.TUIPreferences().ConnectionsColumns; len(columns) == 0 {
		t.Fatal("runtime assembly did not open TUI preferences")
	}
	if !slices.Contains(assembly.Manager.Capabilities(), protocol.CapabilityGeoIP) {
		t.Fatalf("capabilities=%v", assembly.Manager.Capabilities())
	}
	if !slices.Contains(assembly.Manager.Capabilities(), protocol.CapabilityOnboarding) {
		t.Fatalf("capabilities=%v", assembly.Manager.Capabilities())
	}
	raw, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "external-controller: "+settings.ControllerAddr) || !strings.Contains(string(raw), settings.ControllerSecret) {
		t.Fatalf("config=%s", raw)
	}
}

func TestBuildRuntimeWithOptionsMarksNewInstallationSetupRequired(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("d", 64)
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test-version", nil, nil, RuntimeBuildOptions{
		InitialSetupRequired: true, SettingsPath: paths.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := assembly.Manager.OnboardingStatus(context.Background())
	if err != nil || status.Status.Complete {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestBuildRuntimeRejectsOccupiedManagedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("c", 64)
	settings.ControllerAddr = listener.Addr().String()
	_, err = BuildRuntime(paths, settings, "test-version", nil, nil)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState || apiError.Details["setting"] != "controller-addr" {
		t.Fatalf("err=%v", err)
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
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("b", 64)
	if _, err := BuildRuntime(paths, settings, "test-version", nil, nil); err == nil {
		t.Fatal("expected unmanaged runtime config to fail")
	}
}

func testRuntimeSettings(t *testing.T) config.Settings {
	t.Helper()
	listeners := make([]net.Listener, 0, 3)
	addresses := make([]string, 0, 3)
	for range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	settings := config.Defaults()
	settings.MixedAddr = addresses[0]
	settings.ControllerAddr = addresses[1]
	settings.WebAddr = addresses[2]
	return settings
}
