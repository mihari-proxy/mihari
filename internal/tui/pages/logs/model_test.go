package logs

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
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

func TestModel_SearchSupportsPasteMsg(t *testing.T) {
	model := New(10)
	model.searching = true
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
	_ = model
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
