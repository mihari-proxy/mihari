package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fragmentFailureWriter struct {
	bytes.Buffer
	writes, failAt, reports int
	short                   bool
	failure                 error
}

func (w *fragmentFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		if w.short {
			return len(p) - 1, nil
		}
		return 0, w.failure
	}
	return w.Buffer.Write(p)
}

func (w *fragmentFailureWriter) report(_ FailureClass, err error) {
	w.reports++
	w.failure = err
}

func TestRecordFragment_IdentityAndPartialWriteFailures(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"msg": strings.Repeat("\x01", diagnosticMaxBytes)})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"identity", "empty identity", "write", "short write", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			cause := errors.New("fixture record failure")
			out := &fragmentFailureWriter{failure: cause}
			writer := &recordWriter{out: out, newID: func() (string, error) { return "fixture-record", nil }}
			input := raw
			switch mode {
			case "identity":
				writer.newID = func() (string, error) { return "", cause }
			case "empty identity":
				writer.newID = func() (string, error) { return "", nil }
			case "write", "short write":
				out.failAt, out.short = 2, mode == "short write"
			case "metadata":
				input = []byte(strings.Repeat("x", 3*MaxExportRecordBytes))
			}
			_, err := writer.Write(input)
			if err == nil {
				t.Fatal("fragment preparation or write failure was hidden")
			}
			switch mode {
			case "identity", "empty identity", "metadata":
				if out.reports != 1 || out.writes != 0 {
					t.Fatal("preparation failure did not use independent reporter exactly once")
				}
				if mode == "identity" && !errors.Is(err, cause) {
					t.Fatal("identity failure cause lost")
				}
			default:
				if out.reports != 0 || out.writes != 2 {
					t.Fatal("IO failure duplicated its writer owner or continued writing")
				}
				var part struct{ Index, Count int }
				var record map[string]json.RawMessage
				if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(record["fragment_index"], &part.Index); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(record["fragment_count"], &part.Count); err != nil {
					t.Fatal(err)
				}
				if part.Index != 1 || part.Count < 2 {
					t.Fatal("partial logical record cannot be identified")
				}
				if mode == "short write" && !errors.Is(err, io.ErrShortWrite) {
					t.Fatal("short write not detected")
				}
			}
		})
	}
}

func TestRecordFragment_ConcurrentWritersRotateSnapshotAndExport(t *testing.T) {
	fs, paths := openExportTestFS(t)
	var writers []*RotatingWriter
	var loggers []*slog.Logger
	for range 2 {
		writer, err := OpenRotatingWriter(context.Background(), RotatorOptions{PrivateFS: fs, BasePath: paths.DaemonLog, Config: Config{Level: slog.LevelInfo, MaxSizeBytes: 1 << 20, MaxFiles: 10}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := writer.Close(); err != nil {
				t.Error(err)
			}
		})
		writers = append(writers, writer)
		loggers = append(loggers, slog.New(NewJSONHandler(writer, &slog.LevelVar{}, "daemon", nil)).WithGroup("detail"))
	}
	raw := strings.Repeat("\x01", diagnosticMaxBytes-24) + "https://test/?token=abc"
	var tasks sync.WaitGroup
	for _, logger := range loggers {
		tasks.Go(func() { logger.Error("failure", "cause", raw) })
	}
	tasks.Wait()
	for _, writer := range writers {
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Add(time.Second)
	set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths}).Open(context.Background(), SnapshotWindow{To: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	request := ExportRequest{Now: now, Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(t.TempDir(), "fragments.zip"), PrivateFS: fs, Paths: paths}
	result, err := Assemble(context.Background(), request, ExportScopeMachineAndCurrentUser, NamedSourcesFromSet(set), set.Finish)
	if err != nil {
		t.Fatal(err)
	}
	archive := readAssembleZip(t, result.Path)
	type fragment struct {
		ID     string `json:"record_id"`
		Index  int    `json:"fragment_index"`
		Count  int    `json:"fragment_count"`
		Detail struct {
			Cause string `json:"cause"`
		} `json:"detail"`
	}
	records := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(archive[exportDaemonEntry]), "\n") {
		var part fragment
		if err := json.Unmarshal([]byte(line), &part); err != nil {
			t.Fatal(err)
		}
		if part.ID == "" || part.Count < 2 || part.Index < 1 || part.Index > part.Count {
			t.Fatal("fragment metadata lost")
		}
		if records[part.ID] == nil {
			records[part.ID] = make([]string, part.Count)
		}
		if records[part.ID][part.Index-1] != "" {
			t.Fatal("two writers reused a record identity")
		}
		records[part.ID][part.Index-1] = part.Detail.Cause
	}
	if len(records) != 2 {
		t.Fatal("concurrent logical records were lost or mixed")
	}
	for _, parts := range records {
		if strings.Join(parts, "") != raw {
			t.Fatal("rotation, snapshot or archive lost original content")
		}
	}
}
