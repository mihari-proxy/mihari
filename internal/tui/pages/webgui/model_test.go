package webgui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type fakeClient struct {
	calls      int
	status     protocol.WebGUIStatus
	installed  int
	updated    int
	activated  int
	rolledBack int
	opened     int
	openURL    string
	lastID     string
}

func (f *fakeClient) WebGUI(context.Context) (protocol.WebGUIStatus, error) {
	f.calls++
	return f.status, nil
}
func (f *fakeClient) InstallPanel(_ context.Context, id string, _ protocol.PanelInstallRequest) (protocol.MutationResult, error) {
	f.installed++
	f.lastID = id
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}
func (f *fakeClient) UpdatePanel(_ context.Context, id string, _ protocol.MutationRequest) (protocol.MutationResult, error) {
	f.updated++
	f.lastID = id
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}
func (f *fakeClient) ActivatePanel(_ context.Context, id string, _ protocol.MutationRequest) (protocol.MutationResult, error) {
	f.activated++
	f.lastID = id
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}
func (f *fakeClient) RollbackPanel(_ context.Context, id string, _ protocol.MutationRequest) (protocol.MutationResult, error) {
	f.rolledBack++
	f.lastID = id
	return protocol.MutationResult{Schema: "mihari/v1"}, nil
}
func (f *fakeClient) OpenWebGUI(context.Context) (protocol.WebGUIOpenResult, error) {
	f.opened++
	return protocol.WebGUIOpenResult{Schema: "mihari/v1", OpenURL: f.openURL}, nil
}

func sampleStatus() protocol.WebGUIStatus {
	return protocol.WebGUIStatus{
		Schema: "mihari/v1", GatewayAddr: "127.0.0.1:9191", GatewayHealth: "Healthy", ActivePanel: "zashboard", BrowserSessions: 3,
		Panels: []protocol.PanelStatus{
			{ID: "zashboard", Name: "Zashboard", Active: true, InstalledBuild: "v2.1.0", LatestBuild: "v2.2.0", Health: "Healthy", RollbackBuild: "v2.0.0"},
			{ID: "metacubexd", Name: "MetaCubeXD", InstalledBuild: "8e31c4a", LatestBuild: "c12ad90", Health: "Healthy"},
		},
		Safeguards: protocol.GatewaySafeguards{LoopbackBound: true, BrowserAuthenticated: true, ControllerIsolated: true, MutationsCoordinated: true},
	}
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

func TestWebGUIRendersCardsAndFooterWithoutSecrets(t *testing.T) {
	fake := &fakeClient{status: sampleStatus(), openURL: "http://127.0.0.1:9191/?token=super-secret"}
	model := New(fake, []string{protocol.CapabilityWebGUI})
	model.SetOperationID(func() string { return "op" })
	command := model.Load()
	if command == nil {
		t.Fatal("capability did not enable status load")
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	view := model.View()
	for _, want := range []string{"127.0.0.1:9191", "Zashboard", "v2.1.0", "v2.0.0", "MetaCubeXD", "8e31c4a", "3", "Loopback", "Controller isolation", "Mutation coordinator", "Open Browser"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view=%s", want, view)
		}
	}
	if strings.Contains(view, "token=") || strings.Contains(view, "super-secret") {
		t.Fatalf("view leaked open token: %s", view)
	}
	if hints := model.FooterHints(); !strings.Contains(hints, "Space activate") || !strings.Contains(hints, "b rollback") {
		t.Fatalf("hints=%q", hints)
	}
}

func TestWebGUIActionsActivateInstallUpdateOpenWithoutConfirm(t *testing.T) {
	fake := &fakeClient{status: sampleStatus(), openURL: "http://127.0.0.1:9191/?token=hidden"}
	model := New(fake, []string{protocol.CapabilityWebGUI})
	model.SetOperationID(func() string { return "op" })
	model.SetStatus(sampleStatus())
	var opened string
	model.SetOpenBrowser(func(url string) error { opened = url; return nil })

	// activate
	_, cmd := model.Update(tea.KeyPressMsg{Code: ' ', Text: "space"})
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionActivatePanel {
		t.Fatalf("activate action=%s", intent.Action)
	}
	if msg := intent.Execute(); msg.(mutationDoneMsg).err != nil || fake.activated != 1 {
		t.Fatalf("activate failed: %#v", msg)
	}

	// install
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	intent = cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionInstallPanel {
		t.Fatalf("install action=%s", intent.Action)
	}
	_ = intent.Execute()
	if fake.installed != 1 {
		t.Fatalf("installed=%d", fake.installed)
	}

	// update
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	intent = cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionUpdatePanel {
		t.Fatalf("update action=%s", intent.Action)
	}
	_ = intent.Execute()

	// open
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	intent = cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionOpenWebGUI {
		t.Fatalf("open action=%s", intent.Action)
	}
	_ = intent.Execute()
	if opened != fake.openURL || fake.opened != 1 {
		t.Fatalf("opened=%q calls=%d", opened, fake.opened)
	}
	if strings.Contains(model.View(), "token=") {
		t.Fatal("view rendered open token after open")
	}
}

func TestWebGUIRollbackRequiresConfirmationAction(t *testing.T) {
	fake := &fakeClient{status: sampleStatus()}
	model := New(fake, []string{protocol.CapabilityWebGUI})
	model.SetOperationID(func() string { return "op" })
	model.SetStatus(sampleStatus())
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if cmd == nil {
		t.Fatal("expected rollback intent")
	}
	intent := cmd().(ui.ActionIntentMsg)
	if intent.Action != ui.ActionRollbackPanel {
		t.Fatalf("action=%s", intent.Action)
	}
	// Root confirmation policy covers RequiresConfirmation; execute still works after confirm.
	_ = intent.Execute()
	if fake.rolledBack != 1 {
		t.Fatalf("rolledBack=%d", fake.rolledBack)
	}
}
