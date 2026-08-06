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

// overviewLabelWidth is the label column width of the General card grid;
// continuation lines of wrapped values (e.g. Health) indent to match it.
const overviewLabelWidth = 9

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
	// WebGUI carries the daemon Web gateway snapshot for the Web GUI card.
	WebGUI *protocol.WebGUIStatus
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
	general := m.renderGeneralBody(m.fullCardInner())

	subscription := renderActiveSubscription(m.snapshot.Subscriptions, m.snapshot.Stale)
	webGUI := m.renderWebGUICard()

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

	// Do not force another full-width Content box here: the root shell already
	// sizes the content pane. Re-applying Width(m.width) plus card borders clips
	// the right edge of every card.
	// The dos_rebel wordmark sits above the cards; renderBanner returns "" on
	// narrow or short windows, so build the joined parts only from non-empty
	// strings to avoid a stray blank line.
	parts := make([]string, 0, 3)
	if banner := renderBanner(m.theme, m.width, m.height); banner != "" {
		parts = append(parts, banner)
	}
	// Wide layout: General pairs with Core on row 1, Subscription with Web GUI
	// on row 2 (config state lives in the General Health row). Below wideMinWidth
	// everything stacks single-column.
	if m.width >= wideMinWidth {
		half := m.halfCardInner()
		// Equalize body line counts so both cards of a row end at the same
		// bottom border (JoinHorizontal aligns tops; card height is body-driven).
		generalBody, coreBody := ui.EqualizeLineCount(m.renderGeneralBody(half), m.renderCoreCard(half))
		row1 := lipgloss.JoinHorizontal(lipgloss.Top,
			m.cardAt(ui.OverviewGeneralTitle, generalBody, half),
			m.cardAt(ui.CoreCardTitle, coreBody, half),
		)
		subBody, guiBody := ui.EqualizeLineCount(subscription, webGUI)
		row2 := lipgloss.JoinHorizontal(lipgloss.Top,
			m.cardAt(ui.SubscriptionCardTitle, subBody, half),
			m.cardAt(ui.WebGUICardTitle, guiBody, half),
		)
		parts = append(parts, row1, row2, m.card(ui.RecentOperationsTitle, operations))
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	parts = append(parts,
		m.card(ui.OverviewGeneralTitle, general),
		m.card(ui.CoreCardTitle, m.renderCoreCard(m.fullCardInner())),
		m.card(ui.SubscriptionCardTitle, subscription),
		m.card(ui.WebGUICardTitle, webGUI),
		m.card(ui.RecentOperationsTitle, operations),
	)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderCoreCard merges core state and live traffic into one card (design G4):
// line 1 core status/version + memory; line 2 connections + UL/DL totals with
// the global arrow pair; then the UL/DL trend sparklines with the live rate
// fixed at the end of each chart line (no standalone rate row — the rates
// update every stream tick and would otherwise shift line width).
// Core PID/restarts stay on the System page (design G6).
func (m *Model) renderCoreCard(inner int) string {
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
	line1 := fmt.Sprintf("%s  %s · %s %s",
		coreStatus, m.theme.Muted.Render(coreVersion),
		ui.MonitorMemoryShort, ui.FormatBytes(m.snapshot.Monitor.MemoryInUse))
	connLabel := fmt.Sprintf("%d conn", m.snapshot.Monitor.Connections)
	if m.snapshot.Stale {
		connLabel += " · " + ui.StaleLabel
	}
	line2 := fmt.Sprintf("%s · %s",
		connLabel,
		ui.StyleTrafficPair(m.theme,
			"↑"+ui.FormatBytes(m.snapshot.Monitor.UploadTotal),
			"↓"+ui.FormatBytes(m.snapshot.Monitor.DownloadTotal)))
	// Reserve a fixed width for the trailing rate so chart lines never shift.
	const rateReserve = 12 // " 999.9 GiB/s" worst case
	chartWidth := min(50, max(8, inner-len(ui.MonitorUploadShort)-1-rateReserve))
	upload := make([]int64, len(m.snapshot.Monitor.Traffic))
	download := make([]int64, len(m.snapshot.Monitor.Traffic))
	for index, point := range m.snapshot.Monitor.Traffic {
		upload[index], download[index] = point.Up, point.Down
	}
	return strings.Join([]string{
		line1, line2,
		ui.MonitorUploadShort + " " + ui.Sparkline(upload, chartWidth) + " " + ui.FormatRate(m.snapshot.Monitor.UploadRate),
		ui.MonitorDownloadShort + " " + ui.Sparkline(download, chartWidth) + " " + ui.FormatRate(m.snapshot.Monitor.DownloadRate),
	}, "\n")
}

// formatConfigHealth renders the config state as the General card's Health row.
// The ok phrase is long, so it wraps within the value column with continuation
// lines indented under the value; the raw Desired/Observed revision numbers
// stay on the System page.
func formatConfigHealth(theme ui.Theme, snap Snapshot, valueWidth int) string {
	current := snap.Status.Config
	if current == nil {
		return ui.StatusDot(theme, ui.ToneNeutral, ui.ConfigNotAppliedLabel)
	}
	if current.LastError != "" {
		// A failed apply (e.g. rollback) carries the actionable detail.
		indent := strings.Repeat(" ", overviewLabelWidth+2)
		return ui.StatusDot(theme, ui.ToneNegative, ui.ConfigFailedLabel) +
			"\n" + indent + ui.ToneStyle(theme, ui.ToneNegative).Render(current.LastError)
	}
	switch valueOr(current.Status, ui.UnknownLabel) {
	case "ok":
		return wrapStatusDot(theme, ui.TonePositive, ui.ConfigHealthOKLabel, valueWidth)
	case "applying":
		return ui.StatusDot(theme, ui.ToneCaution, ui.ConfigApplyingLabel)
	default:
		return ui.StatusDot(theme, ui.ClassifyStatusTone(valueOr(current.Status, ui.UnknownLabel)), valueOr(current.Status, ui.UnknownLabel))
	}
}

// wrapStatusDot renders "● text" and wraps it at valueWidth, indenting
// continuation lines so they align under the value column of the label grid.
func wrapStatusDot(theme ui.Theme, tone ui.StatusTone, text string, valueWidth int) string {
	if valueWidth <= 0 {
		return ui.StatusDot(theme, tone, text)
	}
	wrapped := lipgloss.NewStyle().Width(valueWidth).Render(ui.StatusDot(theme, tone, text))
	return strings.ReplaceAll(wrapped, "\n", "\n"+strings.Repeat(" ", overviewLabelWidth+2))
}

// renderWebGUICard shows the gateway health dot plus address, default panel and
// browser sessions once the daemon supplies a WebGUI snapshot (design G5).
func (m *Model) renderWebGUICard() string {
	status := m.snapshot.WebGUI
	if status == nil {
		if slices.Contains(m.snapshot.Status.Capabilities, protocol.CapabilityWebGUI) {
			return ui.StatusDot(m.theme, ui.TonePositive, ui.AvailableLabel)
		}
		return ui.ToneStyle(m.theme, ui.ToneNeutral).Render(ui.WebGUIUnavailable)
	}
	health := valueOr(status.GatewayHealth, ui.UnknownLabel)
	dot := ui.StatusDot(m.theme, ui.ClassifyStatusTone(health), health)
	active := valueOr(status.ActivePanel, ui.NoDefaultPanelLabel)
	sessions := fmt.Sprintf("%d session", status.BrowserSessions)
	if status.BrowserSessions != 1 {
		sessions += "s"
	}
	return fmt.Sprintf("%s\n%s · %s · %s",
		dot,
		valueOr(status.GatewayAddr, ui.MissingValue),
		active,
		sessions)
}

// renderGeneralBody is the General card content: Service / Mihari / SysProxy /
// TUN / Health (config state). inner is the card content width; the Health
// phrase wraps within the value column when it is too wide.
func (m *Model) renderGeneralBody(inner int) string {
	// Two-column-ish label layout for readability inside the bordered card.
	rows := []struct{ label, value string }{
		{ui.OverviewServiceLabel, formatServiceValue(m.theme, m.snapshot)},
		{ui.OverviewMihariLabel, formatMihariVersion(m.snapshot.MihariVersion)},
		{ui.OverviewSysProxyLabel, formatSysProxyValue(m.theme, m.snapshot)},
		{ui.OverviewTunLabel, formatTunValue(m.theme, m.snapshot)},
		{ui.OverviewHealthLabel, formatConfigHealth(m.theme, m.snapshot, inner-overviewLabelWidth-2)},
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		label := row.label + strings.Repeat(" ", max(0, overviewLabelWidth-len(row.label)))
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
