package system

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSystemSavedEndpoints_RequestsAutomaticApplication(t *testing.T) {
	m := portsModel(t)
	status := m.onboarding
	status.RestartRequired = true
	_, cmd := m.Update(portsApplyResultMsg{status: status})
	if cmd == nil {
		t.Fatal("missing saved-port follow-up")
	}
	commands := cmd().(tea.BatchMsg)
	request, ok := commands[len(commands)-1]().(ui.ApplySavedEndpointsMsg)
	if !ok || request.Page != ui.PageSystem || request.Status.MixedAddr != status.MixedAddr {
		t.Fatalf("request=%+v", request)
	}
}
