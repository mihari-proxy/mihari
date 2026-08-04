package ui

import (
	"fmt"
	"strings"
	"time"
)

type TrafficPoint struct {
	Up   int64
	Down int64
}

type MemoryPoint struct {
	InUse int64
}

type MonitorSnapshot struct {
	Traffic       []TrafficPoint
	Memory        []MemoryPoint
	Connections   int
	MemoryInUse   int64
	UploadTotal   int64
	DownloadTotal int64
	UploadRate    int64
	DownloadRate  int64
	Stale         bool
}

type OperationRecord struct {
	ID     string
	Object string
	State  string
	At     time.Time
}

func Sparkline(values []int64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	if len(values) == 0 {
		return strings.Repeat("▁", width)
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values {
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var builder strings.Builder
	for range width - len(values) {
		builder.WriteRune(levels[0])
	}
	for _, value := range values {
		level := 0
		switch {
		case value <= 0:
			level = 0
		case maximum == minimum:
			// Flat non-zero series: mid bar, not a solid block of █.
			level = len(levels) / 2
		default:
			level = int((value - minimum) * int64(len(levels)-1) / (maximum - minimum))
		}
		builder.WriteRune(levels[level])
	}
	return builder.String()
}

// FormatBytes renders a non-negative byte count with IEC units.
func FormatBytes(value int64) string {
	return formatIEC(value, false)
}

// FormatRate renders a non-negative byte rate with IEC units and /s.
func FormatRate(value int64) string {
	return formatIEC(value, true)
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
