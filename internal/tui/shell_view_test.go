package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestShellView_FullIncludesStatusBarWithoutRailMonitor(t *testing.T) {
	model := NewModel()
	model.width, model.height = 100, 28
	model.resizePages()
	model.core = protocol.CoreStatus{Status: "running", Version: "v1.19.0"}
	model.subscriptions = protocol.SubscriptionList{
		ActiveID: "main",
		Subscriptions: []protocol.Subscription{
			{ID: "main", Name: "Main", Enabled: true},
		},
	}
	model.monitor.ObserveTraffic(12*1024*1024/10, 41*1024*1024/10)
	model.monitor.ObserveMemory(84 * 1024 * 1024)
	conns := make([]protocol.Connection, 12)
	for i := range conns {
		conns[i] = protocol.Connection{ID: string(rune('a' + i))}
	}
	model.monitor.ObserveConnections(protocol.ConnectionList{Connections: conns})

	plain := normalizeRender(model.View().Content)
	lines := nonEmptyLines(plain)
	if len(lines) == 0 {
		t.Fatal("empty view")
	}
	first := lines[0]
	for _, want := range []string{ui.AppName, "running", "Main", "12 conn", "↑", "↓"} {
		if !strings.Contains(first, want) {
			t.Fatalf("status bar missing %q in first line %q\nfull:\n%s", want, first, plain)
		}
	}
	// Large rail monitor used stacked totals; status shell must not reintroduce them.
	if strings.Contains(plain, ui.MonitorUploadTotal) || strings.Contains(plain, ui.MonitorDownloadTotal) {
		t.Fatalf("rail still shows large monitor totals:\n%s", plain)
	}
	footer := lines[len(lines)-1]
	if strings.Count(footer, "\n") != 0 {
		t.Fatalf("footer is not a single line: %q", footer)
	}
}

func TestShellView_CompactFooterOmitsMonitorSummary(t *testing.T) {
	model := NewModel()
	model.width, model.height = 72, 22
	model.resizePages()
	model.monitor.ObserveTraffic(1024, 2048)
	model.monitor.ObserveMemory(4096)
	model.monitor.ObserveConnections(protocol.ConnectionList{
		Connections: []protocol.Connection{{ID: "one"}},
	})

	plain := normalizeRender(model.View().Content)
	lines := nonEmptyLines(plain)
	if len(lines) == 0 || !strings.Contains(lines[0], ui.AppName) {
		t.Fatalf("compact status missing AppName:\n%s", plain)
	}
	footer := lines[len(lines)-1]
	// ViewSummary used to append "Connections N  UL …  DL …  MEM …" on the footer.
	if strings.Contains(footer, ui.MonitorConnectionsLabel) ||
		strings.Contains(footer, ui.MonitorMemoryShort) ||
		strings.Contains(footer, ui.MonitorUploadShort+" ") {
		t.Fatalf("compact footer still appends monitor summary: %q", footer)
	}
	if strings.Contains(plain, ui.MonitorUploadTotal) {
		t.Fatalf("compact view still shows Uploaded:\n%s", plain)
	}
}

func TestShellView_PendingFooterUsesBrailleSpinner(t *testing.T) {
	model := NewModel()
	model.width, model.height = 100, 28
	model.resizePages()
	model.pendingActions = map[string]ui.Action{"proxy:GLOBAL": ui.ActionSelectProxy}
	model.globalState = ui.StatePending
	model.now = time.Unix(0, 0)

	plain := normalizeRender(model.View().Content)
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	found := false
	for _, frame := range frames {
		if strings.Contains(plain, frame) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pending footer missing braille spinner frame:\n%s", plain)
	}
	if !strings.Contains(plain, ui.GlobalStatePendingLabel) {
		t.Fatalf("pending footer missing %q:\n%s", ui.GlobalStatePendingLabel, plain)
	}
}

func TestShellView_StaleUsesStatusPrefixNotSpinner(t *testing.T) {
	model := NewModel()
	model.width, model.height = 100, 28
	model.resizePages()
	model.stale = true
	model.globalState = ui.StateStale
	model.monitor.SetStale(true)

	plain := normalizeRender(model.View().Content)
	if !strings.Contains(plain, "STALE") {
		t.Fatalf("stale status bar missing STALE:\n%s", plain)
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for _, frame := range frames {
		if strings.Contains(plain, frame) {
			t.Fatalf("stale-only view must not spin with %q:\n%s", frame, plain)
		}
	}
	if !strings.Contains(plain, ui.GlobalStateStaleLabel) {
		t.Fatalf("stale footer missing label:\n%s", plain)
	}
}

func TestShell_SpinnerTickWhilePendingAndStopsWhenIdle(t *testing.T) {
	model := NewModel()
	model.mutationsEnabled = true
	model.status.Capabilities = []string{protocol.CapabilityProxies}
	updated, cmd := model.Update(ui.ActionIntentMsg{
		Action: ui.ActionSelectProxy, Capability: protocol.CapabilityProxies, Key: "proxy:GLOBAL",
		Execute: func() tea.Msg { return nil },
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected execute command batch while pending")
	}
	if model.globalState != ui.StatePending || len(model.pendingActions) != 1 {
		t.Fatalf("state=%q pending=%v", model.globalState, model.pendingActions)
	}

	// Tick while still pending should advance now and re-schedule.
	updated, cmd = model.Update(spinnerTickMsg{t: time.Unix(1, 0)})
	model = updated.(Model)
	if !model.now.Equal(time.Unix(1, 0)) {
		t.Fatalf("now=%v", model.now)
	}
	if cmd == nil {
		t.Fatal("expected re-tick while pending")
	}

	// Complete the action without draining the real tea.Tick delay.
	updated, _ = model.Update(actionCompletedMsg{
		Intent: ui.ActionIntentMsg{Key: "proxy:GLOBAL"},
	})
	model = updated.(Model)
	if len(model.pendingActions) != 0 {
		t.Fatalf("pending remaining: %v", model.pendingActions)
	}

	updated, cmd = model.Update(spinnerTickMsg{t: time.Unix(2, 0)})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("should not re-tick when idle")
	}
	if !model.now.Equal(time.Unix(2, 0)) {
		t.Fatalf("now=%v after idle tick", model.now)
	}
}

func nonEmptyLines(view string) []string {
	raw := strings.Split(view, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
