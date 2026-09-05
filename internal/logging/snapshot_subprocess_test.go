package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

const snapshotLockChildEnv = "MIHARI_SNAPSHOT_LOCK_CHILD"
const snapshotWriterChildEnv = "MIHARI_SNAPSHOT_WRITER_CHILD"

func TestSnapshotSource_SubprocessWriterRotatesWhileRepeatedSnapshotsStayBounded(t *testing.T) {
	for _, maxFiles := range []int{3, 1} {
		t.Run(fmt.Sprintf("max-files-%d", maxFiles), func(t *testing.T) {
			fs, paths := openExportTestFS(t)
			child := startSnapshotWriterChild(t, filepath.Dir(paths.LogDir), paths.TUILog, maxFiles)
			t.Cleanup(func() { child.stop(t) })

			lastWritten := 0
			for iteration := 0; iteration < 8; iteration++ {
				lastWritten = child.write(t, 4)
				handles, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil, nil)
				if err != nil {
					t.Fatalf("snapshot %d: %v", iteration, err)
				}
				if len(handles) == 0 {
					t.Fatalf("snapshot %d returned no handles", iteration)
				}
				frozenMax := lastWritten
				lastWritten = child.write(t, 8)
				assertFrozenJSONLSnapshot(t, iteration, handles, frozenMax)
				if err := closeSnapshots(handles); err != nil {
					t.Fatalf("close snapshot %d: %v", iteration, err)
				}
			}
		})
	}
}

func assertFrozenJSONLSnapshot(t *testing.T, iteration int, handles []snapshotHandle, frozenMax int) {
	t.Helper()
	seenNames := make(map[string]struct{}, len(handles))
	seenIDs := make(map[int]struct{})
	previousID := 0
	for _, handle := range handles {
		if _, exists := seenNames[handle.name]; exists {
			t.Fatalf("snapshot %d duplicate handle %q", iteration, handle.name)
		}
		seenNames[handle.name] = struct{}{}
		body, err := io.ReadAll(io.LimitReader(handle.file, handle.size+1))
		if err != nil {
			t.Fatalf("snapshot %d read %q after rotation: %v", iteration, handle.name, err)
		}
		if int64(len(body)) != handle.size {
			t.Fatalf("snapshot %d %q read=%d frozen size=%d", iteration, handle.name, len(body), handle.size)
		}
		if len(body) > 0 && body[len(body)-1] != '\n' {
			t.Fatalf("snapshot %d %q ends in truncated JSONL record", iteration, handle.name)
		}
		for _, line := range bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var record struct {
				ID int `json:"id"`
			}
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatalf("snapshot %d %q invalid JSONL %q: %v", iteration, handle.name, line, err)
			}
			if record.ID <= previousID {
				t.Fatalf("snapshot %d IDs out of physical order: previous=%d current=%d", iteration, previousID, record.ID)
			}
			if record.ID > frozenMax {
				t.Fatalf("snapshot %d captured post-snapshot append id=%d > %d", iteration, record.ID, frozenMax)
			}
			if _, duplicate := seenIDs[record.ID]; duplicate {
				t.Fatalf("snapshot %d duplicate record id=%d", iteration, record.ID)
			}
			seenIDs[record.ID] = struct{}{}
			previousID = record.ID
		}
	}
}

type snapshotWriterChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

func startSnapshotWriterChild(t *testing.T, root, base string, maxFiles int) *snapshotWriterChild {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotWriterChild$")
	cmd.Env = append(os.Environ(), snapshotWriterChildEnv+"=1", "MIHARI_SNAPSHOT_ROOT="+root, "MIHARI_SNAPSHOT_BASE="+base, fmt.Sprintf("MIHARI_SNAPSHOT_MAX_FILES=%d", maxFiles))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := &snapshotWriterChild{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout)}
	if !child.stdout.Scan() || child.stdout.Text() != "ready" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("writer child did not become ready: line=%q scanErr=%v stderr=%s", child.stdout.Text(), child.stdout.Err(), stderr.String())
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return child
}

