package integration

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

// TestSystemProxyForeignConflictAndForceOverIPC exercises the control path
// (client → named pipe/socket → server → runtime.Manager → FakeBackend).
func TestSystemProxyForeignConflictAndForceOverIPC(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: true, Server: "127.0.0.1:7890"}}
	controller := &stubMihomoController{configs: map[string]any{
		"tun": map[string]any{"enable": false, "stack": "system"},
	}}
	client, cancel := startControlDaemon(t, backend, controller)
	defer cancel()

	status, err := client.SystemProxy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Schema != "mihari/v1" || status.Target != "127.0.0.1:9190" {
		t.Fatalf("status=%#v", status)
	}
	if !status.Observed.Enabled || !status.Observed.Foreign || status.Observed.Owned || status.Observed.Server != "127.0.0.1:7890" {
		t.Fatalf("observed=%#v", status.Observed)
	}
	if status.Desired {
		t.Fatal("desired should start false")
	}

	_, err = client.EnableSystemProxy(context.Background(), protocol.SystemProxyMutationRequest{
		OperationID: "sysproxy-conflict-1",
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeSystemProxyConflict {
		t.Fatalf("enable without force err=%v", err)
	}
	if apiError.Details["current_server"] != "127.0.0.1:7890" {
		t.Fatalf("details=%v", apiError.Details)
	}
	if backend.EnableCalls != 0 {
		t.Fatalf("EnableCalls=%d want 0 on conflict", backend.EnableCalls)
	}

	enabled, err := client.EnableSystemProxy(context.Background(), protocol.SystemProxyMutationRequest{
		OperationID: "sysproxy-force-1",
		Force:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Desired || !enabled.Observed.Enabled || !enabled.Observed.Owned || enabled.Observed.Foreign {
		t.Fatalf("enabled status=%#v", enabled)
	}
	if enabled.Observed.Server != "127.0.0.1:9190" || enabled.Target != "127.0.0.1:9190" {
		t.Fatalf("enabled status=%#v", enabled)
	}
	if backend.EnableCalls != 1 || backend.LastEnableHost != "127.0.0.1" || backend.LastEnablePort != 9190 {
		t.Fatalf("backend EnableCalls=%d host=%q port=%d", backend.EnableCalls, backend.LastEnableHost, backend.LastEnablePort)
	}

	// Disable of Mihari-owned proxy succeeds.
	disabled, err := client.DisableSystemProxy(context.Background(), protocol.SystemProxyMutationRequest{
		OperationID: "sysproxy-disable-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Desired || disabled.Observed.Enabled || disabled.Observed.Owned {
		t.Fatalf("disabled status=%#v", disabled)
	}
	if backend.DisableCalls != 1 {
		t.Fatalf("DisableCalls=%d", backend.DisableCalls)
	}
}

// TestTunEnableDisableOverIPC exercises TUN mutations through the control client.
func TestTunEnableDisableOverIPC(t *testing.T) {
	backend := &sysproxy.FakeBackend{State: sysproxy.State{Enabled: false}}
	controller := &stubMihomoController{configs: map[string]any{
		"tun": map[string]any{"enable": false, "stack": "system"},
	}}
	client, cancel := startControlDaemon(t, backend, controller)
	defer cancel()

	status, err := client.Tun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Managed || status.DesiredEnable {
		t.Fatalf("unmanaged status=%#v", status)
	}
	if status.LiveEnable == nil || *status.LiveEnable {
		t.Fatalf("live_enable=%v", status.LiveEnable)
	}

	enabled, err := client.EnableTun(context.Background(), protocol.TunMutationRequest{
		OperationID: "tun-enable-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.DesiredEnable || !enabled.Managed || enabled.Stack != "gVisor" {
		t.Fatalf("enabled=%#v", enabled)
	}
	if enabled.LiveEnable == nil || !*enabled.LiveEnable {
		t.Fatalf("live_enable=%v", enabled.LiveEnable)
	}
	if controller.patchCalls != 1 {
		t.Fatalf("patchCalls=%d", controller.patchCalls)
	}
	tun, ok := controller.lastPatch["tun"].(map[string]any)
	if !ok || tun["enable"] != true || tun["stack"] != "gVisor" {
		t.Fatalf("lastPatch=%#v", controller.lastPatch)
	}

	disabled, err := client.DisableTun(context.Background(), protocol.TunMutationRequest{
		OperationID: "tun-disable-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.DesiredEnable || !disabled.Managed || disabled.Stack != "gVisor" {
		t.Fatalf("disabled=%#v", disabled)
	}
	if disabled.LiveEnable == nil || *disabled.LiveEnable {
		t.Fatalf("live_enable after disable=%v", disabled.LiveEnable)
	}
	if controller.patchCalls != 2 {
		t.Fatalf("patchCalls=%d after disable", controller.patchCalls)
	}
}

func startControlDaemon(t *testing.T, backend *sysproxy.FakeBackend, controller *stubMihomoController) (*controlclient.Client, context.CancelFunc) {
	t.Helper()
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	settings := config.Settings{
		Schema:             "mihari.settings/v1",
		MixedAddr:          "127.0.0.1:9190",
		ControllerAddr:     "127.0.0.1:9090",
		WebAddr:            "127.0.0.1:9191",
		ControllerSecret:   strings.Repeat("d", 64),
		SystemProxyDesired: false,
	}
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}

	store := state.NewStore(state.Snapshot{
		Revision:  0,
		Version:   "integration",
		StartedAt: time.Now().UTC(),
		Health:    "ok",
	})
	manager := runtimeapi.New(runtimeapi.Options{
		Store:        store,
		Coordinator:  state.NewCoordinator(store),
		Controller:   controller,
		SysProxy:     backend,
		Settings:     settings,
		SettingsPath: settingsPath,
		BinaryExists: func() bool { return true },
		// No supervisor: Run parks on context cancel with degraded core status.
	})

	endpoint := transporttest.Endpoint(t)
	const token = "sysproxy-tun-token"
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{
			Endpoint: endpoint,
			Token:    token,
			Version:  "integration",
			Ready:    ready,
			Store:    store,
			Runtime:  manager,
		})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("daemon did not become ready")
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon exit: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	return controlclient.New(endpoint, token), cancel
}

// stubMihomoController is a minimal runtime.Controller for TUN config patch/status.
type stubMihomoController struct {
	configs    map[string]any
	lastPatch  map[string]any
	patchCalls int
}

func (c *stubMihomoController) Proxies(context.Context) (mihomo.Proxies, error) {
	return mihomo.Proxies{}, nil
}
func (c *stubMihomoController) SelectProxy(context.Context, string, string) error { return nil }
func (c *stubMihomoController) DelayGroup(context.Context, string, string, int) (mihomo.Delays, error) {
	return nil, nil
}
func (c *stubMihomoController) DelayProxy(context.Context, string, string, int) (uint16, error) {
	return 0, nil
}
func (c *stubMihomoController) Connections(context.Context) (mihomo.Connections, error) {
	return mihomo.Connections{}, nil
}
func (c *stubMihomoController) CloseConnection(context.Context, string) error { return nil }
func (c *stubMihomoController) CloseAllConnections(context.Context) error     { return nil }
func (c *stubMihomoController) Rules(context.Context) (mihomo.Rules, error) {
	return mihomo.Rules{}, nil
}
func (c *stubMihomoController) RuleProviders(context.Context) (mihomo.RuleProviders, error) {
	return mihomo.RuleProviders{}, nil
}
func (c *stubMihomoController) UpdateRuleProvider(context.Context, string) error { return nil }
func (c *stubMihomoController) Configs(context.Context) (map[string]any, error) {
	if c.configs == nil {
		return map[string]any{}, nil
	}
	return c.configs, nil
}
func (c *stubMihomoController) PatchConfigs(_ context.Context, patch map[string]any) error {
	c.patchCalls++
	c.lastPatch = patch
	if c.configs == nil {
		c.configs = map[string]any{}
	}
	if tun, ok := patch["tun"].(map[string]any); ok {
		cloned := make(map[string]any, len(tun))
		for k, v := range tun {
			cloned[k] = v
		}
		c.configs["tun"] = cloned
	}
	return nil
}
func (c *stubMihomoController) Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error {
	return nil
}
