package integration

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"path/filepath"
	"strings"
	"testing"
)

func TestEgress_NativeIPCSavesRetainsMissingAndRestoresAutomatic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	f := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true}, nil, func(o *runtimeapi.Options) {
		o.Store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
		o.Settings.ControllerSecret = strings.Repeat("ab", 32)
		o.SettingsPath = path
		o.SaveSettings = config.SaveWithCommit
		o.ListInterfaces = func(context.Context) ([]platform.NetworkInterface, error) {
			return []platform.NetworkInterface{{Name: "Work VPN", Availability: "disconnected"}}, nil
		}
	})
	status, err := f.client.Egress(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.EgressUpdateRequest{OperationID: "egress-save", IfRevision: &status.Revision, Mode: "manual", InterfaceName: "Work VPN"}
	saved, err := f.client.UpdateEgress(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if saved.State != "saved" || saved.Selection.InterfaceName != "Work VPN" {
		t.Fatalf("saved=%+v", saved)
	}
	replay, err := f.client.UpdateEgress(t.Context(), request)
	if err != nil || replay.Revision != saved.Revision {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	persisted, err := config.Load(path)
	if err != nil || persisted.EgressInterface != "Work VPN" {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
	_, err = f.client.UpdateEgress(t.Context(), protocol.EgressUpdateRequest{OperationID: "stale-egress", IfRevision: &status.Revision, Mode: "automatic"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	_, err = f.client.UpdateEgress(t.Context(), protocol.EgressUpdateRequest{OperationID: "egress-auto", Mode: "automatic"})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err = config.Load(path)
	if err != nil || persisted.EgressInterface != "" {
		t.Fatalf("auto=%+v err=%v", persisted, err)
	}
}

func TestEgress_IPCCommittedWarningRemainsSuccess(t *testing.T) {
	f := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true, Warning: errors.New("directory sync warning")}, nil, func(o *runtimeapi.Options) {
		o.Store.Store(state.Snapshot{Core: state.CoreState{Status: "stopped"}})
		o.ListInterfaces = func(context.Context) ([]platform.NetworkInterface, error) {
			return []platform.NetworkInterface{{Name: "Ethernet"}}, nil
		}
	})
	got, err := f.client.UpdateEgress(t.Context(), protocol.EgressUpdateRequest{OperationID: "egress-warning", Mode: "manual", InterfaceName: "Ethernet"})
	if err != nil || got.Selection.InterfaceName != "Ethernet" || len(got.Warnings) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
