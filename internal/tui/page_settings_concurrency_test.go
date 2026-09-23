package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestPageSettings_LatencyConcurrencyDefaultAndAdjustment(t *testing.T) {
	d := newPageSettings(ui.PageProxies, protocol.TUIPreferences{})
	for range 3 {
		d.key("down")
	}
	view := ansi.Strip(d.view(100, 28))
	if !strings.Contains(view, "Test concurrency") || !strings.Contains(view, "< 5 >") {
		t.Fatalf("missing concurrency setting with default 5:\n%s", view)
	}
	d.key("right")
	if view := ansi.Strip(d.view(100, 28)); !strings.Contains(view, "< 6 >") {
		t.Fatalf("right did not increase concurrency:\n%s", view)
	}
	d.key("left")
	if view := ansi.Strip(d.view(100, 28)); !strings.Contains(view, "< 5 >") {
		t.Fatalf("left did not decrease concurrency:\n%s", view)
	}
}

func TestPageSettings_LatencyConcurrencyBoundsAndLayout(t *testing.T) {
	d := newPageSettings(ui.PageProxies, protocol.TUIPreferences{})
	d.focus = settingsFocus{1, 2}
	for range 60 {
		d.key("left")
	}
	if d.draft.LatencyTestConcurrency != 1 {
		t.Fatalf("minimum=%d", d.draft.LatencyTestConcurrency)
	}
	for range 60 {
		d.key("right")
	}
	if d.draft.LatencyTestConcurrency != 50 || !d.draft.ExtraLatency || !d.draft.AutoLatencyTest {
		t.Fatalf("maximum changed unrelated settings: %+v", d.draft)
	}
	for _, size := range [][2]int{{72, 22}, {100, 28}} {
		view := d.view(size[0], size[1])
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("view exceeds %v", size)
		}
		for _, label := range []string{"Test concurrency", "< 50 >", "←/→", "[Save]", "Ctrl+S"} {
			if !strings.Contains(ansi.Strip(view), label) {
				t.Fatalf("missing %s at %v:\n%s", label, size, view)
			}
		}
	}
}

func TestPageSettings_LatencyConcurrencySaveAndCancel(t *testing.T) {
	for _, save := range []bool{false, true} {
		m := NewModel()
		m.active = ui.PageProxies
		client := &settingsTestClient{}
		m.preferencesClient = client
		m.applyPreferences(protocol.TUIPreferences{Revision: 7})
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF4})
		m = next.(Model)
		for range 3 {
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m = next.(Model)
		}
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = next.(Model)
		if m.preferences.EffectiveProxies().LatencyTestConcurrency != 5 {
			t.Fatal("draft changed live preferences")
		}
		want, calls := 5, 0
		if save {
			_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if cmd == nil {
				t.Fatal("save command missing")
			}
			msg := cmd()
			if batch, ok := msg.(tea.BatchMsg); ok {
				msg = batch[0]()
			}
			next, _ = m.Update(msg)
			want, calls = 6, 1
		} else {
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		}
		m = next.(Model)
		if client.calls != calls || m.preferences.EffectiveProxies().LatencyTestConcurrency != want {
			t.Fatalf("save=%t calls=%d preferences=%+v", save, client.calls, m.preferences)
		}
		if save {
			if m.pageSettings == nil || !m.pageSettings.showDone || m.pageSettings.draft.LatencyTestConcurrency != want {
				t.Fatalf("save left dialog=%v", m.pageSettings)
			}
		} else if m.pageSettings != nil {
			t.Fatal("cancel did not close Page Settings")
		}
		if save && (client.request.Proxies.LatencyTestConcurrency != 6 || *client.request.IfRevision != 7 || client.request.ConnectionsColumns != nil || client.request.LogLevels != nil) {
			t.Fatalf("wrong save request: %+v", client.request)
		}
		if got := newPageSettings(ui.PageProxies, m.preferences).draft.LatencyTestConcurrency; got != want {
			t.Fatalf("reopened dialog value=%d want=%d", got, want)
		}
	}
}
