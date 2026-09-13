package setup

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func requireViewText(t *testing.T, view string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(view, text) {
			t.Fatalf("missing %q in view:\n%s", text, view)
		}
	}
}

func TestSetupCoreStatus_SeparatesLocalReadinessAndRuntime(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step, m.coreLocalLoaded = stepCore, true
	m.coreLocal = protocol.CoreStatus{LocalReady: true, LocalVersion: "v1.19.0", Channel: "stable", Status: "stopped"}
	requireViewText(t, m.View(), "Current status", "Local core", "Ready", "Version", "v1.19.0", "Channel", "stable", "Runtime", "stopped", "Next")
	m.coreLocalLoaded = false
	requireViewText(t, m.View(), "Not confirmed", "Enter recheck")
}

func TestSetupGeoIPStatus_ShowsEachDatabaseAndTimestamp(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step, m.geoipLocalLoaded = stepGeoIP, true
	m.geoipLocal = protocol.GeoIPStatus{Country: protocol.GeoIPDatabaseStatus{Available: true, UpdatedAt: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)}}
	requireViewText(t, m.View(), "Current status", "Country", "Ready", "2026-09-12", "ASN", "Unavailable", "Next")
	m.geoipLocal.ASN = protocol.GeoIPDatabaseStatus{Available: true, Error: "https://example.test/?token=secret"}
	requireViewText(t, m.View(), "Ready · update failed")
	if strings.Contains(m.View(), "token=secret") {
		t.Fatal("database status exposed raw failure details")
	}
}

func TestSetupGeoIPStatus_FailedReadClearsStaleReadyState(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step, m.geoipLocalLoaded = stepGeoIP, true
	m.geoipLocal = protocol.GeoIPStatus{Country: protocol.GeoIPDatabaseStatus{Available: true}, ASN: protocol.GeoIPDatabaseStatus{Available: true}}
	m.Update(geoipLocalResultMsg{gen: m.geoipLocalGen, err: context.DeadlineExceeded})
	requireViewText(t, m.View(), "Not confirmed")
	if m.geoipLocalLoaded {
		t.Fatal("failed read retained stale ready status")
	}
}

func TestSetupSubscriptionOverview_CountsStatesAndPreservesInputs(t *testing.T) {
	f := &subscriptionClient{fakeClient: &fakeClient{status: defaultStatus(false)}, profiles: []protocol.Subscription{
		{ID: "one", Name: "Primary", Enabled: true, Cached: true},
		{ID: "two", Name: "Backup", Cached: true, LastError: "download failed"},
		{ID: "three", Name: "Pending", Enabled: true},
	}}
	m := New(f, nil)
	msg := m.Load()().(onboardingResultMsg)
	msg.subscriptions.ActiveID = "one"
	m.Update(msg)
	m.Update(actionResultMsg{next: stepSubscription})
	m.subscriptionInputs[0].SetValue("unsaved name")
	requireViewText(t, m.View(), "3 subscriptions", "Primary", "Current", "Backup", "Disabled", "Cached · refresh failed", "Pending", "Not downloaded", "To add more, open the Subscriptions page.")
	if strings.Contains(m.View(), "URL") || strings.Contains(m.FooterHints(), "Tab fields") {
		t.Fatal("overview still presents the add form")
	}
	m.Update(tea.PasteMsg{Content: "unwanted paste"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.subscriptionInputs[0].Value() != "unsaved name" {
		t.Fatal("overview forwarded input to a hidden form")
	}
}

func TestSetupSubscriptionOverview_ReturnAfterAddIncludesSavedItem(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepSubscription
	m.Update(actionResultMsg{subscription: &protocol.Subscription{ID: "new", Name: "New profile", Cached: true}, next: stepGeoIP})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	requireViewText(t, m.View(), "1 subscription", "New profile", "To add more, open the Subscriptions page.")
}

func TestSetupStatus_BackRefreshesResourceState(t *testing.T) {
	for _, current := range []step{stepSubscription, stepReview} {
		f := &fakeClient{status: defaultStatus(false)}
		m := loadedModel(f)
		m.step = current
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if cmd == nil {
			t.Fatalf("back from %d did not recheck resource status", current)
		}
		m.Update(cmd())
		if current == stepSubscription && f.coreCalls != 1 {
			t.Fatal("core state was not refreshed")
		}
		if current == stepReview && f.geoIPStatusCalls != 1 {
			t.Fatal("GeoIP state was not refreshed")
		}
	}
}

func TestSetupStatusViews_CompactLayoutAndScrollReset(t *testing.T) {
	f := &subscriptionClient{fakeClient: &fakeClient{status: defaultStatus(false)}, profiles: []protocol.Subscription{
		{ID: "one", Name: "Primary", Enabled: true, Cached: true},
		{ID: "two", Name: strings.Repeat("Very long name ", 20), LastError: "download failed"},
		{ID: "three", Name: "Pending"},
	}}
	m := New(f, nil)
	m.Update(m.Load()())
	m.SetSize(70, 20)
	m.coreLocal = protocol.CoreStatus{LocalReady: true, LocalVersion: "v1.19.0", Status: "running", Channel: "stable"}
	m.coreLocalLoaded, m.geoipLocalLoaded = true, true
	m.geoipLocal.Country.Available = true
	for _, current := range []step{stepCore, stepGeoIP, stepSubscription} {
		m.step = current
		view := m.View()
		if lipgloss.Width(view) > 70 || lipgloss.Height(view) > 20 {
			t.Fatalf("step %d overflows compact layout", current)
		}
		t.Logf("Step %d:\n%s", current, view)
	}
	requireViewText(t, m.View(), "PgDn/PgUp scroll", "To add more")
	m.scroll = 5
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.step != stepGeoIP || m.scroll != 0 {
		t.Fatal("subscription scroll offset leaked into the next step")
	}
}
