package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type snapshotSessionClient struct{}

func (snapshotSessionClient) Status(context.Context) (protocol.Status, error) {
	return protocol.Status{Schema: "mihari/v1", PID: 41, StartedAt: time.Unix(100, 0), Health: "ok", Capabilities: []string{
		protocol.CapabilityCore, protocol.CapabilitySubscriptions, protocol.CapabilityProxies,
		protocol.CapabilityRules, protocol.CapabilityRuleProviders, protocol.CapabilityPreferences,
		protocol.CapabilityWebGUI, protocol.CapabilityLogging, protocol.CapabilityRouting,
		protocol.CapabilitySystemProxy, protocol.CapabilityTUN, protocol.CapabilityEgress, protocol.CapabilityOnboarding,
	}}, nil
}
func (snapshotSessionClient) Core(context.Context) (protocol.CoreStatus, error) {
	return protocol.CoreStatus{Schema: "mihari/v1", PID: 42, StartedAt: time.Unix(110, 0), Status: "running"}, nil
}
func (snapshotSessionClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return protocol.SubscriptionList{Schema: "mihari/v1", Subscriptions: []protocol.Subscription{{ID: "a", Name: "Fixture profile", Enabled: true, Cached: true}}, ActiveID: "a"}, nil
}
func (snapshotSessionClient) ProxyGroups(context.Context) (protocol.ProxyGroups, error) {
	return protocol.ProxyGroups{Schema: "mihari/v1", Groups: []protocol.ProxyGroup{{Name: "group", Nodes: []protocol.ProxyNode{{Name: "node"}}}}}, nil
}
func (snapshotSessionClient) Rules(context.Context) (protocol.RuleList, error) {
	return protocol.RuleList{Schema: "mihari/v1", Rules: []protocol.Rule{{Type: "MATCH", Proxy: "DIRECT"}}}, nil
}
func (snapshotSessionClient) RuleProviders(context.Context) (protocol.RuleProviderList, error) {
	return protocol.RuleProviderList{Schema: "mihari/v1", Providers: []protocol.RuleProvider{{Name: "Fixture provider"}}}, nil
}
func (snapshotSessionClient) TUIPreferences(context.Context) (protocol.TUIPreferences, error) {
	return protocol.TUIPreferences{Schema: "mihari/v1", ConnectionsColumns: []string{"host"}, Proxies: &protocol.ProxyPreferences{LatencyTestConcurrency: 5}}, nil
}
func (snapshotSessionClient) WebGUI(context.Context) (protocol.WebGUIStatus, error) {
	return protocol.WebGUIStatus{Schema: "mihari/v1", GatewayHealth: "ok", ActivePanel: "zashboard"}, nil
}
func (snapshotSessionClient) Logging(context.Context) (protocol.LoggingStatus, error) {
	return protocol.LoggingStatus{Schema: "mihari/v1", Level: "info", MaxSizeMB: 10, MaxFiles: 3}, nil
}
func (snapshotSessionClient) Routing(context.Context) (protocol.RoutingStatus, error) {
	return protocol.RoutingStatus{Schema: "mihari/v1", DesiredMode: "rule", LiveMode: "rule", State: "applied"}, nil
}
func (snapshotSessionClient) SystemProxy(context.Context) (protocol.SystemProxyStatus, error) {
	return protocol.SystemProxyStatus{Schema: "mihari/v1"}, nil
}
func (snapshotSessionClient) Tun(context.Context) (protocol.TunStatus, error) {
	return protocol.TunStatus{Schema: "mihari/v1"}, nil
}
func (snapshotSessionClient) Egress(context.Context) (protocol.EgressStatus, error) {
	return protocol.EgressStatus{Schema: "mihari/v1", Selection: protocol.EgressSelection{Mode: "automatic"}}, nil
}
func (snapshotSessionClient) Onboarding(context.Context) (protocol.OnboardingStatus, error) {
	return protocol.OnboardingStatus{Schema: "mihari/v1", Complete: true}, nil
}
func (snapshotSessionClient) Stream(ctx context.Context, _ string, _ func(protocol.StreamEvent) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestSnapshotSession_NavigationLoadsSharedConsumersWithoutEnteringContent(t *testing.T) {
	s := session.New(snapshotSessionClient{}, session.Options{})
	events := s.Start(t.Context())
	defer s.Close()
	m := NewModel()
	m.width, m.height = 140, 40
	m.setRefreshSession(s)
	consumeUntilReady := func(kinds []session.EventKind) {
		t.Helper()
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for {
			ready := m.connected
			for _, kind := range kinds {
				state := m.snapshotStates[kind]
				ready = ready && state.Loaded && !state.Pending && state.Failure == ""
			}
			if ready {
				return
			}
			select {
			case event := <-events:
				next, _ := m.Update(sessionEventMsg{Event: event, Open: true})
				m = next.(Model)
			case <-deadline.C:
				t.Fatalf("page %s did not finish fetching", m.active)
			}
		}
	}
	consumeUntilReady(pageSnapshots(ui.PageOverview))
	for _, entry := range []struct {
		digit rune
		page  ui.PageID
	}{
		{'2', ui.PageProxies}, {'4', ui.PageRules}, {'6', ui.PageSubscriptions},
		{'7', ui.PageWebGUI}, {'8', ui.PageSystem}, {'1', ui.PageOverview}, {'2', ui.PageProxies},
	} {
		next, _ := m.Update(tea.KeyPressMsg{Code: entry.digit, Text: string(entry.digit)})
		m = next.(Model)
		if m.active != entry.page || m.focus.Area != ui.FocusRail {
			t.Fatalf("navigation failed: %s", m.active)
		}
		consumeUntilReady(pageSnapshots(entry.page))
		if entry.page == ui.PageProxies && !m.snapshotActionReady(ui.ActionIntentMsg{Capability: protocol.CapabilityProxies}) {
			t.Fatal("fresh candidates remained disabled")
		}
	}
}
