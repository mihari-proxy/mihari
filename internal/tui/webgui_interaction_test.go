package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	webguipage "github.com/mihari-proxy/mihari/internal/tui/pages/webgui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestModel_WebGUIRightAndTabReachManage(t *testing.T) {
	for _, key := range []rune{tea.KeyRight, tea.KeyTab} {
		model := goldenModel(t, ui.PageWebGUI, 160, 35)
		page := model.pages[ui.PageWebGUI].(*webguipage.Model)
		page.SetCapabilities([]string{protocol.CapabilityWebGUI})
		page.SetStatus(protocol.WebGUIStatus{Panels: []protocol.PanelStatus{
			{ID: "zashboard", Name: "Zashboard", InstalledBuild: "v1"},
			{ID: "metacubexd", Name: "MetaCubeXD"},
		}})
		model = updateModelKey(t, model, tea.KeyPressMsg{Code: key})
		model = updateModelKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
		if !strings.Contains(ansi.Strip(model.View().Content), "Manage Zashboard") {
			t.Fatal("shell navigation skipped Manage")
		}
	}
}

func TestModel_WebGUIInstallTickReturnsToOriginPage(t *testing.T) {
	model := goldenModel(t, ui.PageWebGUI, 160, 35)
	page := model.pages[ui.PageWebGUI].(*webguipage.Model)
	page.SetCapabilities([]string{protocol.CapabilityWebGUI})
	page.SetStatus(protocol.WebGUIStatus{Panels: []protocol.PanelStatus{{ID: "zashboard", Name: "Zashboard"}}})
	_, timer := page.Update(ui.ActionPendingMsg{Page: ui.PageWebGUI, Action: ui.ActionInstallPanel, Key: "panel:install:zashboard"})
	if timer == nil {
		t.Fatal("install did not schedule a timer")
	}
	message, ok := timer().(ui.PageResultMsg)
	if !ok || message.Page != ui.PageWebGUI {
		t.Fatal("timer did not identify its origin page")
	}
	model.active = ui.PageOverview
	model.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageOverview}
	updated, next := model.Update(message)
	if updated.(Model).active != ui.PageOverview || next == nil || !strings.Contains(page.View(), "Installing") {
		t.Fatal("off-page timer lost progress or changed the active page")
	}
}
