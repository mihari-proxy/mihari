package logs

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestModel_ScrollingUpDisablesFollowAndGReturnsToNewest(t *testing.T) {
	model := New(10)
	model.SetSize(100, 12)
	model.Append(logAt("one", "info", 1))
	model.Append(logAt("two", "warn", 2))
	model.focus = focusRow
	model.focused = 1
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	model.Append(logAt("three", "error", 3))
	if model.following || model.Unread() != 1 || model.focused != 0 {
		t.Fatalf("following=%v unread=%d focused=%d", model.following, model.Unread(), model.focused)
	}
	model.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if !model.following || model.Unread() != 0 || model.focused != 2 {
		t.Fatalf("following=%v unread=%d focused=%d", model.following, model.Unread(), model.focused)
	}
}

func TestModel_PauseFreezesRenderedSnapshotAndResumeShowsNewest(t *testing.T) {
	model := New(10)
	model.Append(logAt("before", "info", 1))
	model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	model.Append(logAt("after", "info", 2))
	if strings.Contains(model.View(), "after") || model.Unread() != 1 {
		t.Fatalf("paused view=%s unread=%d", model.View(), model.Unread())
	}
	model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !strings.Contains(model.View(), "after") || model.Unread() != 0 {
		t.Fatalf("resumed view=%s unread=%d", model.View(), model.Unread())
	}
}

func TestModel_LevelSearchAndWrapControls(t *testing.T) {
	model := New(10)
	model.SetSize(38, 12)
	model.Append(logAt("short", "info", 1))
	model.Append(logAt("a very long matching message that must wrap", "error", 2))
	model.SetFilter("error", "matching")
	visible := model.visibleEntries()
	if len(visible) != 1 || visible[0].Log.Level != "error" {
		t.Fatalf("visible=%#v", visible)
	}
	model.wrap = true
	view := model.View()
	if !strings.Contains(view, "matching") || !strings.Contains(view, "\n") || strings.Contains(view, "short") {
		t.Fatalf("view=%s", view)
	}
}

func TestView_ControlStripHighlightsActiveWhenContentFocused(t *testing.T) {
	model := New(10)
	model.SetSize(100, 16)
	model.FocusFirst()
	model.controlIndex = 1 // Wrap

	model.SetContentFocused(false)
	if control := strings.Split(model.View(), "\n")[0]; strings.Contains(control, "\x1b[") {
		t.Fatalf("control strip should stay plain while rail owns focus: %q", control)
	}

	model.SetContentFocused(true)
	control := strings.Split(model.View(), "\n")[0]
	if !strings.Contains(control, "\x1b[") {
		t.Fatalf("active control chip should highlight when content focused: %q", control)
	}
}

func TestLogs_SearchNotInControlStrip(t *testing.T) {
	model := New(10)
	model.SetSize(100, 16)
	model.Append(logAt("daemon started", "info", 1))
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected strip + search + header, got %d lines: %s", len(lines), view)
	}
	control := lines[0]
	if strings.Contains(control, "Search") || strings.Contains(control, "/ ") {
		t.Fatalf("control strip must not embed search: %q", control)
	}
	for _, want := range []string{"Level", "Wrap", "Pause"} {
		if !strings.Contains(control, want) {
			t.Fatalf("control missing %q: %q", want, control)
		}
	}
	if !strings.Contains(lines[1], ui.SearchPlaceholder) && !strings.Contains(lines[1], "/ ") {
		t.Fatalf("search bar missing: %q", lines[1])
	}
	if !strings.Contains(lines[2], ui.TimeLabel) || !strings.Contains(lines[2], ui.MessageLabel) {
		t.Fatalf("table header missing: %q", lines[2])
	}
}

