package integration

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestFileLoggingExport_TwoTUIWritersRotateWhileExporterSnapshots(t *testing.T) {
	root := t.TempDir()
	paths := platform.NewPaths(root)
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}

	// Seed another real source so the exporter reaches its first source lock.
	daemon, err := fs.OpenAppend(paths.DaemonLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Close() })
	daemonLine := []byte(`{"time":"2026-09-05T00:00:00Z","level":"INFO","component":"daemon","msg":"seed"}` + "\n")
	for range 16 {
		if _, err := daemon.Write(daemonLine); err != nil {
			t.Fatal(err)
		}
	}
	if err := daemon.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := logging.Config{Level: slog.LevelDebug, MaxSizeBytes: 8 << 10, MaxFiles: 10}
	writers := make([]*logging.Runtime, 2)
	for i := range writers {
		writers[i], err = logging.Open(context.Background(), logging.RuntimeOptions{
			BasePath: paths.TUILog, Component: "tui", Config: cfg, PrivateFS: fs, Redactor: logging.NewRedactor(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var closeWriters sync.Once
	closeAllWriters := func() {
		closeWriters.Do(func() {
			for _, writer := range writers {
				if err := writer.Close(); err != nil {
					t.Errorf("close TUI writer: %v", err)
				}
			}
		})
	}
	t.Cleanup(closeAllWriters)

	ctx, cancel := context.WithCancel(context.Background())
	var counts [2]atomic.Int64
	var group sync.WaitGroup
	for id, runtime := range writers {
		id, runtime := id, runtime
		group.Add(1)
		go func() {
			defer group.Done()
			for seq := int64(1); ; seq++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				runtime.Logger().Info("concurrent export record", "writer", id, "seq", seq, "padding", strings.Repeat("p", 256))
				counts[id].Store(seq)
			}
		}()
	}
	t.Cleanup(func() {
		cancel()
		group.Wait()
	})

	seedDeadline := time.After(10 * time.Second)
	for counts[0].Load() < 100 || counts[1].Load() < 100 {
		select {
		case <-seedDeadline:
			t.Fatal("timed out seeding both TUI writers")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	before := [2]int64{counts[0].Load(), counts[1].Load()}
	exportCtx, cancelExport := context.WithCancel(context.Background())
	exportAtBarrier := make(chan struct{})
	allowExport := make(chan struct{})
	exportDone := make(chan struct{})
	var result logging.ExportResult
	var exportErr error
	go func() {
		defer close(exportDone)
		result, exportErr = logging.Export(exportCtx, logging.ExportRequest{
			Now: time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local), Range: logging.ExportRange{Kind: logging.RangeAll}, AutoNumber: true,
			Paths:     logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog},
			PrivateFS: fs, Redactor: logging.NewRedactor(), EnterRecordMutex: func(string) func() { return writers[0].EnterRecordMutex() },
			OpenLock: func(lockFS *platform.PrivateFS, basePath string) (platform.AdvisoryLock, error) {
				lock, err := platform.OpenAdvisoryLock(lockFS, basePath)
				if err != nil || basePath != paths.DaemonLog+".lock" {
					return lock, err
				}
				return &exportBarrierLock{AdvisoryLock: lock, reached: exportAtBarrier, allow: allowExport}, nil
			},
		})
	}()
	t.Cleanup(func() {
		cancelExport()
		<-exportDone
	})

	select {
	case <-exportAtBarrier:
	case <-time.After(10 * time.Second):
		t.Fatal("exporter did not reach the deterministic source-lock barrier")
	}
	deadline := time.After(10 * time.Second)
	for {
		// Each encoded record is over 256 bytes and MaxSizeBytes is 8 KiB.
		// While the real exporter is held at the barrier, both writers advance
		// and their combined bytes necessarily cross a rotation boundary.
		firstDelta := counts[0].Load() - before[0]
		secondDelta := counts[1].Load() - before[1]
		advanced := firstDelta > 0 && secondDelta > 0 && firstDelta+secondDelta > 40
		if advanced {
			select {
			case <-exportDone:
				t.Fatal("export completed before both real TUI writers rotated during it")
			default:
			}
			break
		}
		select {
		case <-exportDone:
			t.Fatal("export completed before concurrent rotation was observed")
		case <-deadline:
			t.Fatal("timed out waiting for both TUI writers to rotate during export")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(allowExport)

	<-exportDone
	cancel()
	group.Wait()
	closeAllWriters()
	if exportErr != nil {
		t.Fatal(exportErr)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	rotated := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, "mihari-tui.log.") {
			rotated = true
			break
		}
	}
	if !rotated {
		t.Fatal("two TUI writers advanced across rotation thresholds but no real archive remained")
	}
	assertExportedTUILines(t, result.Path)
}

type exportBarrierLock struct {
	platform.AdvisoryLock
	reached chan<- struct{}
	allow   <-chan struct{}
	once    sync.Once
}

func (l *exportBarrierLock) Lock(ctx context.Context, mode platform.LockMode) error {
	l.once.Do(func() { close(l.reached) })
	select {
	case <-l.allow:
		return l.AdvisoryLock.Lock(ctx, mode)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func assertExportedTUILines(t *testing.T, archivePath string) {
	t.Helper()
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := archive.Close(); err != nil {
			t.Errorf("close export archive: %v", err)
		}
	})
	seen := make(map[string]struct{})
	lines := 0
	for _, item := range archive.File {
		if item.Name != "tui/mihari-tui.log" {
			continue
		}
		entry, err := item.Open()
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(entry)
		for scanner.Scan() {
			var record struct {
				Writer int   `json:"writer"`
				Seq    int64 `json:"seq"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				_ = entry.Close()
				t.Fatalf("exported a partial or invalid JSONL line %q: %v", scanner.Bytes(), err)
			}
			key := fmt.Sprintf("%d/%d", record.Writer, record.Seq)
			if _, duplicate := seen[key]; duplicate {
				_ = entry.Close()
				t.Fatalf("exported duplicate TUI record %s", key)
			}
			seen[key] = struct{}{}
			lines++
		}
		scanErr := scanner.Err()
		closeErr := entry.Close()
		if scanErr != nil || closeErr != nil {
			t.Fatal(fmt.Errorf("read exported TUI entry: %w", errors.Join(scanErr, closeErr)))
		}
	}
	if lines == 0 {
		t.Fatal("archive omitted all concurrent TUI records")
	}
}
