package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/preferences"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestLatencyConcurrency_ControlRoundTripValidationAndLegacyPatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	newClient := func() *controlclient.Client {
		prefs, err := preferences.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		store := state.NewStore(state.Snapshot{})
		manager := runtimeapi.New(runtimeapi.Options{Store: store, Coordinator: state.NewCoordinator(store), Preferences: prefs})
		server := controlserver.New(controlserver.Options{Token: "fixture-token", Store: store, Runtime: manager})
		return controlclient.NewHTTP("http://control.invalid", "fixture-token", &http.Client{Transport: providerHandlerTransport{server.Handler()}})
	}
	client := newClient()
	initial, err := client.TUIPreferences(t.Context())
	if err != nil || initial.EffectiveProxies().LatencyTestConcurrency != 5 {
		t.Fatalf("defaults=%+v err=%v", initial, err)
	}
	for _, limit := range []int{1, 50, 8} {
		prefs := initial.EffectiveProxies()
		prefs.LatencyTestConcurrency = limit
		got, err := client.UpdateTUIPreferences(t.Context(), protocol.UpdateTUIPreferencesRequest{OperationID: fmt.Sprintf("limit-%d", limit), Proxies: &prefs})
		if err != nil || got.EffectiveProxies().LatencyTestConcurrency != limit {
			t.Fatalf("limit=%d saved=%+v err=%v", limit, got, err)
		}
	}
	for i, req := range []protocol.UpdateTUIPreferencesRequest{
		{Proxies: &protocol.ProxyPreferences{AutoLatencyTest: true}},
		{ConnectionsColumns: []string{"host", "chain"}},
		{LogLevels: []string{"error"}},
	} {
		req.OperationID = fmt.Sprintf("legacy-independent-%d", i)
		if _, err := client.UpdateTUIPreferences(t.Context(), req); err != nil {
			t.Fatal(err)
		}
	}
	before, err := client.TUIPreferences(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{-1, 51} {
		_, err := client.UpdateTUIPreferences(t.Context(), protocol.UpdateTUIPreferencesRequest{OperationID: fmt.Sprintf("invalid-%d", limit), ConnectionsColumns: []string{"traffic"}, Proxies: &protocol.ProxyPreferences{LatencyTestConcurrency: limit}})
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
			t.Fatalf("invalid limit=%d error=%v", limit, err)
		}
	}
	after, err := client.TUIPreferences(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected updates changed revision/state: before=%+v after=%+v err=%v", before, after, err)
	}
	reopened, err := newClient().TUIPreferences(t.Context())
	if err != nil || reopened.EffectiveProxies().LatencyTestConcurrency != 8 || reopened.EffectiveProxies().ExtraLatency || !reopened.EffectiveProxies().AutoLatencyTest || !reflect.DeepEqual(reopened.ConnectionsColumns, []string{"host", "chain"}) || !reflect.DeepEqual(reopened.LogLevels, []string{"error"}) {
		t.Fatalf("persisted preferences=%+v err=%v", reopened, err)
	}
	// Restoring 5 omits only the new field; the older boolean overrides remain.
	prefs := before.EffectiveProxies()
	prefs.LatencyTestConcurrency = 5
	if _, err := client.UpdateTUIPreferences(t.Context(), protocol.UpdateTUIPreferencesRequest{OperationID: "restore-default", Proxies: &prefs}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Schema             string   `json:"schema"`
		ConnectionsColumns []string `json:"connections_columns"`
		Proxies            struct {
			ExtraLatency    bool `json:"extra_latency"`
			AutoLatencyTest bool `json:"auto_latency_test"`
		} `json:"proxies"`
		LogLevels []string `json:"log_levels"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		t.Fatalf("restoring default did not restore legacy file shape: %v", err)
	}
}
