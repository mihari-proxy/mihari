package integration

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/preferences"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestLogPreferences_ControlPlanePersistsIndependentFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	newClient := func() *controlclient.Client {
		service, err := preferences.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		store := state.NewStore(state.Snapshot{})
		manager := runtimeapi.New(runtimeapi.Options{Store: store, Coordinator: state.NewCoordinator(store), Preferences: service})
		server := controlserver.New(controlserver.Options{Token: "fixture-token", Store: store, Runtime: manager})
		return controlclient.NewHTTP("http://control.invalid", "fixture-token", &http.Client{Transport: providerHandlerTransport{server.Handler()}})
	}
	ctx := context.Background()
	client := newClient()
	for _, request := range []protocol.UpdateTUIPreferencesRequest{
		{OperationID: "columns-first", ConnectionsColumns: []string{"host", "chain"}},
		{OperationID: "proxies-first", Proxies: &protocol.ProxyPreferences{ExtraLatency: false, AutoLatencyTest: false}},
		{OperationID: "levels-first", LogLevels: []string{"debug", "warn"}},
		{OperationID: "columns-second", ConnectionsColumns: []string{"host", "traffic"}},
		{OperationID: "proxies-second", Proxies: &protocol.ProxyPreferences{ExtraLatency: false, AutoLatencyTest: true}},
		{OperationID: "levels-second", LogLevels: []string{"error"}},
		{OperationID: "proxies-third", Proxies: &protocol.ProxyPreferences{ExtraLatency: true, AutoLatencyTest: false}},
	} {
		if _, err := client.UpdateTUIPreferences(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	for _, request := range []protocol.UpdateTUIPreferencesRequest{
		{OperationID: "unknown-level", LogLevels: []string{"silent"}},
		{OperationID: "empty-levels", ConnectionsColumns: []string{"chain"}, LogLevels: []string{}},
		{OperationID: "empty-columns", ConnectionsColumns: []string{}, LogLevels: []string{"warn"}},
		{OperationID: "invalid-combined", LogLevels: []string{}, Proxies: &protocol.ProxyPreferences{ExtraLatency: false, AutoLatencyTest: true}},
	} {
		_, err := client.UpdateTUIPreferences(ctx, request)
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidArgument {
			t.Fatalf("invalid preferences: %v", err)
		}
	}
	// A fresh daemon/client pair reads only the committed file.
	got, err := newClient().TUIPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.LogLevels, []string{"error"}) || !slices.Equal(got.ConnectionsColumns, []string{"host", "traffic"}) {
		t.Fatalf("reopened preferences=%+v", got)
	}
	if got.Proxies == nil || *got.Proxies != (protocol.ProxyPreferences{ExtraLatency: true, AutoLatencyTest: false}) {
		t.Fatalf("independent updates lost proxy settings: %+v", got.Proxies)
	}
}
