package overview

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/service"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

// wideMinWidth is the content width at which KPI cards switch to a 2-column grid.
const wideMinWidth = 60

type Snapshot struct {
	Status        protocol.Status
	Core          protocol.CoreStatus
	Subscriptions protocol.SubscriptionList
	Monitor       ui.MonitorSnapshot
	Operations    []ui.OperationRecord
	// Local / daemon-backed strip fields (Service is OS-local; others need daemon).
	ServiceStatus service.StatusKind
	ServiceLoaded bool
	Connected     bool
	MihariVersion string
	SystemProxy   *protocol.SystemProxyStatus
	Tun           *protocol.TunStatus
}

type Model struct {
	width    int
	height   int
	snapshot Snapshot
	theme    ui.Theme
}

func New() *Model { return &Model{theme: ui.DefaultTheme()} }

func (m *Model) ID() ui.PageID { return ui.PageOverview }

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() {}

func (m *Model) SetSnapshot(snapshot Snapshot) {
	snapshot.Operations = append([]ui.OperationRecord(nil), snapshot.Operations...)
	snapshot.Monitor.Traffic = append([]ui.TrafficPoint(nil), snapshot.Monitor.Traffic...)
	snapshot.Monitor.Memory = append([]ui.MemoryPoint(nil), snapshot.Monitor.Memory...)
	m.snapshot = snapshot
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "esc" {
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	}
	return m, nil
}

func (m *Model) View() string {
	general := m.renderGeneralBody()

	coreStatus := valueOr(m.snapshot.Core.Status, ui.UnknownLabel)
	coreVersion := valueOr(m.snapshot.Core.Version, ui.UnknownLabel)
	core := fmt.Sprintf("%s · %s\n%s %d", coreStatus, coreVersion, ui.PIDLabel, m.snapshot.Core.PID)

	config := ui.ConfigNotAppliedLabel
	if current := m.snapshot.Status.Config; current != nil {
		config = fmt.Sprintf("%s\n%s %d · %s %d", valueOr(current.Status, ui.UnknownLabel),
			ui.DesiredLabel, current.DesiredRevision, ui.ObservedLabel, current.ObservedRevision)
		if current.LastError != "" {
			config += "\n" + current.LastError
		}
	}

	subscription := renderActiveSubscription(m.snapshot.Subscriptions)

	webGUI := ui.WebGUIUnavailable
	if slices.Contains(m.snapshot.Status.Capabilities, protocol.CapabilityWebGUI) {
		webGUI = ui.AvailableLabel
	}

	operations := ui.NoRecentOperations
	if len(m.snapshot.Operations) > 0 {
		lines := make([]string, 0, min(5, len(m.snapshot.Operations)))
		start := max(0, len(m.snapshot.Operations)-5)
		for _, operation := range m.snapshot.Operations[start:] {
			lines = append(lines, fmt.Sprintf("%s · %s", valueOr(operation.Object, operation.ID), operation.State))
		}
		operations = strings.Join(lines, "\n")
	}

	traffic := fmt.Sprintf("%s %d\n%s %s · %s %s · %s %s",
		ui.MonitorConnectionsLabel, m.snapshot.Monitor.Connections,
		ui.MonitorUploadShort, ui.FormatRate(m.snapshot.Monitor.UploadRate),
		ui.MonitorDownloadShort, ui.FormatRate(m.snapshot.Monitor.DownloadRate),
		ui.MonitorMemoryShort, ui.FormatBytes(m.snapshot.Monitor.MemoryInUse))
	chartWidth := min(50, max(8, m.width-12))
	upload := make([]int64, len(m.snapshot.Monitor.Traffic))
	download := make([]int64, len(m.snapshot.Monitor.Traffic))
	for index, point := range m.snapshot.Monitor.Traffic {
		upload[index], download[index] = point.Up, point.Down
	}
	traffic += "\n" + ui.MonitorUploadShort + " " + ui.Sparkline(upload, chartWidth)
	traffic += "\n" + ui.MonitorDownloadShort + " " + ui.Sparkline(download, chartWidth)

	// Do not force another full-width Content box here: the root shell already
	// sizes the content pane. Re-applying Width(m.width) plus card borders clips
	// the right edge of every card.
	generalCard := m.card(ui.OverviewGeneralTitle, general)
	if m.width >= wideMinWidth {
		half := m.halfCardInner()
		row1 := lipgloss.JoinHorizontal(lipgloss.Top,
			m.cardAt(ui.CoreCardTitle, core, half),
			m.cardAt(ui.ConfigCardTitle, config, half),
		)
		row2 := lipgloss.JoinHorizontal(lipgloss.Top,
			m.cardAt(ui.SubscriptionCardTitle, subscription, half),
			m.cardAt(ui.WebGUICardTitle, webGUI, half),
		)
		return lipgloss.JoinVertical(lipgloss.Left,
			generalCard,
			row1,
			row2,
			m.card(ui.MonitorTrafficTitle, traffic),
			m.card(ui.RecentOperationsTitle, operations),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		generalCard,
		m.card(ui.CoreCardTitle, core),
		m.card(ui.ConfigCardTitle, config),
		m.card(ui.SubscriptionCardTitle, subscription),
		m.card(ui.WebGUICardTitle, webGUI),
		m.card(ui.MonitorTrafficTitle, traffic),
		m.card(ui.RecentOperationsTitle, operations),
	)
}

// renderGeneralBody is the General card content: Service / Mihari / SysProxy / TUN.
func (m *Model) renderGeneralBody() string {
	// Two-column-ish label layout for readability inside the bordered card.
	rows := []struct{ label, value string }{
		{ui.OverviewServiceLabel, formatServiceValue(m.snapshot)},
		{ui.OverviewMihariLabel, formatMihariVersion(m.snapshot.MihariVersion)},
		{ui.OverviewSysProxyLabel, formatSysProxyValue(m.snapshot)},
		{ui.OverviewTunLabel, formatTunValue(m.snapshot)},
	}
	const labelWidth = 9 // longest label "SysProxy"
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		label := row.label + strings.Repeat(" ", max(0, labelWidth-len(row.label)))
		lines = append(lines, label+"  "+row.value)
	}
	return strings.Join(lines, "\n")
}