func (c *snapshotWriterChild) write(t *testing.T, count int) int {
	t.Helper()
	if _, err := fmt.Fprintf(c.stdin, "write %d\n", count); err != nil {
		t.Fatal(err)
	}
	if !c.stdout.Scan() {
		t.Fatalf("writer child stopped: %v", c.stdout.Err())
	}
	fields := strings.Fields(c.stdout.Text())
	if len(fields) != 2 || fields[0] != "ack" {
		t.Fatalf("writer child response=%q", c.stdout.Text())
	}
	last, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("writer child ack=%q: %v", c.stdout.Text(), err)
	}
	return last
}

func (c *snapshotWriterChild) stop(t *testing.T) {
	t.Helper()
	if c.cmd.ProcessState != nil {
		return
	}
	if _, err := io.WriteString(c.stdin, "stop\n"); err != nil {
		t.Error(err)
	}
	_ = c.stdin.Close()
	if err := c.cmd.Wait(); err != nil {
		t.Errorf("writer child exit: %v", err)
	}
}

func TestSnapshotWriterChild(t *testing.T) {
	if os.Getenv(snapshotWriterChildEnv) != "1" {
		t.Skip("subprocess helper")
	}
	fs, err := platform.NewPrivateFS(filepath.Clean(os.Getenv("MIHARI_SNAPSHOT_ROOT")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fs.Close() }()
	if err := fs.EnsureDir(filepath.Dir(os.Getenv("MIHARI_SNAPSHOT_BASE"))); err != nil {
		t.Fatal(err)
	}
	maxFiles, err := strconv.Atoi(os.Getenv("MIHARI_SNAPSHOT_MAX_FILES"))
	if err != nil || maxFiles < 1 {
		t.Fatalf("invalid max files: %q", os.Getenv("MIHARI_SNAPSHOT_MAX_FILES"))
	}
	runtime, err := Open(context.Background(), RuntimeOptions{
		BasePath: os.Getenv("MIHARI_SNAPSHOT_BASE"), Component: "snapshot-writer", PrivateFS: fs,
		Config: Config{Level: slog.LevelInfo, MaxSizeBytes: 320, MaxFiles: maxFiles},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	lastID := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 1 && fields[0] == "stop" {
			return
		}
		if len(fields) != 2 || fields[0] != "write" {
			t.Fatalf("invalid command %q", scanner.Text())
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil || count < 1 {
			t.Fatalf("invalid write count %q", fields[1])
		}
		for range count {
			lastID++
			runtime.Logger().Info("snapshot rotation record", "id", lastID, "padding", "0123456789abcdef0123456789abcdef")
		}
		fmt.Printf("ack %d\n", lastID)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotSource_SubprocessExclusiveCannotPassSharedSnapshot(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.TUILog, "{\"record\":1}\n")
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		handles, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil,
			func(fs *platform.PrivateFS, path string, identity platform.FileIdentity) (*os.File, error) {
				close(entered)
				<-release
				return platform.OpenSnapshot(fs, path, identity)
			})
		if err == nil {
			err = closeSnapshots(handles)
		}
		done <- err
	}()
	<-entered

	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotLockChild$")
	cmd.Env = append(os.Environ(), snapshotLockChildEnv+"=1", "MIHARI_SNAPSHOT_ROOT="+filepath.Dir(paths.LogDir), "MIHARI_SNAPSHOT_BASE="+paths.TUILog)
	output, err := cmd.CombinedOutput()
	if err != nil {
		close(release)
		t.Fatalf("exclusive child: %v\n%s", err, output)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("snapshotSource: %v", err)
	}
}

func TestSnapshotLockChild(t *testing.T) {
	if os.Getenv(snapshotLockChildEnv) != "1" {
		t.Skip("subprocess helper")
	}
	fs, err := platform.NewPrivateFS(filepath.Clean(os.Getenv("MIHARI_SNAPSHOT_ROOT")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer func() { _ = fs.Close() }()
	lock, err := platform.OpenAdvisoryLock(fs, os.Getenv("MIHARI_SNAPSHOT_BASE")+".lock")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer func() { _ = lock.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := lock.Lock(ctx, platform.LockExclusive); err != context.DeadlineExceeded {
		fmt.Fprintf(os.Stderr, "exclusive lock error=%v, want deadline exceeded\n", err)
		os.Exit(2)
	}
}
