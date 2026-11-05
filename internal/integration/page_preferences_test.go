package integration

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/observation"
	"github.com/mihari-proxy/mihari/internal/preferences"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestPagePreferences_ControlRoundTripAndIndependentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	prefs, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(state.Snapshot{Health: "ok"})
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Preferences: prefs})
	httpServer := httptest.NewServer(server.New(server.Options{Token: "synthetic-token", Store: store, Runtime: manager}).Handler())
	t.Cleanup(httpServer.Close)
	client := controlclient.NewHTTP(httpServer.URL, "synthetic-token", httpServer.Client())
	ctx := context.Background()
	initial, err := client.TUIPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !initial.EffectiveProxies().ExtraLatency || !initial.EffectiveProxies().AutoLatencyTest {
		t.Fatalf("defaults=%+v", initial)
	}
	disabled := protocol.ProxyPreferences{LatencyTestConcurrency: 5}
	saved, err := client.UpdateTUIPreferences(ctx, protocol.UpdateTUIPreferencesRequest{OperationID: "proxy-settings", IfRevision: &initial.Revision, Proxies: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if saved.EffectiveProxies() != disabled || !slices.Equal(saved.ConnectionsColumns, initial.ConnectionsColumns) {
		t.Fatalf("proxy patch=%+v", saved)
	}
	_, err = client.UpdateTUIPreferences(ctx, protocol.UpdateTUIPreferencesRequest{OperationID: "stale-settings", IfRevision: &initial.Revision, Proxies: &protocol.ProxyPreferences{ExtraLatency: true}})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("stale patch err=%v", err)
	}
	saved, err = client.UpdateTUIPreferences(ctx, protocol.UpdateTUIPreferencesRequest{OperationID: "columns", IfRevision: &saved.Revision, ConnectionsColumns: []string{"host", "traffic"}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.EffectiveProxies() != disabled || !slices.Equal(saved.ConnectionsColumns, []string{"host", "traffic"}) {
		t.Fatalf("columns patch=%+v", saved)
	}
	reopened, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot(); got.Proxies.ExtraLatency || got.Proxies.AutoLatencyTest || !slices.Equal(got.ConnectionsColumns, saved.ConnectionsColumns) {
		t.Fatalf("persisted=%+v", got)
	}
}

func TestPerformancePreferences_ControlAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	prefs, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(state.Snapshot{Health: "ok"})
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Preferences: prefs})
	httpServer := httptest.NewServer(server.New(server.Options{Token: "synthetic-token", Store: store, Runtime: manager}).Handler())
	t.Cleanup(httpServer.Close)
	client := controlclient.NewHTTP(httpServer.URL, "synthetic-token", httpServer.Client())
	initial, err := client.TUIPreferences(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Performance == nil || initial.EffectivePerformance().ObservationReuseSeconds != 3 {
		t.Fatalf("defaults=%+v", initial.Performance)
	}
	value := protocol.PerformancePreferences{ControllerOwnerDetection: true, ObservationReuseSeconds: 5}
	proxies := initial.EffectiveProxies()
	proxies.ExtraLatency = false
	saved, err := client.UpdateTUIPreferences(t.Context(), protocol.UpdateTUIPreferencesRequest{OperationID: "performance", IfRevision: &initial.Revision, Performance: &value, Proxies: &proxies})
	if err != nil {
		t.Fatal(err)
	}
	if saved.EffectivePerformance() != value || saved.EffectiveProxies() != proxies || saved.Revision != initial.Revision+1 {
		t.Fatalf("saved=%+v", saved)
	}
	bad := value
	bad.ObservationReuseSeconds = 6
	_, err = client.UpdateTUIPreferences(t.Context(), protocol.UpdateTUIPreferencesRequest{OperationID: "invalid-performance", Performance: &bad})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
		t.Fatal(err)
	}
	after, err := client.TUIPreferences(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != saved.Revision || after.EffectivePerformance() != value {
		t.Fatal("invalid save changed committed policy")
	}
	reopened, err := preferences.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().Performance.ObservationReuseSeconds != 5 {
		t.Fatal("policy did not persist")
	}
	// A legacy PATCH omitting performance must preserve it.
	if _, err = client.UpdateTUIPreferences(t.Context(), protocol.UpdateTUIPreferencesRequest{OperationID: "legacy-proxy", Proxies: &proxies}); err != nil {
		t.Fatal(err)
	}
	if manager.TUIPreferences().Performance.ObservationReuseSeconds != 5 {
		t.Fatal("legacy patch reset performance")
	}
}

func TestPerformanceObservation_ExplicitFreshCrossesIPC(t *testing.T) {
	prefs, err := preferences.Open(filepath.Join(t.TempDir(), "tui.json"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &sysproxy.FakeBackend{}
	store := state.NewStore(state.Snapshot{Health: "ok"})
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Preferences: prefs, SysProxy: backend, Settings: config.Settings{MixedAddr: "127.0.0.1:17890"}})
	httpServer := httptest.NewServer(server.New(server.Options{Token: "synthetic-token", Store: store, Runtime: manager}).Handler())
	t.Cleanup(httpServer.Close)
	client := controlclient.NewHTTP(httpServer.URL, "synthetic-token", httpServer.Client())
	for range 2 {
		if _, err := client.SystemProxy(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if backend.GetCalls != 1 {
		t.Fatalf("ordinary reads=%d", backend.GetCalls)
	}
	if _, err := client.SystemProxy(observation.WithFresh(t.Context())); err != nil {
		t.Fatal(err)
	}
	if backend.GetCalls != 2 {
		t.Fatal("fresh query lost in transport")
	}
}
