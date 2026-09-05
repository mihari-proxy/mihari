package logging

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestExportRange_NormalizesClosedUTCWindow(t *testing.T) {
	local := time.FixedZone("UTC+08:30", 8*60*60+30*60)
	now := time.Date(2026, 9, 2, 23, 41, 8, 123, local)
	tests := []struct {
		name string
		in   ExportRange
		from time.Time
		to   time.Time
	}{
		{"last 24 hours", ExportRange{Kind: RangeLast24Hours}, time.Date(2026, 9, 1, 15, 11, 8, 123, time.UTC), time.Date(2026, 9, 2, 15, 11, 8, 123, time.UTC)},
		{"last 60 minutes", ExportRange{Kind: RangeLast60Minutes}, time.Date(2026, 9, 2, 14, 11, 8, 123, time.UTC), time.Date(2026, 9, 2, 15, 11, 8, 123, time.UTC)},
		{"between", ExportRange{Kind: RangeBetween, From: time.Date(2026, 8, 31, 9, 0, 0, 0, local), To: time.Date(2026, 8, 31, 9, 0, 0, 0, local)}, time.Date(2026, 8, 31, 0, 30, 0, 0, time.UTC), time.Date(2026, 8, 31, 0, 30, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeExportRange(now, tt.in)
			if err != nil {
				t.Fatalf("normalizeExportRange: %v", err)
			}
			if !got.From.Equal(tt.from) || got.From.Location() != time.UTC {
				t.Fatalf("From=%v (%v), want %v UTC", got.From, got.From.Location(), tt.from)
			}
			if !got.To.Equal(tt.to) || got.To.Location() != time.UTC {
				t.Fatalf("To=%v (%v), want %v UTC", got.To, got.To.Location(), tt.to)
			}
		})
	}
}

func TestExportRange_AllHasNoBoundaries(t *testing.T) {
	got, err := normalizeExportRange(time.Date(2026, 9, 2, 1, 2, 3, 0, time.Local), ExportRange{Kind: RangeAll})
	if err != nil {
		t.Fatalf("normalizeExportRange: %v", err)
	}
	if !got.From.IsZero() || !got.To.IsZero() {
		t.Fatalf("All boundaries=(%v, %v), want zero values", got.From, got.To)
	}
}

func TestExportRange_RejectsInvalidRange(t *testing.T) {
	now := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
	for _, in := range []ExportRange{
		{Kind: RangeBetween, From: now.Add(time.Second), To: now},
		{Kind: RangeKind("future")},
	} {
		if _, err := normalizeExportRange(now, in); !errors.Is(err, ErrInvalidExportRequest) {
			t.Fatalf("normalizeExportRange(%q) error=%v, want ErrInvalidExportRequest", in.Kind, err)
		}
	}
}

