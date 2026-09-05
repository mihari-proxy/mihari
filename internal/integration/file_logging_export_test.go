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
	requests := [2]chan chan int64{make(chan chan int64), make(chan chan int64)}
	var group sync.WaitGroup
	for id, runtime := range writers {
		id, runtime := id, runtime
		group.Add(1)
		go func() {
			defer group.Done()
			var seq int64
			for {
				select {
				case <-ctx.Done():
					return
				case reply := <-requests[id]:
					seq++
					runtime.Logger().Info("concurrent export record", "writer", id, "seq", seq, "padding", strings.Repeat("p", 256))
					counts[id].Store(seq)
					reply <- seq
				}
			}
		}()
	}
	t.Cleanup(func() {
		cancel()
		group.Wait()
	})

	writeRounds(t, requests, 100)
	before := [2]int64{counts[0].Load(), counts[1].Load()}
	exportCtx, cancelExport := context.WithCancel(context.Background())
	tuiSnapshotStarting := make(chan struct{})
	allowSnapshotStart := make(chan struct{})
	tuiSnapshotFixed := make(chan struct{})
	postSnapshotReached := make(chan struct{})
	allowPostSnapshot := make(chan struct{})
	var snapshotCeiling [2]int64
	exportDone := make(chan struct{})
	var result logging.ExportResult
	var exportErr error
	go func() {
		defer close(exportDone)
		result, exportErr = logging.Export(exportCtx, logging.ExportRequest{
			Now: time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local), Range: logging.ExportRange{Kind: logging.RangeAll}, AutoNumber: true,
			Paths:     logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog},
			PrivateFS: fs, Redactor: logging.NewRedactor(), EnterRecordMutex: func(basePath string) func() {
				if basePath != paths.TUILog {
					return func() {}
				}
				close(tuiSnapshotStarting)
				select {
				case <-allowSnapshotStart:
				case <-exportCtx.Done():
					return func() {}
				}
				return writers[0].EnterRecordMutex()
			},
			OpenLock: func(lockFS *platform.PrivateFS, basePath string) (platform.AdvisoryLock, error) {
				lock, err := platform.OpenAdvisoryLock(lockFS, basePath)
				if err != nil {
					return lock, err
				}
				switch basePath {
				case paths.TUILog + ".lock":
					return &exportSnapshotBoundaryLock{
						AdvisoryLock: lock, fixed: tuiSnapshotFixed, counts: &counts, ceiling: &snapshotCeiling,
					}, nil
				case paths.MihomoLog + ".lock":
					return &exportLockBarrier{AdvisoryLock: lock, reached: postSnapshotReached, allow: allowPostSnapshot}, nil
				default:
					return lock, nil
				}
			},
		})
	}()
	t.Cleanup(func() {
		cancelExport()
		<-exportDone
	})
	var releaseStart, releasePost sync.Once
	releaseSnapshotStart := func() { releaseStart.Do(func() { close(allowSnapshotStart) }) }
	releasePostSnapshot := func() { releasePost.Do(func() { close(allowPostSnapshot) }) }
	releaseAllBarriers := func() {
		releaseSnapshotStart()
		releasePostSnapshot()
	}
	t.Cleanup(releaseAllBarriers)

	select {
	case <-tuiSnapshotStarting:
	case <-time.After(10 * time.Second):
		t.Fatal("exporter did not reach the deterministic TUI snapshot-start barrier")
	}
	// Forty rounds are over 16 KiB across the two writers, so real rotation is
	// unavoidable while Export is waiting to begin the TUI snapshot.
	writeRounds(t, requests, 40)
	releaseSnapshotStart()
	select {
	case <-tuiSnapshotFixed:
	case <-time.After(10 * time.Second):
		t.Fatal("exporter did not fix the TUI handle sizes")
	}
	select {
	case <-postSnapshotReached:
	case <-time.After(10 * time.Second):
		t.Fatal("exporter did not reach the post-TUI-snapshot barrier")
	}
	// Reaching the next source proves snapshotSource returned and released both
	// the real advisory lock and the first writer's record mutex. These appends
	// are therefore strictly outside the fixed TUI handles.
	writeRounds(t, requests, 5)
	releasePostSnapshot()

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
	assertExportedTUILines(t, result.Path, before, snapshotCeiling)
}

func writeRounds(t *testing.T, requests [2]chan chan int64, rounds int) {
	t.Helper()
	for range rounds {
		replies := [2]chan int64{make(chan int64, 1), make(chan int64, 1)}
		for writer := range requests {
			select {
			case requests[writer] <- replies[writer]:
			case <-time.After(10 * time.Second):
				t.Fatalf("writer %d did not accept a bounded write request", writer)
			}
		}
		for writer := range replies {
			select {
			case <-replies[writer]:
			case <-time.After(10 * time.Second):
				t.Fatalf("writer %d did not acknowledge a bounded write request", writer)
			}
		}
	}
}

type exportSnapshotBoundaryLock struct {
	platform.AdvisoryLock
	fixed   chan<- struct{}
	counts  *[2]atomic.Int64
	ceiling *[2]int64
}

func (l *exportSnapshotBoundaryLock) Unlock() error {
	for writer := range l.counts {
		l.ceiling[writer] = l.counts[writer].Load()
	}
	err := l.AdvisoryLock.Unlock()
	close(l.fixed)
	return err
}

type exportLockBarrier struct {
	platform.AdvisoryLock
	reached chan<- struct{}
	allow   <-chan struct{}
	once    sync.Once
}

func (l *exportLockBarrier) Lock(ctx context.Context, mode platform.LockMode) error {
	l.once.Do(func() { close(l.reached) })
	select {
	case <-l.allow:
		return l.AdvisoryLock.Lock(ctx, mode)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func assertExportedTUILines(t *testing.T, archivePath string, before, ceiling [2]int64) {
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
	postBefore := [2]bool{}
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
			if record.Writer < 0 || record.Writer >= len(postBefore) {
				_ = entry.Close()
				t.Fatalf("exported unexpected writer id %d", record.Writer)
			}
			if record.Seq > ceiling[record.Writer] {
				_ = entry.Close()
				t.Fatalf("exported writer %d post-snapshot seq=%d above fixed ceiling=%d", record.Writer, record.Seq, ceiling[record.Writer])
			}
			if record.Seq > before[record.Writer] {
				postBefore[record.Writer] = true
			}
		}
		scanErr := scanner.Err()
		closeErr := entry.Close()
		if scanErr != nil || closeErr != nil {
			t.Fatal(fmt.Errorf("read exported TUI entry: %w", errors.Join(scanErr, closeErr)))
		}
	}
	for writer, included := range postBefore {
		if !included {
			t.Fatalf("archive omitted writer %d records produced after pre-snapshot high-water mark %d", writer, before[writer])
		}
	}
}