func TestLogs_SearchMatchesVisibleColumns(t *testing.T) {
	model := New(10)
	model.Append(logAt("upstream reset", "error", 2))
	model.Append(logAt("daemon started", "info", 1))
	model.query = "error"
	visible := model.visibleEntries()
	if len(visible) != 1 || visible[0].Log.Message != "upstream reset" {
		t.Fatalf("level column match failed: %#v", visible)
	}
	model.query = "daemon"
	visible = model.visibleEntries()
	if len(visible) != 1 || visible[0].Log.Message != "daemon started" {
		t.Fatalf("message column match failed: %#v", visible)
	}
	// Time is formatted via Local(); pin UTC for a stable HH:MM:SS substring.
	orig := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = orig })
	model.query = "00:00:01"
	visible = model.visibleEntries()
	if len(visible) != 1 || visible[0].Log.Message != "daemon started" {
		t.Fatalf("time column match failed: %#v", visible)
	}
}

func TestView_FocusedRowHighlightOnlyWhenContentFocused(t *testing.T) {
	model := New(10)
	model.SetSize(100, 12)
	model.Append(logAt("alpha-line", "info", 1))
	model.Append(logAt("beta-line", "warn", 2))
	model.focus = focusRow
	model.focused = 1

	model.SetContentFocused(false)
	for _, line := range strings.Split(model.View(), "\n") {
		if strings.Contains(line, "beta-line") {
			if !strings.Contains(line, ui.FocusMarker) {
				t.Fatalf("row marker missing while rail-focused: %q", line)
			}
			if strings.Contains(line, "\x1b[") {
				t.Fatalf("row should not use accent while rail owns focus: %q", line)
			}
		}
	}

	model.SetContentFocused(true)
	var focused string
	for _, line := range strings.Split(model.View(), "\n") {
		if strings.Contains(line, "beta-line") {
			focused = line
		}
	}
	if focused == "" || !strings.Contains(focused, ui.FocusMarker) || !strings.Contains(focused, "\x1b[") {
		t.Fatalf("focused content row missing highlight: %q", focused)
	}
}

func TestModel_FooterHintsAreContextual(t *testing.T) {
	model := New(10)
	if hints := model.FooterHints(); !strings.Contains(hints, "/ search") {
		t.Fatalf("default=%q", hints)
	}
	model.searching = true
	if hints := model.FooterHints(); hints != ui.FooterSearchMode {
		t.Fatalf("search=%q", hints)
	}
	model.searching = false
	model.detail = &detailState{entry: logAt("detail", "info", 1)}
	if hints := model.FooterHints(); hints != ui.FooterDetailMode {
		t.Fatalf("detail=%q", hints)
	}
}

func TestModel_SearchSupportsPasteMsg(t *testing.T) {
	model := New(10)
	model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !model.searching {
		t.Fatal("expected search mode after /")
	}
	model.Update(tea.PasteMsg{Content: "match\nme"})
	if model.query != "matchme" {
		t.Fatalf("query=%q", model.query)
	}
	updated, command := model.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("expected clipboard read command")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	// Clipboard content is environment-dependent; ensure the command path stays live.
	updated, leave := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(*Model)
	if model.searching {
		t.Fatal("esc should leave search")
	}
	if leave == nil {
		t.Fatal("expected input mode restore command")
	}
	mode, ok := leave().(ui.InputModeMsg)
	if !ok || mode.Mode != ui.InputNavigation {
		t.Fatalf("mode=%#v", mode)
	}
}

func TestModel_EnterOpensTypedDetailAndEscCloses(t *testing.T) {
	model := New(10)
	model.SetSize(100, 20)
	model.Append(Entry{ObservedAt: time.Unix(1, 0), Log: protocol.LogEntry{Level: "warn", Message: "first\nsecond\x1b[31m"}})
	model.focus = focusRow
	model.focused = 0
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := model.View()
	for _, want := range []string{"Log details", "warn", "first", "second", `"observed_at"`, `"payload"`} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %s", want, view)
		}
	}
	if strings.Contains(view, "\x1b[31m") {
		t.Fatalf("view retained log-provided terminal escape: %q", view)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.detail != nil {
		t.Fatal("detail remained open")
	}
}

func logAt(message, level string, second int64) Entry {
	return Entry{ObservedAt: time.Unix(second, 0), Log: protocol.LogEntry{Level: level, Message: message}}
}
