package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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
	for _, want := range []string{"running", "v1.19.0", ui.ConfigApplyingLabel, "Web GUI", "mihomo", "Succeeded", "27.7 MiB"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
	if strings.Contains(view, "Phase 5") {
		t.Fatalf("overview must not mention Phase 5: %s", view)
	}
}

func TestOverview_GeneralCardServiceMihariSysProxyTun(t *testing.T) {
	live := true
	model := New()
	model.SetSize(100, 30)
	model.SetSnapshot(Snapshot{
		ServiceLoaded: true,
		ServiceStatus: service.StatusStopped,
		Connected:     true,
		MihariVersion: "0.1.0",
		SystemProxy: &protocol.SystemProxyStatus{
			Desired:  true,
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
		Tun:  &protocol.TunStatus{DesiredEnable: true, LiveEnable: &live, Stack: "gVisor", Managed: true},
		Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 1},
	})
	view := model.View()
	for _, want := range []string{
		ui.OverviewGeneralTitle,
		ui.OverviewServiceLabel, string(service.StatusStopped),
		ui.OverviewMihariLabel, "v0.1.0",
		ui.OverviewSysProxyLabel, ui.OverviewValueOwned,
		ui.OverviewTunLabel, ui.OverviewValueOn + "/gVisor",
		ui.CoreCardTitle,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("general card missing %q:\n%s", want, view)
		}
	}
	// Title is embedded in the top border, not a body line "---General---".
	if strings.Contains(view, "---General---") {
		t.Fatalf("title must be in top border, not body:\n%s", view)
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
		ui.OverviewGeneralTitle,
		string(service.StatusNotInstalled),
		"dev",
		ui.OverviewSysProxyLabel, ui.OverviewValueDash,
		ui.OverviewTunLabel, ui.OverviewValueDash,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("offline general card missing %q:\n%s", want, view)
		}
	}
}

func TestOverview_SysProxyDesiredObservedDrift(t *testing.T) {
	model := New()
	model.SetSize(100, 30)
	// Desired on but observed off. Note Owned is false here: sysproxy.IsOwned
	// gates on Enabled, so an off proxy can never be Owned. The daemon derives
	// Owned=false for this state; Overview must still flag the Desired-vs-Owned
	// drift (caution + "on · drift") instead of a green "on".
	model.SetSnapshot(Snapshot{
		Connected: true,
		SystemProxy: &protocol.SystemProxyStatus{
			Desired:  true,
			Observed: protocol.SystemProxyObserved{Enabled: false, Owned: false},
		},
		Core: protocol.CoreStatus{Status: "running"},
	})
	view := model.View()
	want := ui.OverviewValueOn + " · " + ui.OverviewDriftLabel
	if !strings.Contains(view, want) {
		t.Fatalf("drift badge should render %q:\n%s", want, view)
	}

	// Desired on but enabled to the wrong server (owned=false, foreign=false):
	// Desired!=Owned catches this; the older Desired!=Enabled check would miss
	// it because Enabled happens to be true.
	model.SetSnapshot(Snapshot{
		Connected: true,
		SystemProxy: &protocol.SystemProxyStatus{
			Desired:  true,
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "10.0.0.1:1", Owned: false, Foreign: false},
		},
		Core: protocol.CoreStatus{Status: "running"},
	})
	view = model.View()
	if !strings.Contains(view, ui.OverviewValueOn+" · "+ui.OverviewDriftLabel) {
		t.Fatalf("enabled-but-not-owned drift should render %q:\n%s", ui.OverviewValueOn+" · "+ui.OverviewDriftLabel, view)
	}

	// When desired and ownership agree (owned), the badge stays clean.
	model.SetSnapshot(Snapshot{
		Connected: true,
		SystemProxy: &protocol.SystemProxyStatus{
			Desired:  true,
			Observed: protocol.SystemProxyObserved{Enabled: true, Owned: true},
		},
		Core: protocol.CoreStatus{Status: "running"},
	})
	view = model.View()
	if strings.Contains(view, ui.OverviewDriftLabel) {
		t.Fatalf("no drift suffix when desired==owned:\n%s", view)
	}
	if !strings.Contains(view, ui.OverviewValueOwned) {
		t.Fatalf("owned badge should render %q:\n%s", ui.OverviewValueOwned, view)
	}
}

