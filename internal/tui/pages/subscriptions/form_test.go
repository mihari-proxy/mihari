package subscriptions

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestForm_TabTraversalAndEscCancel(t *testing.T) {
	form := newAddForm()
	if form.index != 0 || !form.inputs[0].Focused() {
		t.Fatalf("initial form=%#v", form)
	}
	form.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if form.index != 1 || !form.inputs[1].Focused() {
		t.Fatalf("after tab index=%d", form.index)
	}
	form.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if form.index != 0 {
		t.Fatalf("after shift-tab index=%d", form.index)
	}
	closed, _ := form.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !closed {
		t.Fatal("esc did not cancel form")
	}
}

func TestEditForm_LeavesURLBlankAndBuildsTypedUpdate(t *testing.T) {
	form := newEditForm(protocol.Subscription{Name: "Main", Interval: "6h", AutoRefresh: true})
	if form.inputs[1].Value() != "" {
		t.Fatalf("URL was exposed: %q", form.inputs[1].Value())
	}
	request := form.updateRequest("op-1", 7)
	if request.URL != nil || request.Name == nil || *request.Name != "Main" || request.Interval == nil || *request.Interval != "6h" || request.AutoRefresh == nil || !*request.AutoRefresh || request.IfRevision == nil || *request.IfRevision != 7 {
		t.Fatalf("request=%#v", request)
	}
}
