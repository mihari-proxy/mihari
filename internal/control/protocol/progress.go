package protocol

import (
	"fmt"
	"time"
)

const (
	ProgressPhaseDownloading = "downloading"
	ProgressPhaseExtracting  = "extracting"
	ProgressPhaseChecking    = "checking"
)

// OperationProgress is the optional download observation on an in-flight operation.
// Byte fields are present only while the release asset is being copied.
type OperationProgress struct {
	Phase               string `json:"phase"`
	ReceivedBytes       *int64 `json:"received_bytes,omitempty"`
	TotalBytes          *int64 `json:"total_bytes,omitempty"`
	ElapsedMilliseconds int64  `json:"elapsed_ms"`
}

// FormatCoreInstallProgress renders the setup, system, and CLI progress line.
// A nil progress uses the install request's own elapsed time.
func FormatCoreInstallProgress(progress *OperationProgress, requestElapsed time.Duration) string {
	if progress == nil {
		return "Installing mihomo core  " + formatClock(requestElapsed)
	}
	elapsed := time.Duration(progress.ElapsedMilliseconds) * time.Millisecond
	switch progress.Phase {
	case ProgressPhaseDownloading:
		received := formatByteSize(progressValue(progress.ReceivedBytes))
		clock := formatClock(elapsed)
		if progress.TotalBytes == nil {
			return "Downloading mihomo core  " + received + "  " + clock
		}
		return "Downloading mihomo core  " + received + " / " + formatByteSize(*progress.TotalBytes) + "  " + clock
	case ProgressPhaseExtracting:
		return "Extracting mihomo core  " + formatClock(elapsed)
	case ProgressPhaseChecking:
		return "Checking mihomo core  " + formatClock(elapsed)
	default:
		return "Installing mihomo core  " + formatClock(requestElapsed)
	}
}

func progressValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func formatClock(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("%02d:%02d", int(elapsed/time.Minute), int((elapsed/time.Second)%60))
}

func formatByteSize(n int64) string {
	if n < 0 {
		n = 0
	}
	const kib = 1024
	const mib = 1024 * 1024
	if n < kib {
		return fmt.Sprintf("%d B", n)
	}
	if n < mib {
		return fmt.Sprintf("%.1f KiB", float64(n)/kib)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/mib)
}
