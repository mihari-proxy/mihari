package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

// subscriptionDetailRoot opens the editable overlay at the minimum terminal size.
func subscriptionDetailRoot() Model {
	m := NewModel()
	m.width, m.height = 72, 22
	m.active = ui.PageSubscriptions
	m.focus.Area = ui.FocusContent
	m.resizePages()
	p := m.pages[ui.PageSubscriptions].(*subscriptionspage.Model)
	p.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main", Enabled: true}}})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	return m
}

func TestSubscriptionDetail_TextKeysStayInForm(t *testing.T) {
	m := subscriptionDetailRoot()
	// Keys must remain page-owned immediately, even before an async InputModeMsg.
	next, _ := m.Update(tea.KeyPressMsg{Code: '6', Text: "6"})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = next.(Model)
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("form q quit app")
		}
	}
	if m.active != ui.PageSubscriptions {
		t.Fatal("text digit changed pages")
	}
}

func TestSubscriptionDetail_MinimumRootFrame(t *testing.T) {
	m := subscriptionDetailRoot()
	for i := 0; i < 6; i++ {
		view := ansi.Strip(m.View().Content)
		if len(strings.Split(view, "\n")) > 22 {
			t.Fatalf("root height exceeded: %d", len(strings.Split(view, "\n")))
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > 72 {
				t.Fatal("root width exceeded")
			}
		}
		if i == 5 && !strings.Contains(view, "Save") {
			t.Fatal("Save is clipped at minimum root size")
		}
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = next.(Model)
	}
}
