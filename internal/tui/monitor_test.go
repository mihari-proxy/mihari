package tui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestMonitor_KeepsLatestSixtySamples(t *testing.T) {
	monitor := NewMonitor()
	for index := int64(0); index < 75; index++ {
		monitor.ObserveTraffic(index, index*2)
	}
	if got := len(monitor.Traffic()); got != 60 {
		t.Fatalf("len=%d", got)
	}
	if got := monitor.Traffic()[0].Up; got != 15 {
		t.Fatalf("first=%d", got)
	}
}

func TestMonitor_StaleSummaryKeepsLastValues(t *testing.T) {
	monitor := NewMonitor()
	monitor.ObserveTraffic(1024, 2048)
	monitor.SetStale(true)
	view := monitor.ViewSummary(80)
	for _, want := range []string{"Stale", "1.0 KiB/s", "2.0 KiB/s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q: %s", want, view)
		}
	}
}

func TestMonitor_LowHeightHidesChartsBeforeNumbers(t *testing.T) {
	monitor := NewMonitor()
	monitor.ObserveTraffic(1024, 2048)
	view := monitor.ViewFull(24, 7)
	if strings.ContainsAny(view, "▁▂▃▄▅▆▇█") || !strings.Contains(view, "UL") {
		t.Fatalf("view=%s", view)
	}
}

func TestMonitor_ViewFullNeverExceedsRailWidth(t *testing.T) {
	monitor := NewMonitor()
	monitor.ObserveTraffic(12_345_678, 98_765_432)
	monitor.ObserveMemory(256 * 1024 * 1024)
	monitor.ObserveConnections(protocol.ConnectionList{
		UploadTotal: 1_024 * 1024 * 1024, DownloadTotal: 4_096 * 1024 * 1024,
	})
	const railWidth = 24
	view := monitor.ViewFull(railWidth, 20)
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > railWidth {
			t.Fatalf("line width %d > %d: %q", width, railWidth, line)
		}
	}
}

func TestMonitor_UploadedAndDownloadedAreSeparateLinesWithDynamicUnits(t *testing.T) {
	monitor := NewMonitor()
	monitor.ObserveConnections(protocol.ConnectionList{
		UploadTotal:   6 * 1024,                // 6.0 KiB
		DownloadTotal: 27*1024*1024 + 700*1024, // ~27.7 MiB
		Connections:   nil,
	})
	view := monitor.ViewNumbers(24)
	lines := strings.Split(view, "\n")
	var uploadedLine, downloadedLine string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, ui.MonitorUploadTotal):
			uploadedLine = line
		case strings.HasPrefix(line, ui.MonitorDownloadTotal):
			downloadedLine = line
		}
	}
	if uploadedLine == "" || downloadedLine == "" {
		t.Fatalf("missing total lines in view:\n%s", view)
	}
	if strings.Contains(uploadedLine, ui.MonitorDownloadTotal) || strings.Contains(downloadedLine, ui.MonitorUploadTotal) {
		t.Fatalf("totals shared a line:\n%s", view)
	}
	if !strings.Contains(uploadedLine, "6.0 KiB") {
		t.Fatalf("upload unit not dynamic: %q", uploadedLine)
	}
	if !strings.Contains(downloadedLine, "MiB") {
		t.Fatalf("download unit not dynamic: %q", downloadedLine)
	}
	// Confirm they are not rendered as a single combined line anywhere.
	if strings.Contains(view, ui.MonitorUploadTotal+" ") && strings.Contains(view, "  "+ui.MonitorDownloadTotal) {
		t.Fatalf("still using side-by-side totals: %s", view)
	}
}
