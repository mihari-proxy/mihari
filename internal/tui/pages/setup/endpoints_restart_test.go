package setup

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSetupSavedEndpoints_RequestsAutomaticApplication(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	status := m.status
	status.RestartRequired = true
	_, cmd := m.handleEndpointsSaved(endpointsSavedMsg{gen: m.executionGen, status: status})
	if cmd == nil {
		t.Fatal("saving endpoints only displayed restart instructions")
	}
	request, ok := cmd().(ui.ApplySavedEndpointsMsg)
	if !ok || request.Page != ui.PageSetup || request.Retry || !reflect.DeepEqual(request.Status, status) {
		t.Fatalf("request=%+v", request)
	}
}

func TestSetupResumePendingEndpoints_RequestsAutomaticApplication(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.status.RestartRequired = true
	m.SetAutomatic(true)
	m.portProbe = [3]portState{portFree, portFree, portFree}
	cmd := m.resumeFromState()
	if cmd == nil {
		t.Fatal("reopening TUI did not handle pending endpoint application")
	}
	if _, ok := cmd().(ui.ApplySavedEndpointsMsg); !ok {
		t.Fatal("missing restart request")
	}
}

func TestSetupEndpointRestart_ExplicitRetryAndProgress(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.waitingRestart = true
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("restart recovery has no retry action")
	}
	request, ok := cmd().(ui.ApplySavedEndpointsMsg)
	if !ok || !request.Retry {
		t.Fatal("missing explicit retry")
	}
	m.SetEndpointRestartState(true, "")
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd != nil {
		t.Fatal("pending restart allowed a second attempt")
	}
}
