package logging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestExportOriginal_PreservesFileAndMachineSnapshotContent(t *testing.T) {
	const original = "https://example.test/sub?token=fixture-token\npassword: fixture-password\n/home/test/config.yaml"
	line, err := json.Marshal(map[string]string{"time": "2026-09-05T00:00:00Z", "msg": original})
	if err != nil {
		t.Fatal(err)
	}
	line = append(line, '\n')
	redactor := NewRedactor("fixture-token", "fixture-password")
	t.Run("local export", func(t *testing.T) {
		var out bytes.Buffer
		stats, err := exportJSON(context.Background(), bytes.NewReader(line), &out, ExportRange{Kind: RangeAll}, redactor)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Lines != 1 || stats.Redacted != 0 {
			t.Fatal("original export must not claim redaction")
		}
		if jsonStringField(t, decodeDiagnosticRecord(t, out.Bytes()), "msg") != original {
			t.Fatal("local export changed original log content")
		}
	})
	t.Run("machine snapshot", func(t *testing.T) {
		fs, paths := openExportTestFS(t)
		writeSnapshotFixture(t, fs, paths.DaemonLog, string(line))
		set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: fs, Paths: paths, Redactor: redactor}).Open(context.Background(), SnapshotWindow{To: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := set.Close(); err != nil {
				t.Error(err)
			}
		})
		reader, err := set.Source(DaemonSource)
		if err != nil {
			t.Fatal(err)
		}
		payload, changed, err := reader.Next(context.Background())
		if err != nil || changed {
			t.Fatal("snapshot performed content redaction")
		}
		if jsonStringField(t, decodeDiagnosticRecord(t, payload), "msg") != original {
			t.Fatal("machine snapshot changed original log content")
		}
	})
}

func TestExportOriginal_NearLimitHTMLEscapableRecordRemainsBounded(t *testing.T) {
	prefix := `{"time":"2026-09-05T00:00:00Z","msg":"`
	suffix := `"}`
	separator := " "
	fillerBytes := MaxExportRecordBytes - len(prefix) - len(suffix) - len(separator)
	if fillerBytes < 1 {
		t.Fatal("invalid test record budget")
	}
	message := strings.Repeat("<", fillerBytes) + separator
	line := []byte(prefix + message + suffix)
	if len(line) != MaxExportRecordBytes || bytes.ContainsAny(line, "\r\n") {
		t.Fatalf("fixture length=%d newline=%v", len(line), bytes.ContainsAny(line, "\r\n"))
	}

	t.Run("local export", func(t *testing.T) {
		var out bytes.Buffer
		stats, err := exportJSON(context.Background(), bytes.NewReader(append(append([]byte(nil), line...), '\n')), &out, ExportRange{Kind: RangeAll}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := append(append([]byte(nil), line...), '\n')
		if !bytes.Equal(out.Bytes(), want) || len(out.Bytes()) != MaxExportRecordBytes+1 {
			t.Fatalf("export changed or expanded record: got=%d want=%d", len(out.Bytes()), len(want))
		}
		if stats.Lines != 1 || stats.SkippedInvalid != 0 || stats.Redacted != 0 {
			t.Fatalf("stats=%+v", stats)
		}
	})

	t.Run("machine source", func(t *testing.T) {
		privateFS, paths := openExportTestFS(t)
		writeSnapshotFixture(t, privateFS, paths.DaemonLog, string(line)+"\n")
		set, err := NewMachineSnapshotSource(MachineSnapshotOptions{PrivateFS: privateFS, Paths: paths}).Open(context.Background(), SnapshotWindow{To: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := set.Close(); err != nil {
				t.Error(err)
			}
		})
		reader, err := set.Source(DaemonSource)
		if err != nil {
			t.Fatal(err)
		}
		payload, changed, err := reader.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if changed || !bytes.Equal(payload, line) || len(payload) > MaxExportRecordBytes || bytes.ContainsAny(payload, "\r\n") {
			t.Fatalf("snapshot changed or expanded record: changed=%v bytes=%d", changed, len(payload))
		}
		if _, _, err := reader.Next(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("snapshot EOF: %v", err)
		}
		stats, err := reader.Finish(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(append(append([]byte(nil), line...), '\n'))
		if stats.Lines != 1 || stats.Bytes != int64(MaxExportRecordBytes+1) || stats.SkippedInvalid != 0 || stats.Redacted != 0 || stats.SHA256 != fmt.Sprintf("%x", digest) {
			t.Fatalf("stats=%+v", stats)
		}
	})
}
