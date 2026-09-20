package tui

import (
	lipgloss "charm.land/lipgloss/v2"
	"context"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPageSettings_F4OpensFromRailAndEscapeReturns(t *testing.T) {
	m := NewModel()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF4})
	m = next.(Model)
	if view := m.View().Content; !strings.Contains(view, "Page Settings") || !strings.Contains(view, "No settings available yet") || !strings.Contains(view, "Ctrl+S") {
		t.Fatalf("settings not opened from Overview rail:\n%s", view)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if strings.Contains(m.View().Content, "No settings available yet") {
		t.Fatal("Escape did not close settings")
	}
}

func TestPageSettings_JumpExpandsAndFocusLinksDirectory(t *testing.T) {
	d := newPageSettings(ui.PageProxies, protocol.TUIPreferences{})
	if !d.expanded[ui.PageProxies] || d.expanded[ui.PageOverview] {
		t.Fatal("wrong initial expansion")
	}
	d.key("shift+tab")
	d.key("down")
	d.key("enter")
	if d.area != 1 || d.focus != (settingsFocus{2, -1}) || !d.expanded[ui.PageConnections] || !d.expanded[ui.PageProxies] {
		t.Fatalf("jump=%+v", d)
	}
	d.key("up")
	if d.directory != 1 || d.focus != (settingsFocus{1, 1}) {
		t.Fatalf("directory did not follow focus: %+v", d)
	}
	d.key("space")
	if d.draft.AutoLatencyTest {
		t.Fatal("checkbox did not toggle")
	}
	d.key("tab")
	if d.area != 2 {
		t.Fatal("Tab must jump directly to Cancel")
	}
	d.key("right")
	if d.area != 3 || d.key("enter") != ModalConfirm {
		t.Fatal("button navigation failed")
	}
	for area := 0; area < 4; area++ {
		d.area = area
		if d.key("ctrl+s") != ModalConfirm {
			t.Fatalf("Ctrl+S area=%d", area)
		}
	}
}

func TestPageSettings_LayoutFitsAndKeepsFooter(t *testing.T) {
	for _, size := range [][2]int{{72, 22}, {100, 28}, {180, 42}} {
		d := newPageSettings(ui.PageProxies, protocol.TUIPreferences{})
		for _, id := range d.pages {
			d.expanded[id] = true
		}
		d.focus = settingsFocus{7, -1}
		d.directory = 7
		view := d.view(size[0], size[1])
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("view exceeds %v: %dx%d", size, lipgloss.Width(view), lipgloss.Height(view))
		}
		plain := ansi.Strip(view)
		for _, label := range []string{"System", "No settings available yet", "[Save]", "Ctrl+S", "[Cancel]"} {
			if !strings.Contains(plain, label) {
				t.Fatalf("missing %s at %v:\n%s", label, size, plain)
			}
		}
	}
}

type settingsTestClient struct {
	calls    int
	request  protocol.UpdateTUIPreferencesRequest
	err      error
	warnings protocol.WarningOutcome
}

func (c *settingsTestClient) UpdateTUIPreferences(_ context.Context, req protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
	c.calls++
	c.request = req
	return protocol.TUIPreferences{Revision: 8, Proxies: req.Proxies, WarningOutcome: c.warnings}, c.err
}

func TestPageSettings_FullSaveFailureReachesF2(t *testing.T) {
	m := NewModel()
	m.active = ui.PageProxies
	original := "synthetic save failure\nserver details\n" + strings.Repeat("full diagnostic detail ", 100)
	m.preferencesClient = &settingsTestClient{err: errors.New(original)}
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.ExtraLatency = false
	next, _ := m.Update(m.savePageSettings()())
	m = next.(Model)
	if len(m.diagnosticWindow.entries) != 1 || !strings.Contains(m.diagnosticWindow.entries[0].snapshot.Detail, original) {
		t.Fatal("save failure lost full diagnostic detail")
	}
	next, _ = m.Update(diagnosticKey(tea.KeyF2))
	m = next.(Model)
	if !m.diagnosticWindow.open || !strings.Contains(m.diagnosticWindow.pinned.Detail, original) || m.pageSettings == nil {
		t.Fatal("F2 did not preserve full failure and underlying settings draft")
	}
}

