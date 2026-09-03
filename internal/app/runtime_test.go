package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type webProxySelectionCall struct {
	operation runtimeapi.Operation
	group     string
	name      string
}

type webConnectionCloseCall struct {
	operation runtimeapi.Operation
	id        string
}

type recordingWebMutationRuntime struct {
	selectCalls        []webProxySelectionCall
	closeCalls         []webConnectionCloseCall
	closeAllOperations []runtimeapi.Operation
	enableOperations   []runtimeapi.Operation
	disableOperations  []runtimeapi.Operation
}

func (r *recordingWebMutationRuntime) SelectProxy(_ context.Context, operation runtimeapi.Operation, group, name string) error {
	r.selectCalls = append(r.selectCalls, webProxySelectionCall{operation: operation, group: group, name: name})
	return nil
}

func (r *recordingWebMutationRuntime) CloseConnection(_ context.Context, operation runtimeapi.Operation, id string) error {
	r.closeCalls = append(r.closeCalls, webConnectionCloseCall{operation: operation, id: id})
	return nil
}

func (r *recordingWebMutationRuntime) CloseAllConnections(_ context.Context, operation runtimeapi.Operation) error {
	r.closeAllOperations = append(r.closeAllOperations, operation)
	return nil
}

func (r *recordingWebMutationRuntime) EnableTun(_ context.Context, operation runtimeapi.Operation, _ bool) (protocol.TunStatus, error) {
	r.enableOperations = append(r.enableOperations, operation)
	return protocol.TunStatus{}, nil
}

func (r *recordingWebMutationRuntime) DisableTun(_ context.Context, operation runtimeapi.Operation) (protocol.TunStatus, error) {
	r.disableOperations = append(r.disableOperations, operation)
	return protocol.TunStatus{}, nil
}

func TestWebMutatorRoutesOperationsThroughRuntime(t *testing.T) {
	runtime := &recordingWebMutationRuntime{}
	mutator := webMutator{manager: runtime}
	ctx := context.Background()

	if err := mutator.SelectProxy(ctx, "Proxy Group", "selected-proxy"); err != nil {
		t.Fatal(err)
	}
	if err := mutator.CloseConnection(ctx, "connection-id"); err != nil {
		t.Fatal(err)
	}
	if err := mutator.CloseAllConnections(ctx); err != nil {
		t.Fatal(err)
	}

	if len(runtime.selectCalls) != 1 {
		t.Fatalf("select calls=%d", len(runtime.selectCalls))
	}
	selectCall := runtime.selectCalls[0]
	if selectCall.group != "Proxy Group" || selectCall.name != "selected-proxy" {
		t.Fatalf("select call=%#v", selectCall)
	}
	assertWebOperation(t, selectCall.operation, "web-select-")

	if len(runtime.closeCalls) != 1 {
		t.Fatalf("close calls=%d", len(runtime.closeCalls))
	}
	closeCall := runtime.closeCalls[0]
	if closeCall.id != "connection-id" {
		t.Fatalf("close call=%#v", closeCall)
	}
	assertWebOperation(t, closeCall.operation, "web-close-")

	if len(runtime.closeAllOperations) != 1 {
		t.Fatalf("close all calls=%d", len(runtime.closeAllOperations))
	}
	assertWebOperation(t, runtime.closeAllOperations[0], "web-close-all-")
}

func TestWebMutatorConfigPatchAllowlist(t *testing.T) {
	runtime := &recordingWebMutationRuntime{}
	mutator := webMutator{manager: runtime}
	ctx := context.Background()

	if err := mutator.ApplyConfigPatch(ctx, map[string]any{"tun": map[string]any{"enable": true}}); err != nil {
		t.Fatal(err)
	}
	if err := mutator.ApplyConfigPatch(ctx, map[string]any{"tun": map[string]any{"enable": false}}); err != nil {
		t.Fatal(err)
	}

	if len(runtime.enableOperations) != 1 {
		t.Fatalf("enable calls=%d", len(runtime.enableOperations))
	}
	assertWebOperation(t, runtime.enableOperations[0], "web-tun-")
	if len(runtime.disableOperations) != 1 {
		t.Fatalf("disable calls=%d", len(runtime.disableOperations))
	}
	assertWebOperation(t, runtime.disableOperations[0], "web-tun-")

	for _, test := range []struct {
		name  string
		patch map[string]any
		code  protocol.ErrorCode
	}{
		{name: "missing tun", patch: map[string]any{}, code: protocol.CodeUnsupportedMutation},
		{name: "non object tun", patch: map[string]any{"tun": "enabled"}, code: protocol.CodeInvalidArgument},
		{name: "non boolean enable", patch: map[string]any{"tun": map[string]any{"enable": "true"}}, code: protocol.CodeInvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := mutator.ApplyConfigPatch(ctx, test.patch)
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != test.code {
				t.Fatalf("err=%v, code=%q", err, apiError.Code)
			}
		})
	}
}

