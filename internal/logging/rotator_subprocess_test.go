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

const rotatorChildEnv = "MIHARI_ROTATOR_CHILD"

func init() {
	if os.Getenv(rotatorChildEnv) == "" {
		return
	}
	os.Exit(runRotatorChild())
}

type rotatorRecord struct {
	Writer string `json:"writer"`
	Seq    int    `json:"seq"`
}

func TestRotatingWriter_TwoProcessesNoTornRecords(t *testing.T) {
	fs, paths := openTestLogFS(t)
	base := paths.TUILog
	cfg := Config{Level: slog.LevelInfo, MaxSizeBytes: 512, MaxFiles: 10}

	a := startRotatorChild(t, paths.Root, base, "write", "A", cfg, 2000)
	b := startRotatorChild(t, paths.Root, base, "write", "B", cfg, 2000)
	waitChildExit(t, a)
	waitChildExit(t, b)

	lines := readAllJSONL(t, fs, paths.LogDir, filepath.Base(base))
	if len(lines) == 0 {
		t.Fatal("merged logs are empty")
	}
	seen := make(map[string]map[int]struct{})
	for i, line := range lines {
		var rec rotatorRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("line %d is not JSON: %s err=%v", i, line, err)
		}
		if rec.Writer == "" || rec.Seq < 1 {
			t.Fatalf("line %d missing writer/seq: %+v", i, rec)
		}
		if seen[rec.Writer] == nil {
			seen[rec.Writer] = make(map[int]struct{})
		}
		if _, ok := seen[rec.Writer][rec.Seq]; ok {
			t.Fatalf("duplicate %s seq=%d", rec.Writer, rec.Seq)
		}
		seen[rec.Writer][rec.Seq] = struct{}{}
	}
}

func TestRotatingWriter_OpenEnumeratesOnlyAfterLock(t *testing.T) {
	fs, paths := openTestLogFS(t)
	mustWriteFile(t, fs, paths.TUILog, []byte("ACTIVE\n"))
	for i := 1; i <= 2; i++ {
		mustWriteFile(t, fs, fmt.Sprintf("%s.%d", paths.TUILog, i), []byte(fmt.Sprintf("pre-%d\n", i)))
	}
	cfg := Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3}
	child := startRotatorChild(t, paths.Root, paths.TUILog, "open-pause", "P", cfg, 0)
	if !child.sc.Scan() || child.sc.Text() != "locked" {
		t.Fatalf("want locked, got %q stderr=%s", child.sc.Text(), child.errBuf.String())
	}

	late := paths.TUILog + ".99"
	mustWriteFile(t, fs, late, []byte("late-enum\n"))
	if _, err := io.WriteString(child.stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	if !child.sc.Scan() || child.sc.Text() != "opened" {
		t.Fatalf("want opened, got %q stderr=%s", child.sc.Text(), child.errBuf.String())
	}
	waitChildExit(t, child)
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Fatalf("Open used a listing captured before lock; late .99 survived: %v", err)
	}
	if got := readLogFile(t, paths.TUILog); string(got) != "ACTIVE\n" {
		t.Fatalf("Open rewrote active: %q", got)
	}
}

func TestRotatingWriter_NoStaleInodeWrite(t *testing.T) {
	fs, paths := openTestLogFS(t)
	rec1 := rotatorRecord{Writer: "child", Seq: 1}
	body1, err := json.Marshal(rec1)
	if err != nil {
		t.Fatal(err)
	}
	body1 = append(body1, '\n')
	cfg := Config{Level: slog.LevelInfo, MaxSizeBytes: int64(len(body1)), MaxFiles: 3}

	child := startRotatorChild(t, paths.Root, paths.TUILog, "stale", "child", cfg, 0)
	if !child.sc.Scan() || child.sc.Text() != "wrote1" {
		t.Fatalf("want wrote1, got %q stderr=%s", child.sc.Text(), child.errBuf.String())
	}

	parent := openRotatorAt(t, fs, paths.TUILog, cfg)
	recP := rotatorRecord{Writer: "parent", Seq: 1}
	bodyP, err := json.Marshal(recP)
	if err != nil {
		t.Fatal(err)
	}
	bodyP = append(bodyP, '\n')
	if _, err := parent.Write(bodyP); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(child.stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	if !child.sc.Scan() || child.sc.Text() != "wrote2" {
		t.Fatalf("want wrote2, got %q stderr=%s", child.sc.Text(), child.errBuf.String())
	}
	waitChildExit(t, child)

	files := collectLogFiles(t, fs, paths.LogDir, filepath.Base(paths.TUILog))
	marker1 := []byte(`"writer":"child","seq":1`)
	marker2 := []byte(`"writer":"child","seq":2`)
	for name, body := range files {
		if bytes.Contains(body, marker1) && bytes.Contains(body, marker2) {
			t.Fatalf("stale inode %s received both child records:\n%s", name, body)
		}
	}
	merged := bytes.Join(fileValues(files), nil)
	if !bytes.Contains(merged, marker2) {
		t.Fatalf("child seq 2 missing from merged logs: %q", merged)
	}
}

type rotatorChild struct {
	stdin  io.WriteCloser
	sc     *bufio.Scanner
	cmd    *exec.Cmd
	errBuf *bytes.Buffer
}

func startRotatorChild(t *testing.T, root, base, mode, writer string, cfg Config, count int) *rotatorChild {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		rotatorChildEnv+"="+mode,
		"MIHARI_ROTATOR_ROOT="+root,
		"MIHARI_ROTATOR_BASE="+base,
		"MIHARI_ROTATOR_WRITER="+writer,
		"MIHARI_ROTATOR_COUNT="+strconv.Itoa(count),
		"MIHARI_ROTATOR_MAXSIZE="+strconv.FormatInt(cfg.MaxSizeBytes, 10),
		"MIHARI_ROTATOR_MAXFILES="+strconv.Itoa(cfg.MaxFiles),
	)
	errBuf := &bytes.Buffer{}
	cmd.Stderr = errBuf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return &rotatorChild{stdin: stdin, sc: bufio.NewScanner(stdout), cmd: cmd, errBuf: errBuf}
}

