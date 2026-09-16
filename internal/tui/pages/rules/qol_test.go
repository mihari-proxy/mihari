package rules

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestRuleDetail_CenteredOverlayKeepsTableAndColors(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 28)
	m.SetRules(protocol.RuleList{Rules: []protocol.Rule{{Type: "RuleSet", Payload: "Lan", Proxy: "DIRECT"}}})
	m.focus = pageFocus{kind: focusRow, row: 0}
	before := m.View()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View()
	if lipgloss.Height(view) > 28 || lipgloss.Width(view) > 100 {
		t.Fatalf("dialog exceeds viewport: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
	for _, s := range []string{"Rule details", "Payload", "Lan", "Evaluation order", "DIRECT", "Controls"} {
		if !strings.Contains(ansi.Strip(view), s) {
			t.Fatalf("missing %s: %s", s, view)
		}
	}
	titleLine := ""
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "Rule details") {
			titleLine = line
		}
	}
	if strings.Index(titleLine, "Rule details") < 10 {
		t.Fatalf("dialog not centered: %q", titleLine)
	}
	for _, code := range []string{"38;5;75", "38;5;245"} {
		if !strings.Contains(view, code) {
			t.Fatalf("missing semantic/dim color %s", code)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.View() != before {
		t.Fatal("closing changed table state")
	}
}

func TestRuleDetail_LongPayloadScrollsInsideBounds(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(58, 20)
	m.SetRules(protocol.RuleList{Rules: []protocol.Rule{{Type: "AND", Payload: strings.Repeat("(DOMAIN-SUFFIX,example.test),", 70) + "PAYLOAD-END", Proxy: "Proxy"}}})
	m.focus = pageFocus{kind: focusRow, row: 0}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	first := m.View()
	if lipgloss.Height(first) > 20 || lipgloss.Width(first) > 58 {
		t.Fatal("long payload overflows")
	}
	for i := 0; i < 100; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	last := m.View()
	if first == last || !strings.Contains(ansi.Strip(last), "PAYLOAD-END") || !strings.Contains(ansi.Strip(last), "Enter/Esc close") {
		t.Fatalf("payload cannot scroll to end: %s", last)
	}
	if m.focus.row != 0 {
		t.Fatal("detail navigation moved table")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.detail != nil {
		t.Fatal("Enter did not close")
	}
}
