package protocol

import (
	"testing"
	"time"
)

func TestFormatCoreInstallProgress_BeforeBytesUsesRequestClock(t *testing.T) {
	got := FormatCoreInstallProgress(nil, 4*time.Second)
	if got != "Installing mihomo core  00:04" {
		t.Fatalf("progress line = %q", got)
	}
}

func TestFormatCoreInstallProgress_DownloadShowsBytesAndOptionalTotal(t *testing.T) {
	received := int64(1536)
	total := int64(2048)
	withTotal := FormatCoreInstallProgress(&OperationProgress{
		Phase: ProgressPhaseDownloading, ReceivedBytes: &received, TotalBytes: &total, ElapsedMilliseconds: 1000,
	}, 99*time.Second)
	if withTotal != "Downloading mihomo core  1.5 KiB / 2.0 KiB  00:01" {
		t.Fatalf("sized download = %q", withTotal)
	}
	unknown := FormatCoreInstallProgress(&OperationProgress{
		Phase: ProgressPhaseDownloading, ReceivedBytes: &received, ElapsedMilliseconds: 18000,
	}, 0)
	if unknown != "Downloading mihomo core  1.5 KiB  00:18" {
		t.Fatalf("unsized download = %q", unknown)
	}
}

func TestFormatCoreInstallProgress_LaterPhasesDropBytes(t *testing.T) {
	received := int64(99)
	extracting := FormatCoreInstallProgress(&OperationProgress{
		Phase: ProgressPhaseExtracting, ReceivedBytes: &received, ElapsedMilliseconds: 1500,
	}, 0)
	if extracting != "Extracting mihomo core  00:01" {
		t.Fatalf("extract = %q", extracting)
	}
	checking := FormatCoreInstallProgress(&OperationProgress{
		Phase: ProgressPhaseChecking, ElapsedMilliseconds: 12000,
	}, 0)
	if checking != "Checking mihomo core  00:12" {
		t.Fatalf("check = %q", checking)
	}
}

func TestFormatByteSize_UsesBinaryUnits(t *testing.T) {
	if got := formatByteSize(512); got != "512 B" {
		t.Fatalf("bytes = %q", got)
	}
	if got := formatByteSize(0); got != "0 B" {
		t.Fatalf("zero = %q", got)
	}
	if got := formatByteSize(13 * 1024 * 1024); got != "13.0 MiB" {
		t.Fatalf("mebibytes = %q", got)
	}
}