func TestOverview_SectionTitlesEmbeddedInBorder(t *testing.T) {
	model := New()
	model.SetSize(90, 28)
	model.SetSnapshot(Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.0.0", PID: 1},
	})
	view := model.View()
	for _, name := range []string{
		ui.OverviewGeneralTitle, ui.CoreCardTitle,
		ui.SubscriptionCardTitle, ui.WebGUICardTitle, ui.RecentOperationsTitle,
	} {
		if !strings.Contains(view, name) {
			t.Fatalf("missing section title %q:\n%s", name, view)
		}
	}
	// Spot-check: a line containing General also has top-border corners.
	found := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, ui.OverviewGeneralTitle) && strings.Contains(line, "╭") && strings.Contains(line, "╮") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("General title should sit on top border line:\n%s", view)
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

	for _, want := range []string{ui.CoreCardTitle, ui.SubscriptionCardTitle, ui.WebGUICardTitle, ui.RecentOperationsTitle} {
		if !strings.Contains(wideView, want) {
			t.Fatalf("wide view missing %q: %s", want, wideView)
		}
	}

	// Config state lives in the General Health row with the ok phrase; in a
	// half-width card the long phrase wraps, so match the visible fragments.
	if !strings.Contains(wideView, ui.OverviewHealthLabel) || !strings.Contains(wideView, "All Config Desired and") || !strings.Contains(wideView, "Applied Successfully") {
		t.Fatalf("wide view missing Health row:\n%s", wideView)
	}

	// General and Core titles should share a visual row when joined horizontally.
	foundPair := false
	for _, line := range strings.Split(wideView, "\n") {
		if strings.Contains(line, ui.OverviewGeneralTitle) && strings.Contains(line, ui.CoreCardTitle) {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Fatalf("expected General and Core titles on the same row in wide layout:\n%s", wideView)
	}

	narrow := New()
	narrow.SetSize(40, 26)
	narrow.SetSnapshot(snapshot)
	narrowView := narrow.View()
	// The wide view carries the banner above the cards while the narrow view
	// does not; compare only the card rows so the layout claim stays about
	// card density, not banner presence.
	wideLines := strings.Count(wideView, "\n") + 1
	if strings.Contains(wideView, dosRebelBanner[0]) {
		wideLines -= len(dosRebelBanner)
	}
	narrowLines := strings.Count(narrowView, "\n") + 1
	if wideLines >= narrowLines {
		t.Fatalf("wide 2-column layout should use fewer lines than narrow stack: wide=%d narrow=%d\nwide:\n%s\nnarrow:\n%s",
			wideLines, narrowLines, wideView, narrowView)
	}
}

func TestOverview_BannerVisibility(t *testing.T) {
	snapshot := Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.0.0", PID: 1},
	}

	// Wide + tall window: the dos_rebel wordmark shows above the cards.
	wide := New()
	wide.SetSize(90, 26)
	wide.SetSnapshot(snapshot)
	view := wide.View()
	for _, want := range []string{"██████", "░░███"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wide view missing banner glyph %q:\n%s", want, view)
		}
	}
	if index := strings.Index(view, "██████"); index < 0 || index > strings.Index(view, ui.OverviewGeneralTitle) {
		t.Fatalf("banner should render above the cards:\n%s", view)
	}

	// Narrow window: the 59-column banner cannot fit, so it is hidden.
	narrow := New()
	narrow.SetSize(40, 26)
	narrow.SetSnapshot(snapshot)
	if strings.Contains(narrow.View(), "██████") {
		t.Fatalf("narrow view must not render the banner:\n%s", narrow.View())
	}

	// Short window: cards stay fully visible, banner is hidden.
	short := New()
	short.SetSize(90, 20)
	short.SetSnapshot(snapshot)
	if strings.Contains(short.View(), "██████") {
		t.Fatalf("short view must not render the banner:\n%s", short.View())
	}
}

func TestOverview_CoreCardMergesCoreAndTraffic(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.19.0", PID: 42},
		Monitor: ui.MonitorSnapshot{
			Connections: 3, UploadTotal: 1 << 30, DownloadTotal: 2 << 30,
			UploadRate: 3 << 20, DownloadRate: 4 << 20, MemoryInUse: 1024,
		},
	})
	view := model.View()
	for _, want := range []string{
		"running", "v1.19.0", ui.MonitorMemoryShort, "3 conn",
		"↑1.0 GiB", "↓2.0 GiB", "3.0 MiB/s", "4.0 MiB/s",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, ui.MonitorTrafficTitle) {
		t.Fatalf("traffic card must be merged into Core: %s", view)
	}
}