func TestExportTarget_RejectsInvalidDestination(t *testing.T) {
	fs, paths := openExportTestFS(t)
	outside := t.TempDir()
	existing := filepath.Join(outside, "existing.zip")
	if err := os.WriteFile(existing, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	notDir := filepath.Join(outside, "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"relative", "export.zip", ErrInvalidExportRequest},
		{"wrong extension", filepath.Join(outside, "export.tar"), ErrInvalidExportRequest},
		{"missing parent", filepath.Join(outside, "missing", "export.zip"), ErrInvalidExportRequest},
		{"parent is file", filepath.Join(notDir, "export.zip"), ErrInvalidExportRequest},
		{"existing target", existing, ErrExportTargetExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if target, err := resolveExportTarget(ExportRequest{Now: time.Now(), OutputPath: tt.path, Paths: paths, PrivateFS: fs}); !errors.Is(err, tt.wantErr) {
				if target != nil {
					closeExportTarget(target)
				}
				t.Fatalf("resolveExportTarget error=%v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestExportTarget_RejectsLogDirectoryAndDescendant(t *testing.T) {
	fs, paths := openExportTestFS(t)
	descendant := filepath.Join(paths.LogDir, "child")
	if err := os.Mkdir(descendant, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{paths.LogDir, descendant} {
		target, err := resolveExportTarget(ExportRequest{Now: time.Now(), OutputPath: filepath.Join(parent, "export.zip"), Paths: paths, PrivateFS: fs})
		if target != nil {
			closeExportTarget(target)
		}
		if !errors.Is(err, ErrInvalidExportRequest) {
			t.Fatalf("parent %q error=%v, want ErrInvalidExportRequest", parent, err)
		}
	}
}

func TestExportTarget_RejectsResolvedPathInsideLogDirectory(t *testing.T) {
	fs, paths := openExportTestFS(t)
	link := filepath.Join(t.TempDir(), "logs-link")
	if err := os.Symlink(paths.LogDir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	target, err := resolveExportTarget(ExportRequest{Now: time.Now(), OutputPath: filepath.Join(link, "export.zip"), Paths: paths, PrivateFS: fs})
	if target != nil {
		closeExportTarget(target)
	}
	if !errors.Is(err, ErrInvalidExportRequest) {
		t.Fatalf("error=%v, want ErrInvalidExportRequest", err)
	}
}

func TestExportTarget_DataRootSymlinkCannotBypassContainment(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	paths := exportPaths(platform.NewPaths(linkedRoot))
	fs, err := platform.NewPrivateFS(linkedRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	target, err := resolveExportTarget(ExportRequest{Now: time.Now(), OutputPath: filepath.Join(realRoot, "logs", "export.zip"), Paths: paths, PrivateFS: fs})
	if target != nil {
		closeExportTarget(target)
	}
	if !errors.Is(err, ErrInvalidExportRequest) {
		t.Fatalf("error=%v, want ErrInvalidExportRequest", err)
	}
}

func TestExportTarget_DefaultCreatesAndNumbersInHeldDirectory(t *testing.T) {
	fs, paths := openExportTestFS(t)
	now := time.Date(2026, 9, 2, 23, 41, 8, 0, time.FixedZone("UTC-05", -5*60*60))
	if err := fs.EnsureDir(paths.ExportDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mihari-logs-20260902-234108-0500.zip", "mihari-logs-20260902-234108-0500-1.zip"} {
		f, err := fs.OpenAppend(filepath.Join(paths.ExportDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	target, err := resolveExportTarget(ExportRequest{Now: now, AutoNumber: true, Paths: paths, PrivateFS: fs})
	if err != nil {
		t.Fatalf("resolveExportTarget: %v", err)
	}
	defer closeExportTarget(target)
	if target.Name != "mihari-logs-20260902-234108-0500-2.zip" || target.Base != "mihari-logs-20260902-234108-0500" || target.Suffix != 2 || !target.AutoNumber {
		t.Fatalf("target=%+v, want preserved negative timezone stem and suffix 2", target)
	}
	if !filepath.IsAbs(target.Path) || target.Path != filepath.Join(target.Dir.Path(), target.Name) || filepath.Base(target.Name) != target.Name {
		t.Fatalf("target path/name invalid: %+v", target)
	}
	if inside, err := target.Dir.IsWithin(target.LogDir); err != nil || inside {
		t.Fatalf("live capabilities: inside=%v err=%v", inside, err)
	}
}

func TestExportTarget_CustomTargetNeverAdvances(t *testing.T) {
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	target, err := resolveExportTarget(ExportRequest{Now: time.Now(), OutputPath: filepath.Join(parent, "Chosen.ZIP"), Paths: paths, PrivateFS: fs})
	if err != nil {
		t.Fatalf("resolveExportTarget: %v", err)
	}
	defer closeExportTarget(target)
	if target.AutoNumber || target.Name != "Chosen.ZIP" || target.Path != filepath.Join(target.Dir.Path(), "Chosen.ZIP") {
		t.Fatalf("target=%+v", target)
	}
	if err := target.Advance(); !errors.Is(err, ErrInvalidExportRequest) {
		t.Fatalf("Advance error=%v, want ErrInvalidExportRequest", err)
	}
}

func TestExportTarget_AdvancePreservesTimezoneStemAndDetectsOverflow(t *testing.T) {
	target := &exportTarget{AutoNumber: true, Base: "mihari-logs-20260902-234108-0500", Suffix: 0}
	if err := target.Advance(); err != nil {
		t.Fatal(err)
	}
	if target.Name != "mihari-logs-20260902-234108-0500-1.zip" || target.Suffix != 1 {
		t.Fatalf("after Advance: name=%q suffix=%d", target.Name, target.Suffix)
	}
	target.Suffix = math.MaxInt64
	err := target.Advance()
	if err != errExportTargetSuffixOverflow {
		t.Fatalf("overflow error=%v, want exact stable overflow error", err)
	}
}

func openExportTestFS(t *testing.T) (*platform.PrivateFS, ExportPaths) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	paths := platform.NewPaths(root)
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	return fs, exportPaths(paths)
}

func exportPaths(paths platform.Paths) ExportPaths {
	return ExportPaths{
		LogDir: paths.LogDir, ExportDir: paths.LogExportDir,
		DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog,
	}
}

func closeExportTarget(target *exportTarget) {
	_ = target.Dir.Close()
	_ = target.LogDir.Close()
}

func TestExportTarget_ExistingSymlinkIsOccupied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary symlink creation is not reliably available on Windows")
	}
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	if err := os.Symlink(filepath.Join(parent, "missing"), filepath.Join(parent, "occupied.zip")); err != nil {
		t.Fatal(err)
	}
	if target, err := resolveExportTarget(ExportRequest{Now: time.Now(), OutputPath: filepath.Join(parent, "occupied.zip"), Paths: paths, PrivateFS: fs}); !errors.Is(err, ErrExportTargetExists) {
		if target != nil {
			closeExportTarget(target)
		}
		t.Fatalf("error=%v, want ErrExportTargetExists", err)
	}
}
