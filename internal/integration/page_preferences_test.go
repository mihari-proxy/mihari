package integration

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/preferences"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
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