func TestOverview_WebGUICardShowsAllAvailableStatus(t *testing.T) {
	model := New()
	model.SetSize(90, 26)
	model.SetSnapshot(Snapshot{
		Status: protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}},
		WebGUI: &protocol.WebGUIStatus{
			GatewayHealth: "healthy", GatewayAddr: "127.0.0.1:9191",
			ActivePanel: "Zashboard", BrowserSessions: 2,
		},
	})
	view := model.View()
	for _, want := range []string{"healthy", "127.0.0.1:9191", "Zashboard", "2 sessions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	// No default panel: show the address and single-session form.
	model.SetSnapshot(Snapshot{
		Status: protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}},
		WebGUI: &protocol.WebGUIStatus{GatewayHealth: "idle", BrowserSessions: 1},
	})
	view = model.View()
	for _, want := range []string{"idle", ui.NoDefaultPanelLabel, "1 session"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestOverview_NarrowLayoutStacksSingleColumn(t *testing.T) {
	model := New()
	model.SetSize(40, 26)
	model.SetSnapshot(Snapshot{
		Core: protocol.CoreStatus{Status: "running", Version: "v1.0.0", PID: 1},
	})
	view := model.View()
	// Single-column: General title line should not also contain Core.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, ui.OverviewGeneralTitle) && strings.Contains(line, ui.CoreCardTitle) {
			t.Fatalf("narrow layout should stack cards, not join General/Core horizontally:\n%s", view)
		}
	}
	if !strings.Contains(view, ui.OverviewGeneralTitle) || !strings.Contains(view, ui.CoreCardTitle) {
		t.Fatalf("narrow layout still needs all cards: %s", view)
	}
}

func TestOverview_MediumWidthKeepsFullCoreAndWebGUIMetrics(t *testing.T) {
	// 74 is above the old 60-column two-column tripwire but below the
	// half-card budget needed for Core IEC units and "N sessions" (issue #84).
	model := New()
	model.SetSize(74, 26)
	mem := int64(100 << 20)
	up := int64(136 << 20)
	down := int64(199 << 20)
	model.SetSnapshot(Snapshot{
		Status: protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}},
		Core:   protocol.CoreStatus{Status: "running", Version: "v1.19.29"},
		Monitor: ui.MonitorSnapshot{
			Connections:   58,
			MemoryInUse:   mem,
			UploadTotal:   up,
			DownloadTotal: down,
		},
		WebGUI: &protocol.WebGUIStatus{
			GatewayHealth:   "healthy",
			GatewayAddr:     "127.0.0.1:9191",
			ActivePanel:     "zashboard",
			BrowserSessions: 0,
		},
	})
	view := model.View()
	for _, want := range []string{
		ui.FormatBytes(mem),
		"↑" + ui.FormatBytes(up),
		"↓" + ui.FormatBytes(down),
		"0 sessions",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("medium width truncated %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, ui.OverviewGeneralTitle) && strings.Contains(line, ui.CoreCardTitle) {
			t.Fatalf("medium width should stack cards, not pair General/Core:\n%s", view)
		}
	}
}

func TestOverview_FullContentWidthKeepsWebGUISessions(t *testing.T) {
	// 84 is the Full shell content pane (terminal 100 − rail 16). Two-column
	// stays on, but the Web GUI address line is 40 cells and must wrap rather
	// than MaxWidth-clip "sessions".
	model := New()
	model.SetSize(84, 26)
	model.SetSnapshot(Snapshot{
		Status: protocol.Status{Capabilities: []string{protocol.CapabilityWebGUI}},
		Core:   protocol.CoreStatus{Status: "running", Version: "v1.19.29"},
		Monitor: ui.MonitorSnapshot{
			Connections:   58,
			MemoryInUse:   100 << 20,
			UploadTotal:   136 << 20,
			DownloadTotal: 199 << 20,
		},
		WebGUI: &protocol.WebGUIStatus{
			GatewayHealth:   "healthy",
			GatewayAddr:     "127.0.0.1:9191",
			ActivePanel:     "zashboard",
			BrowserSessions: 0,
		},
	})
	view := model.View()
	for _, want := range []string{
		ui.FormatBytes(100 << 20),
		"↑" + ui.FormatBytes(136<<20),
		"↓" + ui.FormatBytes(199<<20),
		"127.0.0.1:9191",
		"zashboard",
		"sessions",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("full content width truncated %q:\n%s", want, view)
		}
	}
	foundPair := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, ui.OverviewGeneralTitle) && strings.Contains(line, ui.CoreCardTitle) {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Fatalf("full content width should keep the 2-column grid:\n%s", view)
	}
}
