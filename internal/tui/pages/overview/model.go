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
	// Stale marks last-known values when the daemon stream is stale; cards
	// keep the last values and append " · Stale" (design G2).
	Stale bool
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

	coreStatusRaw := valueOr(m.snapshot.Core.Status, ui.UnknownLabel)
	coreTone := ui.ClassifyStatusTone(coreStatusRaw)
	if m.snapshot.Stale {
		// Keep the last-known glyph, degrade the dot to caution yellow, and
		// mark the value as stale (design G2).
		coreTone = ui.ToneCaution
		coreStatusRaw += " · " + ui.StaleLabel
	}
	coreStatus := ui.StatusDot(m.theme, coreTone, coreStatusRaw)
	coreVersion := valueOr(m.snapshot.Core.Version, ui.UnknownLabel)
	core := fmt.Sprintf("%s  %s\n%s %d", coreStatus, m.theme.Muted.Render(coreVersion), ui.PIDLabel, m.snapshot.Core.PID)

	config := ui.ConfigNotAppliedLabel
	if current := m.snapshot.Status.Config; current != nil {
		configStatus := valueOr(current.Status, ui.UnknownLabel)
		config = fmt.Sprintf("%s\n%s %d · %s %d", ui.StatusDot(m.theme, ui.ClassifyStatusTone(configStatus), configStatus),
			ui.DesiredLabel, current.DesiredRevision, ui.ObservedLabel, current.ObservedRevision)
		if current.LastError != "" {
			config += "\n" + ui.ToneStyle(m.theme, ui.ToneNegative).Render(current.LastError)
		}
	}

	subscription := renderActiveSubscription(m.snapshot.Subscriptions, m.snapshot.Stale)

	var webGUI string
	if slices.Contains(m.snapshot.Status.Capabilities, protocol.CapabilityWebGUI) {
		webGUI = ui.StatusDot(m.theme, ui.TonePositive, ui.AvailableLabel)
	} else {
		webGUI = ui.ToneStyle(m.theme, ui.ToneNeutral).Render(ui.WebGUIUnavailable)
	}

	operations := ui.NoRecentOperations
	if len(m.snapshot.Operations) > 0 {
		lines := make([]string, 0, min(5, len(m.snapshot.Operations)))
		start := max(0, len(m.snapshot.Operations)-5)
		for _, operation := range m.snapshot.Operations[start:] {
			state := ui.ToneStyle(m.theme, ui.ClassifyStatusTone(operation.State)).Render(operation.State)
			lines = append(lines, fmt.Sprintf("%s · %s", valueOr(operation.Object, operation.ID), state))
		}
		operations = strings.Join(lines, "\n")
	}

	connLabel := fmt.Sprintf("%d conn", m.snapshot.Monitor.Connections)
	if m.snapshot.Stale {
		connLabel += " · " + ui.StaleLabel
	}
	traffic := fmt.Sprintf("%s\n%s %s · %s %s · %s %s",
		connLabel,
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
		{ui.OverviewServiceLabel, formatServiceValue(m.theme, m.snapshot)},
		{ui.OverviewMihariLabel, formatMihariVersion(m.snapshot.MihariVersion)},
		{ui.OverviewSysProxyLabel, formatSysProxyValue(m.theme, m.snapshot)},
		{ui.OverviewTunLabel, formatTunValue(m.theme, m.snapshot)},
	}
	const labelWidth = 9 // longest label "SysProxy"
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		label := row.label + strings.Repeat(" ", max(0, labelWidth-len(row.label)))
		lines = append(lines, label+"  "+row.value)
	}
	return strings.Join(lines, "\n")
}

func formatServiceValue(theme ui.Theme, snap Snapshot) string {
	if !snap.ServiceLoaded {
		return ui.OverviewValueDash
	}
	var status string
	switch snap.ServiceStatus {
	case service.StatusNotInstalled:
		status = string(service.StatusNotInstalled)
	case service.StatusStopped:
		status = string(service.StatusStopped)
	case service.StatusRunning:
		status = string(service.StatusRunning)
	default:
		status = string(service.StatusUnknown)
	}
	return ui.StatusDot(theme, ui.ClassifyStatusTone(status), status)
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

func formatSysProxyValue(theme ui.Theme, snap Snapshot) string {
	if !snap.Connected || snap.SystemProxy == nil {
		return ui.OverviewValueDash
	}
	obs := snap.SystemProxy.Observed
	var label string
	tone := ui.ToneNeutral
	switch {
	case obs.Foreign:
		label = ui.OverviewValueForeign
		tone = ui.ToneCaution
	case obs.Owned:
		label = ui.OverviewValueOwned
		tone = ui.TonePositive
	case obs.Enabled:
		label = ui.OverviewValueOn
		tone = ui.TonePositive
	case snap.SystemProxy.Desired:
		label = ui.OverviewValueOn
		tone = ui.TonePositive
	default:
		label = ui.OverviewValueOff
	}
	if snap.Stale {
		// Keep the last-known value and mark it stale (design G2).
		label += " · " + ui.StaleLabel
	}
	return ui.StatusDot(theme, tone, label)
}

func formatTunValue(theme ui.Theme, snap Snapshot) string {
	if !snap.Connected || snap.Tun == nil {
		return ui.OverviewValueDash
	}
	var label string
	tone := ui.ToneNeutral
	if snap.Tun.LiveEnable != nil {
		if *snap.Tun.LiveEnable {
			tone = ui.TonePositive
			if snap.Tun.Stack != "" {
				label = ui.OverviewValueOn + "/" + snap.Tun.Stack
			} else {
				label = ui.OverviewValueOn
			}
		} else {
			label = ui.OverviewValueOff
		}
	} else if snap.Tun.DesiredEnable {
		label = ui.OverviewValueOn
	} else {
		label = ui.OverviewValueOff
	}
	if snap.Stale {
		// Keep the last-known value and mark it stale (design G2).
		label += " · " + ui.StaleLabel
	}
	return ui.StatusDot(theme, tone, label)
}

func renderActiveSubscription(list protocol.SubscriptionList, stale bool) string {
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
		// Usage line (design G3): ↑/↓ IEC values, total omitted; hidden when
		// the provider published no usage (0/0/0).
		if profile.Upload > 0 || profile.Download > 0 {
			subscription += "\n↑" + ui.FormatBytes(profile.Upload) + " · ↓" + ui.FormatBytes(profile.Download)
		}
		if stale {
			subscription += " · " + ui.StaleLabel
		}
		if profile.LastError != "" {
			subscription += "\n" + profile.LastError
		}
		return subscription
	}
	return ui.NoActiveSubscriptionLabel
}

func (m *Model) fullCardInner() int {
	return ui.FullSectionInner(m.width)
}

func (m *Model) halfCardInner() int {
	return ui.HalfSectionInner(m.width)
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
