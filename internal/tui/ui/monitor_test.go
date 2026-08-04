package ui

import (
	"strings"
	"testing"
)

func TestSparkline_FlatNonZeroUsesMidBarNotSolidBlock(t *testing.T) {
	// Stable memory samples used to render as a solid █ bar, which looked broken.
	line := Sparkline([]int64{28_000_000, 28_000_000, 28_000_000, 28_000_000}, 8)
	if strings.Contains(line, "█") {
		t.Fatalf("flat series rendered solid block: %q", line)
	}
	if !strings.ContainsAny(line, "▃▄▅") {
		t.Fatalf("flat series missing mid bar: %q", line)
	}
}

func TestSparkline_ZeroSeriesIsFloor(t *testing.T) {
	line := Sparkline([]int64{0, 0, 0}, 4)
	if line != "▁▁▁▁" {
		t.Fatalf("zero series=%q", line)
	}
}

func TestSparkline_RangeUsesRelativeLevels(t *testing.T) {
	line := Sparkline([]int64{1, 100}, 2)
	if !strings.HasPrefix(line, "▁") || !strings.HasSuffix(line, "█") {
		t.Fatalf("range series=%q", line)
	}
}

func TestFormatBytesUsesIECUnits(t *testing.T) {
	if got := FormatBytes(0); got != "0 B" {
		t.Fatalf("0=%q", got)
	}
	if got := FormatBytes(1024); got != "1.0 KiB" {
		t.Fatalf("1KiB=%q", got)
	}
	if got := FormatBytes(28_356 * 1024); got != "27.7 MiB" {
		t.Fatalf("MiB=%q", got)
	}
	if got := FormatRate(2048); got != "2.0 KiB/s" {
		t.Fatalf("rate=%q", got)
	}
}
