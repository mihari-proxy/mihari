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
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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

func TestGoldenRoutingMode(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"compact", 72, 22}, {"full", 100, 28}} {
		t.Run(size.name, func(t *testing.T) {
			model := goldenModel(t, ui.PageProxies, size.width, size.height)
			model.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 1, Status: protocol.Status{Health: "ok", Revision: 3, Capabilities: []string{protocol.CapabilityProxies, protocol.CapabilityRouting}}})
			model.applySessionEvent(session.Event{Kind: session.EventRouting, Epoch: 1, Routing: protocol.RoutingStatus{Revision: 3, DesiredMode: "rule", LiveMode: "rule", State: "applied", GlobalSelection: "DIRECT", LiveGlobalSelection: "DIRECT"}})
			revision := uint64(3)
			model.applySessionEvent(session.Event{Kind: session.EventProxies, Epoch: 1, Proxies: protocol.ProxyGroups{Revision: &revision, Groups: []protocol.ProxyGroup{{Name: "GLOBAL", Type: "Selector", Now: "DIRECT", All: []string{"DIRECT", "Tokyo", "Singapore"}, Nodes: []protocol.ProxyNode{{Name: "DIRECT", Type: "Direct"}, {Name: "Tokyo", Type: "VLESS"}, {Name: "Singapore", Type: "Trojan"}}}}}})
			page := model.pages[ui.PageProxies].(*proxypage.Model)
			page.FocusFirst()
			assertGoldenContent(t, "routing_header_"+size.name, trimRenderPadding(normalizeRender(model.View().Content)))
			page.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			page.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			assertGoldenContent(t, "routing_picker_"+size.name, trimRenderPadding(normalizeRender(model.View().Content)))
			next, _ := model.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
			if next.(Model).active != ui.PageProxies || page.HelpMode() != ui.ModeRouting {
				t.Fatal("digit escaped routing modal")
			}
			next, _ = model.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
			if next.(Model).modal == nil {
				t.Fatal("routing help did not open")
			}
		})
	}
}

// TestMain fixes the test process timezone before tests start timers or workers.
// Changing time.Local between golden cases races with time.Now in those workers.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
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
	assertGoldenContent(t, name, normalizeRender(model.View().Content))
}

func assertGoldenContent(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
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

func trimRenderPadding(view string) string {
	lines := strings.Split(view, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.Join(lines, "\n")
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
	model := goldenModel(t, ui.PageOverview, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: fullCapabilities(),
	}})
	model.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{
		Schema: "mihari/v1", Status: "running", Version: "v1.19.0", PID: 4242,
	}})
	model.applySessionEvent(session.Event{Kind: session.EventSubscriptions, Subscriptions: protocol.SubscriptionList{
		Revision: 1, ActiveID: "main",
		Subscriptions: []protocol.Subscription{
			{ID: "main", Name: "Main", Enabled: true, Cached: true, UpdatedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)},
		},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: fullCapabilities(),
		Config: &protocol.ConfigStatus{Status: "ok", DesiredRevision: 1, ObservedRevision: 1},
	}})
	assertGolden(t, "full/overview", model)
}

func TestGoldenProxiesFull(t *testing.T) {
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

// TestGoldenConnectionsDetailFull pins the complete wide connection detail.
func TestGoldenConnectionsDetailFull(t *testing.T) {
	goldenConnectionDetail(t, "full/connections-detail", 110, 40, false, false, false, nil)
}

// TestGoldenConnectionsDetailVariants pins compact and retained-observation states.
func TestGoldenConnectionsDetailVariants(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		width, height          int
		closed, paused, bottom bool
	}{
		{"compact", 72, 22, false, false, false},
		{"closed", 110, 40, true, false, false},
		{"paused", 100, 28, false, true, false},
		{"bottom", 72, 22, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			goldenConnectionDetail(t, "full/connections-detail-"+tc.name, tc.width, tc.height, tc.closed, tc.paused, tc.bottom, nil)
		})
	}
}

