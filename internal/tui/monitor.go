package tui

import (
	"fmt"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/session"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

const monitorSampleCapacity = 60

type MonitorModel struct {
	traffic       []ui.TrafficPoint
	memory        []ui.MemoryPoint
	connections   int
	memoryInUse   int64
	uploadTotal   int64
	downloadTotal int64
	uploadRate    int64
	downloadRate  int64
	stale         bool
}

func NewMonitor() MonitorModel { return MonitorModel{} }

func (m *MonitorModel) ObserveTraffic(up, down int64) {
	m.uploadRate, m.downloadRate = up, down
	m.traffic = appendBounded(m.traffic, ui.TrafficPoint{Up: up, Down: down})
}

func (m *MonitorModel) ObserveMemory(inUse int64) {
	m.memoryInUse = inUse
	m.memory = appendBounded(m.memory, ui.MemoryPoint{InUse: inUse})
}

func (m *MonitorModel) ObserveConnections(connections protocol.ConnectionList) {
	m.connections = len(connections.Connections)
	m.uploadTotal = connections.UploadTotal
	m.downloadTotal = connections.DownloadTotal
}

func (m *MonitorModel) Observe(event session.Event) {
	switch event.Kind {
	case session.EventTraffic:
		m.ObserveTraffic(event.Traffic.Up, event.Traffic.Down)
	case session.EventMemory:
		m.ObserveMemory(event.Memory.InUse)
	case session.EventConnections:
		m.ObserveConnections(event.Connections)
	case session.EventConnected:
		m.SetStale(false)
	case session.EventReconnecting, session.EventTerminalError:
		m.SetStale(true)
	}
}

func (m *MonitorModel) SetStale(stale bool) { m.stale = stale }

func (m MonitorModel) Traffic() []ui.TrafficPoint {
	return append([]ui.TrafficPoint(nil), m.traffic...)
}

func (m MonitorModel) Snapshot() ui.MonitorSnapshot {
	return ui.MonitorSnapshot{
		Traffic: m.Traffic(), Memory: append([]ui.MemoryPoint(nil), m.memory...),
		Connections: m.connections, MemoryInUse: m.memoryInUse,
		UploadTotal: m.uploadTotal, DownloadTotal: m.downloadTotal,
		UploadRate: m.uploadRate, DownloadRate: m.downloadRate, Stale: m.stale,
	}
}

func (m MonitorModel) ViewFull(width, height int) string {
	width = max(1, width)
	if height < 12 {
		return clampLines(m.ViewNumbers(width), width)
	}
	// "UL " / "DL " prefix plus sparkline must fit inside the fixed rail column.
	chartWidth := max(1, width-len(ui.MonitorUploadShort)-1)
	upload := make([]int64, len(m.traffic))
	download := make([]int64, len(m.traffic))
	for index, point := range m.traffic {
		upload[index], download[index] = point.Up, point.Down
	}
	memory := make([]int64, len(m.memory))
	for index, point := range m.memory {
		memory[index] = point.InUse
	}
	return clampLines(strings.Join([]string{
		ui.MonitorTrafficTitle,
		ui.MonitorUploadShort + " " + ui.Sparkline(upload, chartWidth),
		ui.MonitorDownloadShort + " " + ui.Sparkline(download, chartWidth),
		ui.MonitorMemoryTitle,
		ui.Sparkline(memory, width),
		m.ViewNumbers(width),
	}, "\n"), width)
}

func (m MonitorModel) ViewNumbers(width int) string {
	state := ""
	if m.stale {
		state = " · " + ui.StaleLabel
	}
	// Prefer stacked rates/totals so large IEC values still fit a 18–24 column rail.
	lines := []string{
		fmt.Sprintf("%s %d%s", ui.MonitorConnectionsLabel, m.connections, state),
		fmt.Sprintf("%s %s", ui.MonitorMemoryLabel, ui.FormatBytes(m.memoryInUse)),
		fmt.Sprintf("%s %s", ui.MonitorUploadShort, ui.FormatRate(m.uploadRate)),
		fmt.Sprintf("%s %s", ui.MonitorDownloadShort, ui.FormatRate(m.downloadRate)),
		fmt.Sprintf("%s %s", ui.MonitorUploadTotal, ui.FormatBytes(m.uploadTotal)),
		fmt.Sprintf("%s %s", ui.MonitorDownloadTotal, ui.FormatBytes(m.downloadTotal)),
	}
	if width <= 0 {
		return strings.Join(lines, "\n")
	}
	return clampLines(strings.Join(lines, "\n"), width)
}

func clampLines(view string, width int) string {
	if width <= 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for index, line := range lines {
		if lipgloss.Width(line) > width {
			lines[index] = lipgloss.NewStyle().MaxWidth(width).Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func (m MonitorModel) ViewSummary(_ int) string {
	state := ""
	if m.stale {
		state = ui.StaleLabel + " · "
	}
	return fmt.Sprintf("%s%s %d  %s %s  %s %s  %s %s",
		state, ui.MonitorConnectionsLabel, m.connections,
		ui.MonitorUploadShort, ui.FormatRate(m.uploadRate),
		ui.MonitorDownloadShort, ui.FormatRate(m.downloadRate),
		ui.MonitorMemoryShort, ui.FormatBytes(m.memoryInUse),
	)
}

func appendBounded[T any](values []T, value T) []T {
	if len(values) == monitorSampleCapacity {
		copy(values, values[1:])
		values[len(values)-1] = value
		return values
	}
	return append(values, value)
}
