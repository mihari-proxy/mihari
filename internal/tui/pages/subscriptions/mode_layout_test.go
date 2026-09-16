package subscriptions

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSubscriptionMode_FullLabelOrHiddenColumn(t *testing.T) {
	for _, width := range []int{58, 84, 120, 200} {
		m := New(nil, nil, nil)
		m.SetSize(width, 22)
		m.subscriptions = []protocol.Subscription{{ID: "a", Name: "fixture", ProxyMode: "auto"}}
		cols, widths := m.subscriptionWidths()
		found := false
		for i, col := range cols {
			if col.ID == "proxy" {
				found = true
				if widths[i] < 26 {
					t.Errorf("Mode truncated at page width %d", width)
				}
			}
		}
		if found != (width != 58) {
			t.Errorf("unexpected Mode visibility at page width %d", width)
		}
		for _, line := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("page overflow: width=%d line=%q", width, ansi.Strip(line))
			}
		}
		if found && !strings.Contains(ansi.Strip(m.View()), "PROXY w Fallback to DIRECT") {
			t.Error("full label missing")
		}
	}
}

func TestSubscriptionMode_FormsWrapFullValueAndKeepRawMode(t *testing.T) {
	for _, form := range []*formModel{newAddForm(), newEditForm(protocol.Subscription{})} {
		index := len(form.inputs) - 1
		form.inputs[index].SetValue("auto")
		form.index = index
		for _, width := range []int{44, 70} {
			layout := form.fieldLayout(ui.DefaultTheme(), width)
			text := ansi.Strip(strings.Join(layout.lines, "\n"))
			if !strings.Contains(text, "PROXY w Fallback to DIRECT") {
				t.Error("full value missing")
			}
			if !strings.Contains(strings.Join(strings.Fields(text), " "), "eligible network errors") {
				t.Error("Mode explanation missing")
			}
			for _, line := range layout.lines {
				if lipgloss.Width(line) > width {
					t.Errorf("form overflow: width=%d line=%q", width, ansi.Strip(line))
				}
			}
			field := layout.fields[index]
			if width == 44 && (field.last <= field.first || !strings.Contains(ansi.Strip(layout.lines[field.first]), "Mode") || !strings.Contains(ansi.Strip(layout.lines[field.first+1]), "PROXY w Fallback to DIRECT")) {
				t.Error("narrow Mode must split label and value")
			}
		}
		if form.kind == formAdd {
			if form.addRequest("op", 1).ProxyMode != "auto" {
				t.Error("raw add mode changed")
			}
		} else {
			request := form.updateRequest("op", 1)
			if request.ProxyMode == nil || *request.ProxyMode != "auto" {
				t.Error("raw edit mode changed")
			}
		}
	}
}
