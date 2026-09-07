package logging

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestAssemble_FixtureSourcesPublishManifestV2(t *testing.T) {
	payload := []byte(`{"time":"2026-09-05T00:00:00Z","msg":"节点","n":1e0}`)
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.TUILog, `{"time":"2026-09-05T00:00:00Z","msg":"tui"}`+"\n")
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	request := ExportRequest{
		Now:   time.Date(2026, 9, 5, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)),
		Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs, Redactor: NewRedactor(),
	}
	sources := []NamedSource{
		{ID: DaemonSource, Source: &memorySnapshotSource{id: DaemonSource, records: []memoryRecord{{payload: payload}}, stats: SourceStats{Source: DaemonSource, Lines: 1, Files: []string{"mihari-daemon.log"}, Bytes: int64(len(payload) + 1)}}},
		{ID: MihomoSource, Source: &memorySnapshotSource{id: MihomoSource, stats: SourceStats{Source: MihomoSource, Files: []string{}, SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}},
		{ID: TUISource, Source: NewFileSnapshotSource(TUISource, MachineSnapshotOptions{PrivateFS: fs, Paths: paths, Redactor: NewRedactor()})},
	}
	finished := false
	result, err := Assemble(context.Background(), request, ExportScopeMachineAndCurrentUser, sources, func(context.Context) error {
		finished = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !finished {
		t.Fatal("set finish was not called before publish")
	}
	if result.Path != out {
		t.Fatalf("path=%q", result.Path)
	}
	got := readAssembleZip(t, out)
	if _, ok := got[exportMihomoEntry]; ok {
		t.Fatal("empty mihomo entry published")
	}
	if got[exportDaemonEntry] == "" || got[exportTUIEntry] == "" || got[exportManifestEntry] == "" {
		t.Fatalf("entries=%v", got)
	}
	if strings.Count(got[exportDaemonEntry], "\n") == 0 || strings.Count(got[exportTUIEntry], "\n") == 0 {
		t.Fatal("zero-byte log entry")
	}
	manifest := decodeAssembleManifest(t, got[exportManifestEntry])
	if manifest["schema"] != exportManifestV2 || manifest["scope"] != ExportScopeMachineAndCurrentUser {
		t.Fatalf("manifest=%v", manifest)
	}
	status, _ := manifest["source_status"].(map[string]any)
	if status["daemon"] != ExportSourceCollected || status["mihomo"] != ExportSourceCollected || status["tui"] != ExportSourceCollected {
		t.Fatalf("status=%v", status)
	}
	files, _ := manifest["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files=%v", files)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("perm=%o", info.Mode().Perm())
		}
	}
}

func TestAssemble_AllEmptyDoesNotPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}
	sources := []NamedSource{
		{ID: DaemonSource, Source: &memorySnapshotSource{id: DaemonSource, stats: SourceStats{Source: DaemonSource, Files: []string{}}}},
		{ID: MihomoSource, Source: &memorySnapshotSource{id: MihomoSource, stats: SourceStats{Source: MihomoSource, Files: []string{}}}},
	}
	published := false
	_, err := assembleWithOps(context.Background(), request, ExportScopeMachineAndCurrentUser, sources, func(context.Context) error { return nil }, exportOps{
		Publish: func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error {
			published = true
			return errors.New("publish must not be called")
		},
	})
	if !errors.Is(err, ErrNoLogLines) {
		t.Fatalf("error=%v", err)
	}
	if published {
		t.Fatal("publish called")
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spool remains %v err=%v", entries, err)
	}
}

func TestAssemble_FinishFailureDoesNotPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}
	payload := []byte(`{"time":"2026-09-05T00:00:00Z","msg":"ok"}`)
	sources := []NamedSource{
		{ID: DaemonSource, Source: &memorySnapshotSource{id: DaemonSource, records: []memoryRecord{{payload: payload}}, stats: SourceStats{Source: DaemonSource, Lines: 1, Files: []string{"mihari-daemon.log"}}}},
	}
	published := false
	_, err := assembleWithOps(context.Background(), request, ExportScopeCurrentUserOnly, sources, func(context.Context) error {
		return errors.New("complete without eof")
	}, exportOps{Publish: func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error {
		published = true
		return errors.New("publish must not be called")
	}})
	if err == nil {
		t.Fatal("expected finish failure")
	}
	if published {
		t.Fatal("publish called")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spool remains %v err=%v", entries, err)
	}
}

