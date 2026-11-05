package tui

import (
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

func TestPerformanceSettings_RendersAndSaves(t *testing.T) {
	m := NewModel()
	client := &settingsTestClient{}
	m.preferencesClient = client
	m.applyPreferences(protocol.TUIPreferences{Revision: 7, Performance: &protocol.PerformancePreferences{ObservationReuseSeconds: 3}})
	d := newPageSettings(ui.PageOverview, m.preferences)
	m.pageSettings = d
	last := len(d.pages) - 1
	d.directory = last
	d.focus = settingsFocus{last, 0}
	if view := d.view(120, 40); !strings.Contains(view, "Performance") || !strings.Contains(view, "Controller owner detection") {
		t.Fatal("Performance section missing")
	}
	d.key("space")
	d.focus = settingsFocus{last, 1}
	for range 10 {
		d.key("right")
	}
	cmd := m.savePageSettings()
	if cmd == nil {
		t.Fatal("no save")
	}
	m.updatePageSettings(cmd())
	if client.request.Performance == nil || !client.request.Performance.ControllerOwnerDetection || client.request.Performance.ObservationReuseSeconds != 5 {
		t.Fatalf("request=%+v", client.request)
	}
	if client.request.Proxies == nil {
		t.Fatal("single Save omitted proxy draft")
	}
	if m.preferences.EffectivePerformance().ObservationReuseSeconds != 5 || !d.showDone {
		t.Fatal("successful save did not apply immediately")
	}
	for range 10 {
		d.key("left")
	}
	if cmd = m.savePageSettings(); cmd == nil {
		t.Fatal("zero reuse must be saved")
	}
	m.updatePageSettings(cmd())
	if client.request.Performance.ObservationReuseSeconds != 0 {
		t.Fatal("minimum must be zero")
	}
}

func TestPerformanceSettings_FailureKeepsDraftAndRetryUsesNewOperation(t *testing.T) {
	m := NewModel()
	client := &settingsTestClient{err: errors.New("save failed")}
	m.preferencesClient = client
	m.applyPreferences(protocol.TUIPreferences{Revision: 7, Performance: &protocol.PerformancePreferences{ObservationReuseSeconds: 3}})
	d := newPageSettings(ui.PageOverview, m.preferences)
	m.pageSettings = d
	last := len(d.pages) - 1
	d.directory = last
	d.focus = settingsFocus{last, 0}
	d.key("space")
	cmd := m.savePageSettings()
	if cmd == nil {
		t.Fatal("no save")
	}
	m.updatePageSettings(cmd())
	firstID := client.request.OperationID
	if d.showDone || d.err == "" || !d.draftPerformance.ControllerOwnerDetection || m.preferences.EffectivePerformance().ControllerOwnerDetection {
		t.Fatal("failed save did not keep draft/old values")
	}
	client.err = nil
	cmd = m.savePageSettings()
	if cmd == nil {
		t.Fatal("manual retry disabled")
	}
	m.updatePageSettings(cmd())
	if client.request.OperationID == firstID || !d.showDone || !m.preferences.EffectivePerformance().ControllerOwnerDetection {
		t.Fatal("retry did not commit a new operation")
	}
}
