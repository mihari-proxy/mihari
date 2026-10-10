package system

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestCoreProgress_CoreSummaryAnimatesUntilResult(t *testing.T) {
	for _, tc := range []struct {
		name, version, note string
		action              ui.Action
		kind                actionKind
	}{
		{"install", "", ui.CoreProgressInstalling, ui.ActionUpdateCore, actionUpdate},
		{"update", "v1.19.0", ui.CoreProgressUpdating, ui.ActionUpdateCore, actionUpdate},
		{"reinstall", "v1.19.0", ui.CoreProgressReinstalling, ui.ActionReinstallCore, actionReinstall},
		{"switch", "v1.19.0", ui.CoreProgressSwitching, ui.ActionSwitchCoreChannel, actionSwitchChannel},
		{"restart", "v1.19.0", ui.CoreProgressRestarting, ui.ActionRestartCore, actionRestart},
	} {
		for _, failed := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/success", true: "/failure"}[failed], func(t *testing.T) {
				m := New(&fakeClient{}, func() string { return "core-progress" })
				m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: tc.version, Channel: "stable", Status: "stopped"})
				m.SetMutationsEnabled(true)
				_, cmd := m.Update(ui.ActionPendingMsg{Action: tc.action})
				if cmd == nil {
					t.Fatal("core operation did not schedule animation")
				}
				start := cmd().(ui.PageResultMsg).Result.(startRowSpinMsg)
				_, tick := m.Update(start)
				if tick == nil {
					t.Fatal("core operation did not start timer")
				}
				coreLine := func() string {
					for _, line := range strings.Split(m.View(), "\n") {
						if strings.Contains(line, ui.MihomoCoreLabel) && strings.Contains(line, "stable") {
							return line
						}
					}
					t.Fatal("missing core summary")
					return ""
				}
				before := coreLine()
				for frame := 1; frame <= 3; frame++ {
					at := time.Unix(0, int64(time.Duration(frame)*rowSpinInterval))
					_, tick = m.Update(rowSpinTickMsg{gen: start.gen, t: at})
					after := coreLine()
					if tick == nil || after == before || !strings.Contains(after, ui.SpinnerLabel(at, tc.note)) {
						t.Fatalf("core summary did not animate: %s", after)
					}
					if !strings.Contains(after, "stable") || (tc.version != "" && !strings.Contains(after, tc.version)) {
						t.Fatalf("core progress hid version/channel: %s", after)
					}
					before = after
				}
				var err error
				if failed {
					err = errors.New("fixture download failed")
				}
				m.Update(actionResultMsg{kind: tc.kind, err: err})
				if strings.Contains(coreLine(), tc.note) {
					t.Fatal("core summary still shows progress after result")
				}
				_, tick = m.Update(rowSpinTickMsg{gen: start.gen, t: time.Unix(1, 0)})
				if tick != nil {
					t.Fatal("core operation kept scheduling animation after result")
				}
			})
		}
	}
}

func TestCoreProgress_NarrowSummaryKeepsSpinner(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0", Status: "running", Channel: "stable"})
	m.SetMutationsEnabled(true)
	m.SetSize(55, 60)
	m.Update(ui.ActionPendingMsg{Action: ui.ActionUpdateCore})
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, ui.MihomoCoreLabel) && strings.Contains(line, ui.SpinnerLabel(time.Unix(0, 0), ui.CoreProgressUpdating)) {
			return
		}
	}
	t.Fatalf("narrow core summary hid update animation:\n%s", m.View())
}

func TestCoreProgress_UnrelatedActionDoesNotAnimateCoreSummary(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Channel: "stable"})
	m.SetMutationsEnabled(true)
	m.Update(ui.ActionPendingMsg{Action: ui.ActionServiceRestart})
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, ui.MihomoCoreLabel) && strings.Contains(line, ui.ServiceProgressRestarting) {
			t.Fatalf("service operation appeared on core summary: %s", line)
		}
	}
}
