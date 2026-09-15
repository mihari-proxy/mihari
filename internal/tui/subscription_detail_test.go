package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
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

// TestSubscriptionDetail_ResizesWithSaveVisible checks every field against the root frame budget.
func TestSubscriptionDetail_ResizesWithSaveVisible(t *testing.T) {
	for _, add := range []bool{false, true} {
		t.Run(fmt.Sprintf("add=%v", add), func(t *testing.T) {
			m := subscriptionDetailRoot()
			if add {
				next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				m = next.(Model)
				next, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
				m = next.(Model)
			}
			count := 5
			if add {
				count = 3
			}
			for _, size := range [][2]int{{160, 42}, {72, 22}, {100, 30}, {160, 42}} {
				next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m = next.(Model)
				for i := 0; i <= count; i++ {
					view := ansi.Strip(m.View().Content)
					if strings.Count(view, "[ Save ]") != 1 {
						t.Fatalf("size=%v field=%d: Save missing", size, i)
					}
					lines := strings.Split(view, "\n")
					if len(lines) > size[1] {
						t.Fatalf("height overflow at %v", size)
					}
					for _, line := range lines {
						if ansi.StringWidth(line) > size[0] {
							t.Fatalf("width overflow at %v", size)
						}
					}
					next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
					m = next.(Model)
				}
			}
		})
	}
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

// TestSubscriptionDetail_FooterFollowsFocusOnce prevents duplicated or stale root shortcuts.
func TestSubscriptionDetail_FooterFollowsFocusOnce(t *testing.T) {
	for _, add := range []bool{false, true} {
		m := subscriptionDetailRoot()
		if add {
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m = next.(Model)
			next, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
			m = next.(Model)
		}
		count := 5
		if add {
			count = 3
		}
		for i := 0; i <= count; i++ {
			view := ansi.Strip(m.View().Content)
			want := "Enter next"
			if i == count {
				want = "Enter save"
			}
			if strings.Count(view, want) != 1 || strings.Contains(view, "next/save") || strings.Count(view, "Esc cancel") != 1 {
				t.Fatalf("add=%v field=%d: incorrect or repeated footer\n%s", add, i, view)
			}
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			m = next.(Model)
		}
	}
}
