package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestExportJSON_OmitsSensitiveKeyMembersWithoutCollisions(t *testing.T) {
	const input = `{"time":"2026-09-02T12:00:00Z","seq":9007199254740993,"export-secret":"drop","***":"keep","nested":[{"https://example.test/a":"drop","https://example.test/b":"drop","[REDACTED_URL]":"keep","safe":"export-secret"}]}`
	const want = `{"time":"2026-09-02T12:00:00Z","seq":9007199254740993,"***":"keep","nested":[{"[REDACTED_URL]":"keep","safe":"***"}]}`
	decode := func(s string) any {
		t.Helper()
		decoder := json.NewDecoder(strings.NewReader(s))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	r := NewRedactor("export-secret")
	original := decode(input)
	got, changed := r.Value(original)
	if !changed || !reflect.DeepEqual(got, decode(want)) {
		t.Fatal("sensitive-key omission lost safe siblings or retained sensitive members")
	}
	if !reflect.DeepEqual(original, decode(input)) {
		t.Fatal("redaction mutated input")
	}
	var output bytes.Buffer
	stats, err := exportJSON(context.Background(), strings.NewReader(input+"\n"+`{"time":"2026-09-02T12:00:00Z","msg":"safe"}`), &output, ExportRange{Kind: RangeAll}, r)
	if err != nil || stats.Lines != 2 || stats.Redacted != 1 || stats.SkippedInvalid != 0 {
		t.Fatalf("record statistics=%+v err=%v", stats, err)
	}
	if strings.Contains(output.String(), "export-secret") || strings.Contains(output.String(), "example.test") {
		t.Fatal("export leaked sensitive key")
	}
}

func TestExport_ContainmentErrorPreservesClosedCause(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T12:00:00Z","msg":"safe"}`)
	var target *exportTarget
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(t.TempDir(), "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{
		Observe: func(v *exportTarget, _ *platform.PublishWorkspace) { target = v },
		Checkpoint: func(stage exportStage) error {
			if stage == stageBeforePublish {
				return target.LogDir.Close()
			}
			return nil
		},
	})
	if !errors.Is(err, ErrExportTargetChanged) || !errors.Is(err, os.ErrClosed) || err.Error() != ErrExportTargetChanged.Error() {
		t.Fatalf("containment error lost classification, cause or stable text: %v", err)
	}
}

func TestExport_SetupCleanupPreservesBothCloseFailures(t *testing.T) {
	fs, paths := openExportTestFS(t)
	dirFailure, logFailure := errors.New("private target close"), errors.New("private source close")
	warnings := 0
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(paths.LogDir, "forbidden.zip"), Paths: paths, PrivateFS: fs, OnWarning: func(warning error) {
		warnings++
		if warning.Error() != "log export cleanup incomplete" {
			t.Fatal("unsanitized setup warning")
		}
	}}, exportOps{
		ClosePublishDir: func(d *platform.PublishDir) error { return errors.Join(d.Close(), dirFailure) },
		CloseLogDir:     func(d *platform.DirectoryIdentity) error { return errors.Join(d.Close(), logFailure) },
	})
	if !errors.Is(err, ErrInvalidExportRequest) || !errors.Is(err, dirFailure) || !errors.Is(err, logFailure) || warnings == 0 || err.Error() != ErrInvalidExportRequest.Error() {
		t.Fatalf("setup cleanup lost causes/warning/stable text: %v warnings=%d", err, warnings)
	}
}
