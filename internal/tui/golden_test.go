package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

// updateGolden regenerates the rendered fixtures. Default test runs only ever
// compare, never write, so a missing or changed golden fails loudly.
var updateGolden = flag.Bool("update", false, "regenerate golden render fixtures")

// ansiPattern strips CSI, OSC, and other VT100 escape sequences so fixtures pin
// layout and copy instead of the ambient terminal color profile.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]|\x1b\\][^\x07]*(\x07|\x1b\\\\)|\x1b[@-Z\\-_]")

func normalizeRender(view string) string {
	plain := ansiPattern.ReplaceAllString(view, "")
	return strings.ReplaceAll(plain, "\r\n", "\n")
}

// freezeUTC pins time.Local so any .Local() formatting in the rendered pages is
// deterministic across machines. Cleanup restores the original zone.
func freezeUTC(t *testing.T) {
	t.Helper()
	orig := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = orig })
}

func goldenModel(t *testing.T, page ui.PageID, width, height int) Model {
	t.Helper()
	model := NewModel()
	model.width, model.height = width, height
	model.active = page
	model.focus = ui.Focus{Area: ui.FocusContent, Page: page}
	for index, id := range model.rail {
		if id == page {
			model.railIndex = index
		}
	}
	model.resizePages()
	return model
}

func assertGolden(t *testing.T, name string, model Model) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	got := normalizeRender(model.View().Content)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s: %v\nrun: go test -run %s -update", path, err, t.Name())
	}
	if string(want) != got {
		t.Fatalf("golden %s changed (run -update if intentional)\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func fullCapabilities() []string {
	return []string{
		protocol.CapabilityCore, protocol.CapabilityProxies, protocol.CapabilityConnections,
		protocol.CapabilityRules, protocol.CapabilityLogs, protocol.CapabilitySubscriptions,
		protocol.CapabilityRuleProviders, protocol.CapabilityGeoIP, protocol.CapabilityPreferences,
		protocol.CapabilityOnboarding,
	}
}

func TestGoldenOverviewFull(t *testing.T) {
	freezeUTC(t)
	model := goldenModel(t, ui.PageOverview, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: fullCapabilities(),
	}})
	model.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{
		Schema: "mihari/v1", Status: "running", Version: "v1.19.0", PID: 4242,
	}})
	model.applySessionEvent(session.Event{Kind: session.EventSubscriptions, Subscriptions: protocol.SubscriptionList{
		Revision: 1,
		Subscriptions: []protocol.Subscription{
			{ID: "main", Name: "Main", Enabled: true, Cached: true},
		},
	}})
	assertGolden(t, "full/overview", model)
}

func TestGoldenProxiesFull(t *testing.T) {
	freezeUTC(t)
	model := goldenModel(t, ui.PageProxies, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityProxies},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{
		Schema: "mihari/v1",
		Groups: []protocol.ProxyGroup{
			{Name: "GLOBAL", Type: "Selector", Now: "node-a", All: []string{"node-a", "node-b"},
				Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "Vless", UDP: true}, {Name: "node-b", Type: "Vmess"}}},
			{Name: "Auto", Type: "URLTest", Now: "node-a", All: []string{"node-a", "node-b"},
				Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "Vless"}, {Name: "node-b", Type: "Vmess"}}},
		},
	}})
	model.pages[ui.PageProxies].FocusFirst()
	assertGolden(t, "full/proxies", model)
}

func TestGoldenConnectionsDetailFull(t *testing.T) {
	freezeUTC(t)
	model := goldenModel(t, ui.PageConnections, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityConnections},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventPreferences, Preferences: protocol.TUIPreferences{
		Revision: 1, ConnectionsColumns: []string{"host", "network", "chain", "traffic"},
	}})
	start := time.Unix(1700000000, 0).UTC()
	model.applySessionEvent(session.Event{Kind: session.EventConnections, ObservedAt: start, Connections: protocol.ConnectionList{
		UploadTotal: 2048, DownloadTotal: 8192,
		Connections: []protocol.Connection{{
			ID: "conn-1", Upload: 1024, Download: 4096,
			Chains: []string{"DIRECT"}, Rule: "DOMAIN-SUFFIX",
			Metadata: protocol.ConnectionMetadata{Network: "TCP", Host: "example.com",
				SourceIP: "127.0.0.1", DestinationIP: "93.184.216.34", Process: "curl"},
		}},
	}})
	page := model.pages[ui.PageConnections]
	page.FocusFirst()
	page = updatePage(page, tea.KeyPressMsg{Code: tea.KeyDown})
	page = updatePage(page, tea.KeyPressMsg{Code: tea.KeyEnter})
	model.pages[ui.PageConnections] = page
	assertGolden(t, "full/connections-detail", model)
}

func TestGoldenLogsCompact(t *testing.T) {
	freezeUTC(t)
	model := goldenModel(t, ui.PageLogs, 72, 22)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityLogs},
	}})
	base := time.Unix(1700000000, 0).UTC()
	entries := []protocol.LogEntry{
		{Level: "info", Message: "daemon started"},
		{Level: "warning", Message: "geoip database missing"},
		{Level: "error", Message: "upstream connection reset"},
	}
	for index, entry := range entries {
		model.applySessionEvent(session.Event{Kind: session.EventLog, ObservedAt: base.Add(time.Duration(index) * time.Second), Log: entry})
	}
	assertGolden(t, "compact/logs", model)
}

func TestGoldenWebGUIUnavailable(t *testing.T) {
	freezeUTC(t)
	model := goldenModel(t, ui.PageWebGUI, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityCore},
	}})
	assertGolden(t, "states/unavailable-web-gui", model)
}

func TestGoldenStaleState(t *testing.T) {
	freezeUTC(t)
	model := goldenModel(t, ui.PageOverview, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityCore},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{
		Schema: "mihari/v1", Status: "running", Version: "v1.19.0",
	}})
	model.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	assertGolden(t, "states/stale", model)
}

func updatePage(page ui.Page, msg tea.Msg) ui.Page {
	updated, _ := page.Update(msg)
	return updated
}