func waitChildExit(t *testing.T, child *rotatorChild) {
	t.Helper()
	if err := child.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.cmd.Wait(); err != nil {
		t.Fatalf("child wait: %v stderr=%s", err, child.errBuf.String())
	}
	child.cmd.Process = nil
}

func runRotatorChild() int {
	root := os.Getenv("MIHARI_ROTATOR_ROOT")
	base := os.Getenv("MIHARI_ROTATOR_BASE")
	writer := os.Getenv("MIHARI_ROTATOR_WRITER")
	mode := os.Getenv(rotatorChildEnv)
	maxSize, _ := strconv.ParseInt(os.Getenv("MIHARI_ROTATOR_MAXSIZE"), 10, 64)
	maxFiles, _ := strconv.Atoi(os.Getenv("MIHARI_ROTATOR_MAXFILES"))
	count, _ := strconv.Atoi(os.Getenv("MIHARI_ROTATOR_COUNT"))
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "child NewPrivateFS: %v\n", err)
		return 1
	}
	defer func() { _ = fs.Close() }()
	if err := fs.EnsureDir(filepath.Dir(base)); err != nil {
		fmt.Fprintf(os.Stderr, "child EnsureDir: %v\n", err)
		return 1
	}
	cfg := Config{Level: slog.LevelInfo, MaxSizeBytes: maxSize, MaxFiles: maxFiles}
	switch mode {
	case "open-pause":
		testAfterExclusiveLock = func() {
			fmt.Println("locked")
			sc := bufio.NewScanner(os.Stdin)
			if !sc.Scan() {
				fmt.Fprintf(os.Stderr, "child pause stdin: %v\n", sc.Err())
				os.Exit(1)
			}
		}
		w, err := OpenRotatingWriter(context.Background(), RotatorOptions{BasePath: base, Config: cfg, PrivateFS: fs, WriteWait: 30 * time.Second})
		if err != nil {
			fmt.Fprintf(os.Stderr, "child Open: %v\n", err)
			return 1
		}
		_ = w.Close()
		fmt.Println("opened")
		return 0
	case "stale":
		w, err := OpenRotatingWriter(context.Background(), RotatorOptions{BasePath: base, Config: cfg, PrivateFS: fs, WriteWait: 30 * time.Second})
		if err != nil {
			fmt.Fprintf(os.Stderr, "child Open: %v\n", err)
			return 1
		}
		defer func() { _ = w.Close() }()
		if err := writeRotatorRecord(w, writer, 1); err != nil {
			fmt.Fprintf(os.Stderr, "child write 1: %v\n", err)
			return 1
		}
		fmt.Println("wrote1")
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			fmt.Fprintf(os.Stderr, "child stale stdin: %v\n", sc.Err())
			return 1
		}
		if err := writeRotatorRecord(w, writer, 2); err != nil {
			fmt.Fprintf(os.Stderr, "child write 2: %v\n", err)
			return 1
		}
		fmt.Println("wrote2")
		return 0
	default:
		w, err := OpenRotatingWriter(context.Background(), RotatorOptions{BasePath: base, Config: cfg, PrivateFS: fs, WriteWait: 30 * time.Second})
		if err != nil {
			fmt.Fprintf(os.Stderr, "child Open: %v\n", err)
			return 1
		}
		defer func() { _ = w.Close() }()
		for i := 1; i <= count; i++ {
			if err := writeRotatorRecord(w, writer, i); err != nil {
				fmt.Fprintf(os.Stderr, "child write %d: %v\n", i, err)
				return 1
			}
		}
		return 0
	}
}

func writeRotatorRecord(w *RotatingWriter, writer string, seq int) error {
	body, err := json.Marshal(rotatorRecord{Writer: writer, Seq: seq})
	if err != nil {
		return err
	}
	_, err = w.Write(append(body, '\n'))
	return err
}

func readAllJSONL(t *testing.T, fs *platform.PrivateFS, dir, baseName string) [][]byte {
	t.Helper()
	files := collectLogFiles(t, fs, dir, baseName)
	var lines [][]byte
	for _, body := range files {
		for _, line := range bytes.Split(body, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			copied := append([]byte(nil), line...)
			lines = append(lines, copied)
		}
	}
	return lines
}

func collectLogFiles(t *testing.T, fs *platform.PrivateFS, dir, baseName string) map[string][]byte {
	t.Helper()
	entries, err := fs.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte)
	for _, e := range entries {
		if e.Name != baseName && !strings.HasPrefix(e.Name, baseName+".") {
			continue
		}
		if strings.HasSuffix(e.Name, ".lock") {
			continue
		}
		if !e.Mode.IsRegular() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name] = body
	}
	return out
}

func fileValues(files map[string][]byte) [][]byte {
	out := make([][]byte, 0, len(files))
	for _, body := range files {
		out = append(out, body)
	}
	return out
}
