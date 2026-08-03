package overview

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

type Snapshot struct {
	Status        protocol.Status
	Core          protocol.CoreStatus
	Subscriptions protocol.SubscriptionList
	Monitor       ui.MonitorSnapshot
	Operations    []ui.OperationRecord
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
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "left" {
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	}
	return m, nil
}

func (m *Model) View() string {
	coreStatus := valueOr(m.snapshot.Core.Status, ui.UnknownLabel)
	coreVersion := valueOr(m.snapshot.Core.Version, ui.UnknownLabel)
	core := fmt.Sprintf("%s · %s\n%s %d", coreStatus, coreVersion, ui.PIDLabel, m.snapshot.Core.PID)

	config := ui.UnavailableTitle
	if current := m.snapshot.Status.Config; current != nil {
		config = fmt.Sprintf("%s\n%s %d · %s %d", valueOr(current.Status, ui.UnknownLabel),
			ui.DesiredLabel, current.DesiredRevision, ui.ObservedLabel, current.ObservedRevision)
		if current.LastError != "" {
			config += "\n" + current.LastError
		}
	}

	subscription := ui.UnavailableTitle
	for _, profile := range m.snapshot.Subscriptions.Subscriptions {
		if profile.ID != m.snapshot.Subscriptions.ActiveID {
			continue
		}
		subscription = profile.Name
		if profile.UpdatedAt.IsZero() {
			subscription += " · " + ui.CacheMissingLabel
		} else {
			subscription += " · " + profile.UpdatedAt.Local().Format("2006-01-02 15:04")
		}
		if profile.LastError != "" {
			subscription += "\n" + profile.LastError
		}
		break
	}

	webGUI := ui.UnavailableTitle
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
		ui.MonitorUploadShort, formatRate(m.snapshot.Monitor.UploadRate),
		ui.MonitorDownloadShort, formatRate(m.snapshot.Monitor.DownloadRate),
		ui.MonitorMemoryShort, formatBytes(m.snapshot.Monitor.MemoryInUse))
	chartWidth := min(50, max(8, m.width-12))
	upload := make([]int64, len(m.snapshot.Monitor.Traffic))
	download := make([]int64, len(m.snapshot.Monitor.Traffic))
	for index, point := range m.snapshot.Monitor.Traffic {
		upload[index], download[index] = point.Up, point.Down
	}
	traffic += "\n" + ui.MonitorUploadShort + " " + ui.Sparkline(upload, chartWidth)
	traffic += "\n" + ui.MonitorDownloadShort + " " + ui.Sparkline(download, chartWidth)

	cards := []string{
		m.card(ui.CoreCardTitle, core),
		m.card(ui.ConfigCardTitle, config),
		m.card(ui.SubscriptionCardTitle, subscription),
		m.card(ui.WebGUICardTitle, webGUI),
		m.card(ui.MonitorTrafficTitle, traffic),
		m.card(ui.RecentOperationsTitle, operations),
	}
	return m.theme.Content.Width(m.width).Height(m.height).Render(strings.Join(cards, "\n"))
}

func (m *Model) card(title, body string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(max(24, m.width-4)).Render(
		m.theme.Title.Render(title) + "\n" + body,
	)
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatRate(value int64) string { return formatBytes(value) + "/s" }

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", max(int64(0), value))
	}
	return fmt.Sprintf("%.1f KiB", float64(value)/1024)
}