func formatServiceValue(snap Snapshot) string {
	if !snap.ServiceLoaded {
		return ui.OverviewValueDash
	}
	switch snap.ServiceStatus {
	case service.StatusNotInstalled:
		return string(service.StatusNotInstalled)
	case service.StatusStopped:
		return string(service.StatusStopped)
	case service.StatusRunning:
		return string(service.StatusRunning)
	default:
		return string(service.StatusUnknown)
	}
}

func formatMihariVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ui.OverviewValueDash
	}
	if !strings.HasPrefix(strings.ToLower(version), "v") && version != "dev" {
		return "v" + version
	}
	return version
}

func formatSysProxyValue(snap Snapshot) string {
	if !snap.Connected || snap.SystemProxy == nil {
		return ui.OverviewValueDash
	}
	obs := snap.SystemProxy.Observed
	switch {
	case obs.Foreign:
		return ui.OverviewValueForeign
	case obs.Owned:
		return ui.OverviewValueOwned
	case obs.Enabled:
		return ui.OverviewValueOn
	case snap.SystemProxy.Desired:
		return ui.OverviewValueOn
	default:
		return ui.OverviewValueOff
	}
}

func formatTunValue(snap Snapshot) string {
	if !snap.Connected || snap.Tun == nil {
		return ui.OverviewValueDash
	}
	if snap.Tun.LiveEnable != nil {
		if *snap.Tun.LiveEnable {
			if snap.Tun.Stack != "" {
				return ui.OverviewValueOn + "/" + snap.Tun.Stack
			}
			return ui.OverviewValueOn
		}
		return ui.OverviewValueOff
	}
	if snap.Tun.DesiredEnable {
		return ui.OverviewValueOn
	}
	return ui.OverviewValueOff
}

func renderActiveSubscription(list protocol.SubscriptionList) string {
	if len(list.Subscriptions) == 0 {
		return ui.NoSubscriptionsConfiguredLabel
	}
	for _, profile := range list.Subscriptions {
		if list.ActiveID == "" || profile.ID != list.ActiveID {
			continue
		}
		subscription := profile.Name
		if profile.UpdatedAt.IsZero() {
			subscription += " · " + ui.CacheMissingLabel
		} else {
			subscription += " · " + profile.UpdatedAt.Local().Format("2006-01-02 15:04")
		}
		if profile.LastError != "" {
			subscription += "\n" + profile.LastError
		}
		return subscription
	}
	return ui.NoActiveSubscriptionLabel
}

func (m *Model) fullCardInner() int {
	// Lipgloss Width is the inner block; rounded borders add 2 columns outside.
	// Leave room for root Content horizontal padding (2) and the border (2).
	return max(20, m.width-4)
}

func (m *Model) halfCardInner() int {
	// Two side-by-side cards: each has a 2-column border; leave content padding (2).
	// 2*(inner+2) <= m.width-2  =>  inner <= (m.width-6)/2
	return max(10, (m.width-6)/2)
}

func (m *Model) card(title, body string) string {
	return m.cardAt(title, body, m.fullCardInner())
}

func (m *Model) cardAt(title, body string, inner int) string {
	// Title sits in the top border edge: ╭─── Name ────────╮
	// `inner` matches the previous lipgloss content Width (padding included).
	return ui.RenderBorderedSection(m.theme, title, body, inner)
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
