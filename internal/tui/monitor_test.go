package tui

import (
	"strings"
	"testing"
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