func TestAssemble_CurrentUserOnlyNotesMachineNotRequested(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.TUILog, `{"time":"2026-09-05T00:00:00Z","msg":"tui"}`+"\n")
	out := filepath.Join(t.TempDir(), "export.zip")
	request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs, Redactor: NewRedactor()}
	sources := []NamedSource{{ID: TUISource, Source: NewFileSnapshotSource(TUISource, MachineSnapshotOptions{PrivateFS: fs, Paths: paths, Redactor: NewRedactor()})}}
	if _, err := Assemble(context.Background(), request, ExportScopeCurrentUserOnly, sources, nil); err != nil {
		t.Fatal(err)
	}
	got := readAssembleZip(t, out)
	if _, ok := got[exportDaemonEntry]; ok {
		t.Fatal("machine log published for TUI-only export")
	}
	manifest := decodeAssembleManifest(t, got[exportManifestEntry])
	if manifest["scope"] != ExportScopeCurrentUserOnly {
		t.Fatalf("scope=%v", manifest["scope"])
	}
	status, _ := manifest["source_status"].(map[string]any)
	if status["daemon"] != ExportSourceNotRequested || status["mihomo"] != ExportSourceNotRequested || status["tui"] != ExportSourceCollected {
		t.Fatalf("status=%v", status)
	}
	notes, _ := manifest["notes"].([]any)
	joined := ""
	for _, note := range notes {
		joined += note.(string)
	}
	if !strings.Contains(joined, exportMachineNote) {
		t.Fatalf("notes=%v", notes)
	}
}

func TestAssemble_TUIFileLimitDoesNotPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}
	files := make([]string, 11)
	for i := range files {
		files[i] = "mihari-tui.log"
	}
	sources := []NamedSource{{ID: TUISource, Source: &memorySnapshotSource{
		id:      TUISource,
		records: []memoryRecord{{payload: []byte(`{"time":"2026-09-05T00:00:00Z"}`)}},
		stats:   SourceStats{Source: TUISource, Lines: 1, Files: files},
	}}}
	published := false
	_, err := assembleWithOps(context.Background(), request, ExportScopeCurrentUserOnly, sources, nil, exportOps{
		Publish: func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error {
			published = true
			return errors.New("publish must not be called")
		},
	})
	if err == nil {
		t.Fatal("11 TUI files accepted")
	}
	if published {
		t.Fatal("publish called")
	}
}

func TestAssemble_CancelDoesNotPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sources := []NamedSource{{ID: TUISource, Source: &memorySnapshotSource{
		id: TUISource, records: []memoryRecord{{payload: []byte(`{"time":"2026-09-05T00:00:00Z"}`)}},
		stats: SourceStats{Source: TUISource, Lines: 1, Files: []string{"mihari-tui.log"}},
	}}}
	_, err := Assemble(ctx, request, ExportScopeCurrentUserOnly, sources, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("residue=%v err=%v", entries, readErr)
	}
}

func TestAssemble_ClosesEachSource(t *testing.T) {
	fs, paths := openExportTestFS(t)
	out := filepath.Join(t.TempDir(), "export.zip")
	request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}
	daemon := &memorySnapshotSource{id: DaemonSource, records: []memoryRecord{{payload: []byte(`{"time":"2026-09-05T00:00:00Z"}`)}}, stats: SourceStats{Source: DaemonSource, Lines: 1, Files: []string{"mihari-daemon.log"}}}
	mihomo := &memorySnapshotSource{id: MihomoSource, stats: SourceStats{Source: MihomoSource, Files: []string{}}}
	if _, err := Assemble(context.Background(), request, ExportScopeMachineAndCurrentUser, []NamedSource{{ID: DaemonSource, Source: daemon}, {ID: MihomoSource, Source: mihomo}}, nil); err != nil {
		t.Fatal(err)
	}
	if daemon.closed.Load() != 1 || mihomo.closed.Load() != 1 {
		t.Fatalf("closes daemon=%d mihomo=%d", daemon.closed.Load(), mihomo.closed.Load())
	}
}

type memoryRecord struct {
	payload  []byte
	redacted bool
}

type memorySnapshotSource struct {
	id      SourceID
	records []memoryRecord
	stats   SourceStats
	openErr error
	closed  atomic.Int32
}

func (s *memorySnapshotSource) Open(context.Context, SnapshotWindow) (SourceReader, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return &memorySourceReader{src: s}, nil
}

type memorySourceReader struct {
	src      *memorySnapshotSource
	index    int
	eof      bool
	finished bool
}

func (r *memorySourceReader) Next(ctx context.Context) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if r.index == len(r.src.records) {
		r.eof = true
		return nil, false, io.EOF
	}
	rec := r.src.records[r.index]
	r.index++
	return rec.payload, rec.redacted, nil
}

func (r *memorySourceReader) Finish(ctx context.Context) (SourceStats, error) {
	if err := ctx.Err(); err != nil {
		return SourceStats{}, err
	}
	if !r.eof {
		return SourceStats{}, errors.New("memory source is incomplete")
	}
	r.finished = true
	return r.src.stats, nil
}

func (r *memorySourceReader) Close() error {
	r.src.closed.Add(1)
	return nil
}

func readAssembleZip(t *testing.T, path string) map[string]string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = string(b)
	}
	return got
}

func decodeAssembleManifest(t *testing.T, raw string) map[string]any {
	t.Helper()
	var manifest map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}
