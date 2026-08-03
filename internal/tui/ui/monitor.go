package ui

import (
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
	maximum := int64(0)
	for _, value := range values {
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
		if maximum > 0 && value > 0 {
			level = int(value * int64(len(levels)-1) / maximum)
		}
		builder.WriteRune(levels[level])
	}
	return builder.String()
}
