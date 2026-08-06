package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// applyRootCmd expands BatchMsg and applies non-blocking messages. tea.Tick
// spinner cmds are not executed (they sleep); call spinnerTickMsg directly to
// exercise the animation loop.
func applyRootCmd(model Model, cmd tea.Cmd) (Model, tea.Cmd) {
	if cmd == nil {
		return model, nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var pending tea.Cmd
		for _, child := range batch {
			var next tea.Cmd
			model, next = applyRootCmd(model, child)
			if next != nil {
				pending = next
			}
		}
		return model, pending
	}
	updated, next := model.Update(msg)
	return updated.(Model), next
}

func TestGlobalActionDispatcherGatesStaleCapabilityConfirmationAndPending(t *testing.T) {
	runs := 0
	intent := ui.ActionIntentMsg{
		Action: ui.ActionUpdateCore, Capability: protocol.CapabilityCore, Key: "core-update",
		Title: "Update", Object: "core", Impact: "replace", Rollback: "retained",
		Execute: func() tea.Msg { runs++; return nil },
	}
	model := NewModel()
	updated, command := model.Update(intent)
	model = updated.(Model)
	if command != nil || runs != 0 || model.globalState != ui.StateStale {
		t.Fatalf("stale command=%v runs=%d state=%s", command != nil, runs, model.globalState)
	}

	model.mutationsEnabled = true
	updated, command = model.Update(intent)
	model = updated.(Model)
	if command != nil || model.globalState != ui.StateCapabilityLost {
		t.Fatalf("capability command=%v state=%s", command != nil, model.globalState)
	}

	model.status.Capabilities = []string{protocol.CapabilityCore}
	updated, command = model.Update(intent)
	model = updated.(Model)
	if command != nil || model.modal == nil || runs != 0 {
		t.Fatalf("confirmation command=%v modal=%v runs=%d", command != nil, model.modal != nil, runs)
	}
	model.modal.selected = 0
	updated, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("confirm did not return actionExecute command")
	}
	// confirmationCmd yields actionExecuteMsg → executeAction (pending + Batch).
	updated, command = model.Update(command())
	model = updated.(Model)
	if command == nil || model.globalState != ui.StatePending {
		t.Fatalf("execute command=%v state=%s", command != nil, model.globalState)
	}
	updated, duplicate := model.Update(intent)
	model = updated.(Model)
	if duplicate != nil || model.globalState != ui.StatePending {
		t.Fatalf("duplicate=%v state=%s", duplicate != nil, model.globalState)
	}
	model, _ = applyRootCmd(model, command)
	if runs != 1 || len(model.pendingActions) != 0 {
		t.Fatalf("runs=%d pending=%v", runs, model.pendingActions)
	}
}

func TestGlobalActionDispatcherRunsSafeActionWithoutConfirmation(t *testing.T) {
	runs := 0
	model := NewModel()
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityProxies}
	updated, command := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionSelectProxy, Capability: protocol.CapabilityProxies, Key: "proxy:GLOBAL",
		Execute: func() tea.Msg { runs++; return nil },
	})
	model = updated.(Model)
	if command == nil || model.modal != nil {
		t.Fatalf("command=%v modal=%v", command != nil, model.modal != nil)
	}
	model, _ = applyRootCmd(model, command)
	if runs != 1 || len(model.pendingActions) != 0 {
		t.Fatalf("runs=%d pending=%v", runs, model.pendingActions)
	}
}

type asyncMarkerMsg struct{}

type recordingPage struct{ received int }

func (p *recordingPage) ID() ui.PageID    { return ui.PageSystem }
func (p *recordingPage) SetSize(int, int) {}
func (p *recordingPage) FocusFirst()      {}
func (p *recordingPage) View() string     { return "" }
func (p *recordingPage) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	if _, ok := message.(asyncMarkerMsg); ok {
		p.received++
	}
	return p, nil
}

func TestGlobalActionResultReturnsToOriginPageAfterNavigation(t *testing.T) {
	page := &recordingPage{}
	model := NewModel()
	model.pages[ui.PageSystem] = page
	model.active = ui.PageSystem
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSystem}
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityProxies}
	updated, command := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionSelectProxy, Page: ui.PageSystem, Capability: protocol.CapabilityProxies,
		Key: "async", Execute: func() tea.Msg { return asyncMarkerMsg{} },
	})
	model = updated.(Model)
	model.active = ui.PageOverview
	model.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageOverview}
	model, _ = applyRootCmd(model, command)
	if page.received != 1 || len(model.pendingActions) != 0 {
		t.Fatalf("received=%d pending=%v", page.received, model.pendingActions)
	}
}

func TestConfirmationActionResultRoutesToOriginPageWithoutRetry(t *testing.T) {
	executes := 0
	page := &recordingPage{}
	model := NewModel()
	model.pages[ui.PageSystem] = page
	model.active = ui.PageSystem
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSystem}
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityCore}
	updated, command := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionRestartCore, Page: ui.PageSystem, Capability: protocol.CapabilityCore, Key: "restart",
		Title: "Restart", Object: "core", Impact: "interrupt", Rollback: "policy",
		Execute: func() tea.Msg { executes++; return asyncMarkerMsg{} },
	})
	model = updated.(Model)
	if model.modal == nil {
		t.Fatal("confirmation modal did not open")
	}
	model.modal.selected = 0
	updated, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	// Enter queues actionExecuteMsg; apply it to enter pending without completing yet.
	if command == nil {
		t.Fatal("confirmation did not return execute command")
	}
	executeMsg := command()
	updated, command = model.Update(executeMsg)
	model = updated.(Model)
	if command == nil || len(model.pendingActions) != 1 {
		t.Fatalf("confirmation did not enter pending: command=%v pending=%v", command != nil, model.pendingActions)
	}
	// Navigate away during the pending window; the result must still reach the origin page.
	model.active = ui.PageOverview
	model.focus = ui.Focus{Area: ui.FocusRail, Page: ui.PageOverview}
	model, _ = applyRootCmd(model, command)
	if executes != 1 || len(model.pendingActions) != 0 || page.received != 1 {
		t.Fatalf("executes=%d pending=%v received=%d (no blind retry, result routed)", executes, model.pendingActions, page.received)
	}
}