// goldenConnectionDetail enters the real detail through page navigation before capture.
func goldenConnectionDetail(t *testing.T, name string, width, height int, closed, paused, bottom bool, connection *protocol.Connection) {
	t.Helper()
	model := goldenModel(t, ui.PageConnections, width, height)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityConnections},
	}})
	start := time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC)
	if connection == nil {
		connection = &protocol.Connection{
			ID: "8d37b6a2-51a4-4f9e-b1d9-6e84b12fa205", Start: start,
			Upload: 2048, Download: 4096, UploadSpeed: 1024, DownloadSpeed: 3072,
			Chains: []string{"Japan 01", "Auto Select", "Proxy"}, Rule: "DomainSuffix", RulePay: "example.test",
			Metadata: protocol.ConnectionMetadata{Network: "TCP", Type: "HTTP", Host: "api.example.test",
				SourceIP: "192.168.1.12", SourcePort: "52341", DestinationIP: "203.0.113.24", DestinationPort: "443",
				Process: "chrome.exe", ProcessPath: "C:/Apps/Browser/chrome.exe", InboundName: "mixed-in"},
		}
	}
	model.applySessionEvent(session.Event{Kind: session.EventConnections, ObservedAt: start, Connections: protocol.ConnectionList{
		Connections: []protocol.Connection{*connection},
	}})
	page := model.pages[ui.PageConnections]
	page.FocusFirst()
	if closed {
		model.applySessionEvent(session.Event{Kind: session.EventConnections, ObservedAt: start.Add(time.Minute), Connections: protocol.ConnectionList{}})
		page = updatePage(page, tea.KeyPressMsg{Code: tea.KeyEnter}) // Active -> Closed dataset.
	}
	if paused {
		page = updatePage(page, tea.KeyPressMsg{Code: 'p', Text: "p"})
	}
	// Control -> search -> header -> first row, then open its detail.
	for range 3 {
		page = updatePage(page, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	page = updatePage(page, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(normalizeRender(page.View()), ui.ConnectionDetailsTitle) {
		t.Fatal("golden must enter connection details before capturing the view")
	}
	if bottom {
		for range 100 {
			page = updatePage(page, tea.KeyPressMsg{Code: tea.KeyDown})
		}
	}
	model.pages[ui.PageConnections] = page
	beforeDiagnostics := model.View().Content
	next, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	model = next.(Model)
	if !model.diagnosticWindow.open {
		t.Fatal("connection detail intercepted global F2")
	}
	next, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = next.(Model)
	if model.View().Content != beforeDiagnostics {
		t.Fatal("closing diagnostics changed the connection detail or scroll position")
	}
	rendered := model.View().Content
	if lipgloss.Width(rendered) > width || lipgloss.Height(rendered) > height {
		t.Fatalf("shell exceeds %dx%d: %dx%d", width, height, lipgloss.Width(rendered), lipgloss.Height(rendered))
	}
	view := trimRenderPadding(normalizeRender(rendered))
	if !strings.Contains(view, ui.ConnectionDetailsTitle) {
		t.Fatalf("shell did not render the detail:\n%s", view)
	}
	assertGoldenContent(t, name, view)
}

func TestGoldenConnectionsRouteStates(t *testing.T) {
	for _, outbound := range []string{"DIRECT", "REJECT"} {
		t.Run(outbound, func(t *testing.T) {
			c := protocol.Connection{
				ID: "route-state", Start: time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC),
				Rule: "DomainSuffix", RulePay: "example.test", Chains: []string{outbound, "Local"},
				Metadata: protocol.ConnectionMetadata{
					Host: "api.example.test", DestinationIP: "203.0.113.24", DestinationPort: "443",
					Type: "Tun", Network: "tcp", InboundName: "DEFAULT-TUN", Process: "browser.exe",
					SourceIP: "198.18.0.1", SourcePort: "52341",
				},
			}
			if outbound == "DIRECT" {
				c.Metadata.RemoteDestination = c.Metadata.DestinationIP
			}
			goldenConnectionDetail(t, "full/connections-detail-"+strings.ToLower(outbound), 110, 40, true, false, false, &c)
		})
	}
}

func TestGoldenLogsCompact(t *testing.T) {
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

func TestGoldenSystemLoggingFull(t *testing.T) {
	t.Setenv("MIHARI_DATA", t.TempDir())
	model := goldenModel(t, ui.PageSystem, 100, 40)
	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 1, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 7, Health: "ok", DaemonVersion: "v0.9.0",
		Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityOnboarding, protocol.CapabilityLogging},
	}})
	model.applySessionEvent(session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{
		Schema: "mihari/v1", Revision: 7, Status: "running", Version: "v1.19.12", Channel: "stable", PID: 4242,
	}})
	model.applySessionEvent(session.Event{Kind: session.EventLogging, Epoch: 1, Logging: protocol.LoggingStatus{
		Schema: "mihari/v1", Revision: 7, Level: "warn", MaxSizeMB: 25, MaxFiles: 6, Dir: `C:\Users\alice\.mihari\logs`,
	}})
	page := model.pages[ui.PageSystem].(*systempage.Model)
	page.SetOnboarding(protocol.OnboardingStatus{
		Revision: 7, MixedAddr: "127.0.0.1:7890", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191",
	})
	page.SetLocalLoggingAvailable(false)
	page.FocusFirst()
	view := normalizeRender(model.View().Content)
	assertGoldenContent(t, "full/system-logging", trimRenderPadding(view))
}

func TestGoldenWebGUIUnavailable(t *testing.T) {
	model := goldenModel(t, ui.PageWebGUI, 100, 28)
	model.applySessionEvent(session.Event{Kind: session.EventStatus, Status: protocol.Status{
		Schema: "mihari/v1", Revision: 1, Capabilities: []string{protocol.CapabilityCore},
	}})
	assertGolden(t, "states/unavailable-web-gui", model)
}

func TestGoldenStaleState(t *testing.T) {
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
