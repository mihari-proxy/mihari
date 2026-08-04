package webgui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

type fakeClient struct {
	calls  int
	status protocol.WebGUIStatus
}

func (f *fakeClient) WebGUI(context.Context) (protocol.WebGUIStatus, error) {
	f.calls++
	return f.status, nil
}

func TestWebGUINoCapabilityRendersUnavailableWithoutCallingClient(t *testing.T) {
	fake := &fakeClient{}
	model := New(fake, nil)
	if got := model.View(); !strings.Contains(got, "Unavailable") {
		t.Fatalf("view=%q", got)
	}
	if command := model.Load(); command != nil || fake.calls != 0 {
		t.Fatalf("command=%v calls=%d", command != nil, fake.calls)
	}
}

func TestWebGUIRendersInjectedFutureCapabilityStatusWithoutMutationKeys(t *testing.T) {
	fake := &fakeClient{status: protocol.WebGUIStatus{
		Schema: "mihari/v1", GatewayAddr: "127.0.0.1:9191", GatewayHealth: "Healthy", ActivePanel: "zashboard", BrowserSessions: 3,
		Panels: []protocol.PanelStatus{
			{ID: "zashboard", Name: "Zashboard", Active: true, InstalledBuild: "v2.1.0", LatestBuild: "v2.2.0", Health: "Healthy", RollbackBuild: "v2.0.0"},
			{ID: "metacubexd", Name: "MetaCubeXD", InstalledBuild: "8e31c4a", LatestBuild: "c12ad90", Health: "Healthy"},
		},
		Safeguards: protocol.GatewaySafeguards{LoopbackBound: true, BrowserAuthenticated: true, ControllerIsolated: true, MutationsCoordinated: true},
	}}
	model := New(fake, []string{protocol.CapabilityWebGUI})
	command := model.Load()
	if command == nil {
		t.Fatal("capability did not enable status load")
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{"127.0.0.1:9191", "Zashboard", "v2.1.0", "v2.0.0", "MetaCubeXD", "8e31c4a", "3", "Loopback", "Controller isolation", "Mutation coordinator"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	for _, key := range []string{"i install", "u update", "b rollback", "o open"} {
		if strings.Contains(strings.ToLower(view), key) {
			t.Fatalf("phase 5 action leaked into view: %s", view)
		}
	}
	for _, key := range []rune{'i', 'u', 'b', 'o'} {
		updated, command := model.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		model = updated.(*Model)
		if command != nil || fake.calls != 1 {
			t.Fatalf("key=%q command=%v calls=%d", key, command != nil, fake.calls)
		}
	}
}