func TestPageSettings_CommittedWarningReachesF2(t *testing.T) {
	m := NewModel()
	m.active = ui.PageProxies
	warning := protocol.Diagnostic{ID: "fixture:page-settings", State: protocol.DiagnosticAvailable, Severity: "warning", Summary: "saved with synchronization warning", Detail: "full warning\nsynthetic detail"}
	m.preferencesClient = &settingsTestClient{warnings: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: warning.Summary, Diagnostic: &warning}}}}
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.ExtraLatency = false
	next, _ := m.Update(m.savePageSettings()())
	m = next.(Model)
	if m.pageSettings != nil || m.preferences.EffectiveProxies().ExtraLatency || len(m.diagnosticWindow.entries) != 1 {
		t.Fatal("warning changed save success or disappeared")
	}
	next, _ = m.Update(diagnosticKey(tea.KeyF2))
	if next.(Model).diagnosticWindow.pinned.Detail != warning.Detail {
		t.Fatal("F2 did not retain full committed warning")
	}
}

func TestPageSettings_SaveCommitsDraft(t *testing.T) {
	m := NewModel()
	c := &settingsTestClient{}
	m.preferencesClient = c
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.AutoLatencyTest = false
	cmd := m.savePageSettings()
	if cmd == nil || m.savePageSettings() != nil {
		t.Fatal("save missing or duplicate save allowed")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if c.calls != 1 || c.request.ConnectionsColumns != nil || *c.request.IfRevision != 7 || c.request.Proxies.AutoLatencyTest || m.pageSettings != nil || m.preferences.EffectiveProxies().AutoLatencyTest {
		t.Fatalf("request=%+v model=%+v", c.request, m.pageSettings)
	}
}

func TestPageSettings_CancelPreservesCommittedPreferences(t *testing.T) {
	m := NewModel()
	c := &settingsTestClient{}
	m.preferencesClient = c
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.ExtraLatency = false
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if c.calls != 0 || m.pageSettings != nil || !m.preferences.EffectiveProxies().ExtraLatency {
		t.Fatal("Cancel changed preferences")
	}
}

func TestPageSettings_FailureRetainsDraft(t *testing.T) {
	m := NewModel()
	c := &settingsTestClient{err: errors.New("synthetic save failure\nserver details\nmore details")}
	m.preferencesClient = c
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.ExtraLatency = false
	next, _ := m.Update(m.savePageSettings()())
	m = next.(Model)
	if m.pageSettings == nil || m.pageSettings.saving || m.pageSettings.draft.ExtraLatency || !strings.Contains(m.pageSettings.err, "synthetic save failure") || !m.preferences.EffectiveProxies().ExtraLatency {
		t.Fatal("failed save did not retain draft and committed state")
	}
	if strings.Contains(m.pageSettings.err, "\n") {
		t.Fatal("multiline failure breaks fixed dialog layout")
	}
}

func TestPageSettings_SaveResultFromPreviousDaemonDoesNotApply(t *testing.T) {
	m := NewModel()
	m.statusEpoch = 1
	m.preferencesClient = &settingsTestClient{}
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.ExtraLatency = false
	cmd := m.savePageSettings()
	m.statusEpoch = 2
	m.preferencesLoaded = false
	m.applyPreferences(protocol.TUIPreferences{Revision: 1})
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !m.preferences.EffectiveProxies().ExtraLatency || m.pageSettings == nil || m.pageSettings.err == "" {
		t.Fatal("previous daemon's save result applied after reconnect")
	}
}

func TestPageSettings_ReconnectingRejectsOutstandingSave(t *testing.T) {
	m := NewModel()
	m.statusEpoch = 1
	m.preferencesClient = &settingsTestClient{}
	m.applyPreferences(protocol.TUIPreferences{Revision: 7})
	m.pageSettings = newPageSettings(ui.PageProxies, m.preferences)
	m.pageSettings.draft.ExtraLatency = false
	cmd := m.savePageSettings()
	m.applySessionEvent(session.Event{Kind: session.EventReconnecting, Epoch: 2})
	// Reconnecting precedes the new status event, so statusEpoch is still 1.
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.preferencesLoaded || !m.preferences.EffectiveProxies().ExtraLatency || m.pageSettings == nil || m.pageSettings.saving || m.pageSettings.err == "" {
		t.Fatal("save from disconnected session applied or discarded the draft")
	}
}

func TestPageSettings_DelayedEntryDoesNotCoverAnotherPage(t *testing.T) {
	m := NewModel()
	p := m.pages[ui.PageProxies].(*proxypage.Model)
	p.SetRoutingAvailable(true, 1)
	p.FocusFirst()
	p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.active = ui.PageRules
	next, _ := m.Update(cmd())
	if next.(Model).pageSettings != nil {
		t.Fatal("late Proxies entry opened settings on another page")
	}
}
