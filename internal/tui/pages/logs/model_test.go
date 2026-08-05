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
	railControl := logsSectionBodyLine(model.View(), 0)
	model.SetContentFocused(true)
	control := logsSectionBodyLine(model.View(), 0)
	if !strings.Contains(control, "\x1b[") {
		t.Fatalf("active control chip should highlight when content focused: %q", control)
	}
	if control == railControl {
		t.Fatal("content-focused control strip should differ from rail-focused")
	}
}

func TestLogs_SearchNotInControlStrip(t *testing.T) {
	model := New(10)
	model.SetSize(100, 16)
	model.Append(logAt("daemon started", "info", 1))
	view := model.View()
	control := logsSectionBodyLine(view, 0)
	if strings.Contains(control, "Search") {
		t.Fatalf("control strip must not embed search: %q", control)
	}
	for _, want := range []string{"Level", "Wrap", "Pause"} {
		if !strings.Contains(control, want) {
			t.Fatalf("control missing %q: %q", want, control)
		}
	}
	search := logsSectionBodyLine(view, 1)
	if !strings.Contains(search, ui.SearchPlaceholder) && !strings.Contains(search, "/ ") {
		t.Fatalf("search bar missing: %q", search)
	}
	if !strings.Contains(view, ui.TimeLabel) || !strings.Contains(view, ui.MessageLabel) {
		t.Fatalf("table header missing in dual-section view:\n%s", view)
	}
	if !strings.Contains(view, ui.ControlsSectionTitle) || !strings.Contains(view, ui.LogsSectionTitle) {
		t.Fatalf("section titles missing:\n%s", view)
	}
}

func logsSectionBodyLine(view string, n int) string {
	lines := strings.Split(view, "\n")
	body := 0
	for i := 1; i < len(lines); i++ {
		plain := logsStripANSI(lines[i])
		if strings.HasPrefix(strings.TrimLeft(plain, " "), "╰") {
			break
		}
		if body == n {
			return lines[i]
		}
		body++
	}
	return ""
}

func logsStripANSI(value string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range value {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
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

func TestView_LogLevelsUseSemanticColorsWhileRailFocused(t *testing.T) {
	model := New(10)
	model.SetSize(100, 16)
	model.Append(logAt("err-msg", "error", 1))
	model.Append(logAt("warn-msg", "warn", 2))
	model.focus = focusRow
	model.focused = 0

	// Data colors must show as soon as the page is visible, even before Enter into content.
	model.SetContentFocused(false)
	view := model.View()
	if !strings.Contains(view, "err-msg") || !strings.Contains(view, "warn-msg") {
		t.Fatalf("messages missing: %s", view)
	}
	errorStyled := model.theme.Danger.Render("ERROR")
	warnStyled := model.theme.Warning.Render("WARN")
	if !strings.Contains(view, errorStyled) {
		t.Fatalf("ERROR should use Danger while rail-focused: %s", view)
	}
	if !strings.Contains(view, warnStyled) {
		t.Fatalf("WARN should use Warning while rail-focused: %s", view)
	}
}

func TestView_FocusedRowHighlightOnlyWhenContentFocused(t *testing.T) {
	model := New(10)
	model.SetSize(100, 12)
	model.Append(logAt("alpha-line", "info", 1))
	model.Append(logAt("beta-line", "warn", 2))
	model.focus = focusRow
	model.focused = 1

	findBeta := func() string {
		for _, line := range strings.Split(model.View(), "\n") {
			if strings.Contains(line, "beta-line") {
				return line
			}
		}
		return ""
	}

	model.SetContentFocused(false)
	railLine := findBeta()
	if railLine == "" || !strings.Contains(railLine, ui.FocusMarker) {
		t.Fatalf("row marker missing while rail-focused: %q", railLine)
	}
	// Semantic level color is fine; reverse RowFocus chrome must wait for content focus.
	if strings.Contains(railLine, "\x1b[7m") {
		t.Fatalf("row should not use reverse focus chrome while rail owns focus: %q", railLine)
	}

	model.SetContentFocused(true)
	focused := findBeta()
	if focused == "" || !strings.Contains(focused, ui.FocusMarker) {
		t.Fatalf("focused content row missing marker: %q", focused)
	}
	if focused == railLine {
		t.Fatalf("content focus should add RowFocus styling: rail=%q content=%q", railLine, focused)
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

func TestLogs_SearchDirectTypeNoEnter(t *testing.T) {
	model := New(10)
	model.Append(Entry{ObservedAt: time.Unix(1, 0), Log: protocol.LogEntry{Level: "info", Message: "hello"}})
	model.FocusFirst()
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(*Model)
	if !model.searching || model.focus != focusSearch || command == nil {
		t.Fatalf("searching=%v focus=%v command=%v", model.searching, model.focus, command != nil)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	model = updated.(*Model)
	if model.query != "hi" {
		t.Fatalf("query=%q", model.query)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	model = updated.(*Model)
	if model.query != "hXi" {
		t.Fatalf("cursor insert query=%q", model.query)
	}
	// Page shortcuts disabled (p would pause).
	wasPaused := model.buffer.Paused()
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	model = updated.(*Model)
	if model.query != "hXpi" || model.buffer.Paused() != wasPaused {
		t.Fatalf("p should type: query=%q paused=%v", model.query, model.buffer.Paused())
	}
	updated, leave := model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	model = updated.(*Model)
	if model.searching || model.focus != focusControl {
		t.Fatalf("up leave searching=%v focus=%v", model.searching, model.focus)
	}
	if leave == nil {
		t.Fatal("expected input mode restore")
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
