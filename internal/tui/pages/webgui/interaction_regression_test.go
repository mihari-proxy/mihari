package webgui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestWebGUI_RefreshImmediatelyFollowsSummary(t *testing.T) {
	m := New(nil, []string{protocol.CapabilityWebGUI})
	m.SetStatus(sampleStatus())
	m.SetSize(160, 35)
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Browser refresh") {
			if i == 0 || !strings.HasPrefix(lines[i-1], "╰") {
				t.Fatalf("refresh callout has extra spacing above it: %q", lines[:i+1])
			}
			return
		}
	}
	t.Fatal("missing refresh callout")
}

// TestWebGUI_InstallAnimationLifetime verifies install, reinstall and update animation cleanup.
func TestWebGUI_InstallAnimationLifetime(t *testing.T) {
	for _, action := range []ui.Action{ui.ActionInstallPanel, ui.ActionReinstallPanel, ui.ActionUpdatePanel} {
		for _, failed := range []bool{false, true} {
			m := New(&fakeClient{}, []string{protocol.CapabilityWebGUI})
			m.SetStatus(sampleStatus())
			label := "Installing"
			command := m.installSelected()
			if action == ui.ActionReinstallPanel {
				command = m.reinstallSelected()
				label = "Reinstalling"
			}
			if action == ui.ActionUpdatePanel {
				command = m.updateSelected()
				label = "Updating"
			}
			intent := command().(ui.ActionIntentMsg)
			pending := ui.ActionPendingMsg{Page: intent.Page, Action: intent.Action, Key: intent.Key}
			_, timer := m.Update(pending)
			if timer == nil {
				t.Fatal("pending install must start animation")
			}
			if _, duplicate := m.Update(pending); duplicate != nil {
				t.Fatal("duplicate pending message started another timer")
			}
			generation := m.installSpinGen
			for frame := 0; frame < 3; frame++ {
				at := time.Unix(0, 0).Add(time.Duration(frame) * installSpinInterval)
				_, next := m.Update(installSpinTickMsg{at: at, gen: generation})
				badge := ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(at, label))
				if next == nil || !strings.Contains(m.View(), badge) {
					t.Fatalf("missing animated orange badge at frame %d", frame)
				}
			}
			// An unrelated action completing must not clear installation progress.
			m.Update(mutationDoneMsg{})
			if !strings.Contains(m.View(), label) {
				t.Fatal("unrelated result cleared installation")
			}
			result := intent.Execute().(mutationDoneMsg)
			if failed {
				result.err = errors.New("fixture install failure")
			}
			_, reload := m.Update(result)
			if reload == nil || strings.Contains(m.View(), label) {
				t.Fatal("completion must clear progress and reload status")
			}
			if failed && !strings.Contains(m.View(), "fixture install failure") {
				t.Fatal("installation failure disappeared")
			}
			if _, next := m.Update(installSpinTickMsg{at: time.Unix(1, 0), gen: generation}); next != nil {
				t.Fatal("completed animation kept scheduling ticks")
			}
			m.Update(pending)
			before := m.View()
			if _, next := m.Update(installSpinTickMsg{at: time.Unix(2, 0), gen: generation}); next != nil || before != m.View() {
				t.Fatal("old tick changed or duplicated new animation")
			}
		}
	}
}

func TestWebGUI_ConcurrentInstallsKeepIndependentBadges(t *testing.T) {
	m := New(&fakeClient{}, []string{protocol.CapabilityWebGUI})
	status := sampleStatus()
	m.SetStatus(status)
	first := m.installSelected()().(ui.ActionIntentMsg)
	m.handleKey("down")
	second := m.installSelected()().(ui.ActionIntentMsg)
	for i, intent := range []ui.ActionIntentMsg{first, second} {
		_, timer := m.Update(ui.ActionPendingMsg{Page: intent.Page, Action: intent.Action, Key: intent.Key})
		if (timer != nil) != (i == 0) {
			t.Fatal("concurrent installs must share one timer")
		}
	}
	status.Panels[0], status.Panels[1] = status.Panels[1], status.Panels[0]
	m.SetStatus(status)
	m.Update(first.Execute())
	if len(m.installing) != 1 || !strings.Contains(m.panelBody(status.Panels[0], 0, 70), "Installing") || strings.Contains(m.panelBody(status.Panels[1], 1, 70), "Installing") {
		t.Fatal("completion or reordered status moved another install's badge")
	}
	m.Update(second.Execute())
	if len(m.installing) != 0 {
		t.Fatal("pending installs remain after all results")
	}
}

func TestWebGUI_HorizontalNavigationVisitsManage(t *testing.T) {
	for _, width := range []int{58, 160} {
		m := New(nil, []string{protocol.CapabilityWebGUI})
		status := sampleStatus()
		status.Panels[1].InstalledBuild = ""
		m.SetStatus(status)
		m.SetSize(width, 35)
		for _, step := range []struct {
			key           string
			panel, action int
		}{
			{"right", 0, 1}, {"right", 1, 0}, {"left", 0, 1}, {"left", 0, 0},
			{"tab", 0, 1}, {"tab", 1, 0}, {"shift+tab", 0, 1},
		} {
			m.handleKey(step.key)
			if m.selected != step.panel || m.action != step.action {
				t.Fatalf("width=%d key=%s focus=(%d,%d), want=(%d,%d)", width, step.key, m.selected, m.action, step.panel, step.action)
			}
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if !strings.Contains(ansi.Strip(m.View()), "Manage Zashboard") {
			t.Fatal("focused Manage did not open")
		}
	}
}

func TestWebGUI_InstallPendingShowsBadgeOnOriginCard(t *testing.T) {
	f := &fakeClient{status: sampleStatus()}
	m := New(f, []string{protocol.CapabilityWebGUI})
	m.SetStatus(f.status)
	intent := m.installSelected()().(ui.ActionIntentMsg)
	// Selection can change between emitting an intent and executing it.
	m.handleKey("down")
	_, cmd := m.Update(ui.ActionPendingMsg{Page: intent.Page, Action: intent.Action, Key: intent.Key})
	if cmd == nil {
		t.Fatal("install did not schedule animation")
	}
	first := m.panelBody(m.status.Panels[0], 0, 70)
	second := m.panelBody(m.status.Panels[1], 1, 70)
	if !strings.Contains(first, "Installing") || strings.Contains(second, "Installing") {
		t.Fatalf("badge did not stay on origin: first=%s second=%s", first, second)
	}
	m.Update(intent.Execute())
	if strings.Contains(m.View(), "Installing") {
		t.Fatal("completed installation kept its badge")
	}
}
