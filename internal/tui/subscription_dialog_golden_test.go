package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// subscriptionDialogFixture drives the root with synthetic events and a fixed clock.
func subscriptionDialogFixture(t *testing.T, width, height int, add bool, field int, lastError string) Model {
	t.Helper()
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	m := goldenModel(t, ui.PageSubscriptions, width, height)
	m.pages[ui.PageSubscriptions] = subscriptionspage.New(nil, nil, func() time.Time { return now })
	m.resizePages()
	m.applySessionEvent(session.Event{Kind: session.EventConnected})
	m.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 1, Status: protocol.Status{Health: "ok", Revision: 1, Capabilities: fullCapabilities()}})
	m.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{Status: "running", Version: "v1.19.12"}})
	m.applySessionEvent(session.Event{Kind: session.EventSubscriptions, Subscriptions: protocol.SubscriptionList{
		ActiveID: "example", GlobalInterval: "12h", Subscriptions: []protocol.Subscription{{ID: "example", Name: "Example", Enabled: true, Cached: true, AutoRefresh: true, ProxyMode: "proxy", UpdatedAt: now.Add(-7 * time.Hour), Total: 80 << 30, Download: 52 << 30, Upload: 1 << 29, LastError: lastError}},
	}})
	m.pages[ui.PageSubscriptions].FocusFirst()
	press := func(msg tea.Msg) { next, _ := m.Update(msg); m = next.(Model) }
	if add {
		press(tea.KeyPressMsg{Code: 'a', Text: "a"})
		press(tea.PasteMsg{Content: "Example"})
	} else {
		press(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	press(tea.KeyPressMsg{Code: tea.KeyTab})
	press(tea.PasteMsg{Content: "https://example.test/subscription/" + strings.Repeat("sample-", 8)})
	press(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	for i := 0; i < field; i++ {
		press(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	return m
}

// TestGoldenSubscriptionDialogs captures shared add/edit, focus, overflow, and error layouts.
func TestGoldenSubscriptionDialogs(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		width, height, field int
		add                  bool
		lastError            string
	}{
		{"detail", 100, 30, 0, false, ""},
		{"cycle", 100, 30, 4, false, ""},
		{"interval", 100, 30, 2, false, ""},
		{"enabled-action", 100, 36, 5, false, ""},
		{"inuse-action", 100, 36, 6, false, ""},
		{"add", 100, 30, 0, true, ""},
		{"compact-url", 72, 22, 1, false, ""},
		{"compact-save", 72, 22, 7, false, ""},
		{"error", 100, 30, 0, false, "Download failed. The cached configuration remains available."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := subscriptionDialogFixture(t, tc.width, tc.height, tc.add, tc.field, tc.lastError)
			assertGoldenContent(t, "subscriptions/"+tc.name, trimRenderPadding(normalizeRender(m.View().Content)))
		})
	}
}
