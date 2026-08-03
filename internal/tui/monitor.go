package tui

import (
	"fmt"
	"strings"

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
	if height < 12 {
		return m.ViewNumbers(width)
	}
	chartWidth := max(1, width-4)
	upload := make([]int64, len(m.traffic))
	download := make([]int64, len(m.traffic))
	for index, point := range m.traffic {
		upload[index], download[index] = point.Up, point.Down
	}
	memory := make([]int64, len(m.memory))
	for index, point := range m.memory {
		memory[index] = point.InUse
	}
	return strings.Join([]string{
		ui.MonitorTrafficTitle,
		ui.MonitorUploadShort + " " + ui.Sparkline(upload, chartWidth),
		ui.MonitorDownloadShort + " " + ui.Sparkline(download, chartWidth),
		ui.MonitorMemoryTitle,
		"  " + ui.Sparkline(memory, chartWidth),
		m.ViewNumbers(width),
	}, "\n")
}

func (m MonitorModel) ViewNumbers(_ int) string {
	state := ""
	if m.stale {
		state = " · " + ui.StaleLabel
	}
	return fmt.Sprintf("%s %d%s\n%s %s\n%s %s  %s %s\n%s %s  %s %s",
		ui.MonitorConnectionsLabel, m.connections, state,
		ui.MonitorMemoryLabel, formatIEC(m.memoryInUse, false),
		ui.MonitorUploadShort, formatIEC(m.uploadRate, true), ui.MonitorDownloadShort, formatIEC(m.downloadRate, true),
		ui.MonitorUploadTotal, formatIEC(m.uploadTotal, false), ui.MonitorDownloadTotal, formatIEC(m.downloadTotal, false),
	)
}

func (m MonitorModel) ViewSummary(_ int) string {
	state := ""
	if m.stale {
		state = ui.StaleLabel + " · "
	}
	return fmt.Sprintf("%s%s %d  %s %s  %s %s  %s %s",
		state, ui.MonitorConnectionsLabel, m.connections,
		ui.MonitorUploadShort, formatIEC(m.uploadRate, true),
		ui.MonitorDownloadShort, formatIEC(m.downloadRate, true),
		ui.MonitorMemoryShort, formatIEC(m.memoryInUse, false),
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

func formatIEC(value int64, rate bool) string {
	if value < 0 {
		value = 0
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	suffix := units[unit]
	if rate {
		suffix += "/s"
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, suffix)
	}
	return fmt.Sprintf("%.1f %s", amount, suffix)
}
