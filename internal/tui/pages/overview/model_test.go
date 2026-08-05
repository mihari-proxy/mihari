package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/service"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestOverview_RendersAuthoritativeCardsAndSessionOperations(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Status: protocol.Status{
			Health: "ok", Revision: 7,
			Config: &protocol.ConfigStatus{Status: "applying", DesiredRevision: 5, ObservedRevision: 4},
		},
		Core:       protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 42},
		Monitor:    ui.MonitorSnapshot{Traffic: []ui.TrafficPoint{{Up: 1, Down: 2}}, MemoryInUse: 28_356 * 1024},
		Operations: []ui.OperationRecord{{Object: "mihomo", State: "Succeeded", At: time.Unix(100, 0).UTC()}},
	})
	view := model.View()
	for _, want := range []string{"running", "v1.19.0", "PID 42", "Desired 5", "Observed 4", "Web GUI", "mihomo", "Succeeded", "27.7 MiB"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
	if strings.Contains(view, "Phase 5") {
		t.Fatalf("overview must not mention Phase 5: %s", view)
	}
}

func TestOverview_SummaryStripServiceMihariSysProxyTun(t *testing.T) {
	live := true
	model := New()
	model.SetSize(100, 26)
	model.SetSnapshot(Snapshot{
		ServiceLoaded: true,
		ServiceStatus: service.StatusStopped,
		Connected:     true,
		MihariVersion: "0.1.0",
		SystemProxy: &protocol.SystemProxyStatus{
			Desired:  true,
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
		Tun: &protocol.TunStatus{DesiredEnable: true, LiveEnable: &live, Stack: "gVisor", Managed: true},
		Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 1},
	})
	view := model.View()
	for _, want := range []string{
		ui.OverviewServiceLabel, string(service.StatusStopped),
		ui.OverviewMihariLabel, "v0.1.0",
		ui.OverviewSysProxyLabel, ui.OverviewValueOwned,
		ui.OverviewTunLabel, ui.OverviewValueOn + "/gVisor",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("summary strip missing %q:\n%s", want, view)
		}
	}

	// Offline daemon: sysproxy/TUN show dash; service still visible.
	model.SetSnapshot(Snapshot{
		ServiceLoaded: true,
		ServiceStatus: service.StatusNotInstalled,
		Connected:     false,
		MihariVersion: "dev",
		Core:          protocol.CoreStatus{Status: "stopped"},
	})
	view = model.View()
	for _, want := range []string{
		ui.OverviewServiceLabel, string(service.StatusNotInstalled),
		ui.OverviewMihariLabel, "dev",
		ui.OverviewSysProxyLabel + "  " + ui.OverviewValueDash,
		ui.OverviewTunLabel + "  " + ui.OverviewValueDash,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("offline strip missing %q:\n%s", want, view)
		}
	}
}

func TestOverview_EmptyConfigAndSubscriptionUseClearLabels(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 1},
	})
	view := model.View()
	for _, want := range []string{ui.ConfigNotAppliedLabel, ui.NoSubscriptionsConfiguredLabel, ui.WebGUIUnavailable} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %s", want, view)
		}
	}
	if strings.Contains(view, ui.UnavailableTitle) {
		t.Fatalf("empty overview should not use bare Unavailable: %s", view)
	}
	if strings.Contains(view, "Phase") {
		t.Fatalf("overview must not mention Phase: %s", view)
	}
}

func TestOverview_NoActiveSubscriptionWhenListHasProfiles(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Subscriptions: protocol.SubscriptionList{
			Subscriptions: []protocol.Subscription{{ID: "one", Name: "Main"}},
		},
	})
	view := model.View()
	if !strings.Contains(view, ui.NoActiveSubscriptionLabel) {
		t.Fatalf("view=%s", view)
	}
}

func TestOverview_WideLayoutUsesTwoColumnKPIGrid(t *testing.T) {
	snapshot := Snapshot{
		Status: protocol.Status{
			Config: &protocol.ConfigStatus{Status: "ok", DesiredRevision: 1, ObservedRevision: 1},
		},
		Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 42},
		Monitor: ui.MonitorSnapshot{
			Traffic:     []ui.TrafficPoint{{Up: 1, Down: 2}},
			MemoryInUse: 1024,
		},
		Operations: []ui.OperationRecord{{Object: "mihomo", State: "Succeeded"}},
	}

	wide := New()
	wide.SetSize(90, 26)
	wide.SetSnapshot(snapshot)
	wideView := wide.View()

	for _, want := range []string{ui.CoreCardTitle, ui.ConfigCardTitle, ui.SubscriptionCardTitle, ui.WebGUICardTitle, ui.MonitorTrafficTitle, ui.RecentOperationsTitle} {
		if !strings.Contains(wideView, want) {
			t.Fatalf("wide view missing %q: %s", want, wideView)
		}
	}

	// Core and Config titles should share a visual row when joined horizontally.
	foundPair := false
	for _, line := range strings.Split(wideView, "\n") {
		if strings.Contains(line, ui.CoreCardTitle) && strings.Contains(line, ui.ConfigCardTitle) {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Fatalf("expected Core and Config titles on the same row in wide layout:\n%s", wideView)
	}

	narrow := New()
	narrow.SetSize(40, 26)
	narrow.SetSnapshot(snapshot)
	narrowView := narrow.View()
	wideLines := strings.Count(wideView, "\n") + 1
	narrowLines := strings.Count(narrowView, "\n") + 1
	if wideLines >= narrowLines {
		t.Fatalf("wide 2-column layout should use fewer lines than narrow stack: wide=%d narrow=%d\nwide:\n%s\nnarrow:\n%s",
			wideLines, narrowLines, wideView, narrowView)
	}
}

func TestOverview_NarrowLayoutStacksSingleColumn(t *testing.T) {
	model := New()
	model.SetSize(40, 26)
	model.SetSnapshot(Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.0.0", PID: 1},
	})
	view := model.View()
	// Single-column: Core title line should not also contain Config.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, ui.CoreCardTitle) && strings.Contains(line, ui.ConfigCardTitle) {
			t.Fatalf("narrow layout should stack cards, not join Core/Config horizontally:\n%s", view)
		}
	}
	if !strings.Contains(view, ui.CoreCardTitle) || !strings.Contains(view, ui.ConfigCardTitle) {
		t.Fatalf("narrow layout still needs all cards: %s", view)
	}
}