func assertWebOperation(t *testing.T, operation runtimeapi.Operation, prefix string) {
	t.Helper()
	if operation.Source != "web" {
		t.Fatalf("operation source=%q", operation.Source)
	}
	if operation.ID == "" || !strings.HasPrefix(operation.ID, prefix) {
		t.Fatalf("operation ID=%q, prefix=%q", operation.ID, prefix)
	}
}

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
	if !slices.Contains(assembly.Manager.Capabilities(), protocol.CapabilityWebGUI) {
		t.Fatalf("capabilities=%v", assembly.Manager.Capabilities())
	}
	if assembly.Web == nil {
		t.Fatal("runtime assembly did not wire web gateway")
	}
	if _, err := os.Stat(paths.WebCredential); err != nil {
		t.Fatalf("web credential missing: %v", err)
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

func TestBuildRuntimeSetsCoreChannelFromSettingsWhenVersionDetected(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := os.MkdirAll(filepath.Dir(paths.CoreBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeCoreBinary(t, paths.CoreBinary, "Mihomo Meta v1.19.0 windows amd64")
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("e", 64)
	settings.CoreChannel = "alpha"
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test-version", nil, nil, RuntimeBuildOptions{SettingsPath: paths.Settings})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := assembly.Store.Load()
	if snapshot.Core.Status != "stopped" || snapshot.Core.Version != "v1.19.0" || snapshot.Core.Channel != "alpha" {
		t.Fatalf("core=%#v", snapshot.Core)
	}
}

func writeFakeCoreBinary(t *testing.T, dest, versionOutput string) {
	t.Helper()
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "main.go")
	source := fmt.Sprintf("package main\nimport (\n\t\"fmt\"\n\t\"os\"\n)\nfunc main() {\n\tif len(os.Args) > 1 && os.Args[1] == \"-v\" {\n\t\tfmt.Print(%q)\n\t\treturn\n\t}\n\tos.Exit(1)\n}\n", versionOutput)
	if err := os.WriteFile(src, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", dest, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake core: %v\n%s", err, out)
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

func TestBuildRuntime_UsesOptionWritersCapture(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("a", 64)
	captureOut := &bytes.Buffer{}
	captureErr := &bytes.Buffer{}
	positionalOut := &bytes.Buffer{}
	positionalErr := &bytes.Buffer{}
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test-version", positionalOut, positionalErr, RuntimeBuildOptions{
		SettingsPath: paths.Settings,
		MihomoStdout: captureOut,
		MihomoStderr: captureErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if assembly.mihomoStarter.Stdout != captureOut {
		t.Fatalf("starter stdout=%T want capture buffer", assembly.mihomoStarter.Stdout)
	}
	if assembly.mihomoStarter.Stderr != captureErr {
		t.Fatalf("starter stderr=%T want capture buffer", assembly.mihomoStarter.Stderr)
	}
	if assembly.mihomoStarter.Stdout == positionalOut || assembly.mihomoStarter.Stderr == positionalErr {
		t.Fatal("positional stdout/stderr entered CommandStarter")
	}
}

func TestBuildRuntime_LogsRedactedBackground(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("b", 64)
	secret := "background-secret-value"
	redactor := logging.NewRedactor(secret)
	var buf bytes.Buffer
	level := &slog.LevelVar{}
	logger := slog.New(logging.NewJSONHandler(&buf, level, "daemon", redactor))
	reported := make(chan struct{})
	var once sync.Once
	var gotComponent string
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test-version", nil, nil, RuntimeBuildOptions{
		SettingsPath: paths.Settings,
		OnBackgroundError: func(component string, err error) {
			gotComponent = component
			logger.Error("background "+secret+": "+err.Error(), slog.String("component", component))
			once.Do(func() { close(reported) })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	holder, err := net.Listen("tcp", settings.WebAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- assembly.Manager.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("manager did not stop")
		}
	})
	select {
	case <-reported:
	case <-time.After(5 * time.Second):
		t.Fatal("manager background error was not reported")
	}
	if gotComponent != "web-gateway" {
		t.Fatalf("component=%q", gotComponent)
	}
	logged := buf.String()
	if strings.Contains(logged, secret) {
		t.Fatalf("secret leaked into daemon logger: %s", logged)
	}
	if !strings.Contains(logged, "***") || !strings.Contains(logged, "web-gateway") {
		t.Fatalf("logger=%s", logged)
	}
}
