package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestOpenTUILogging_UserTreeDoesNotCreateCredentialOrReadDataTree(t *testing.T) {
	machine := filepath.Join(t.TempDir(), "machine")
	if err := os.MkdirAll(machine, 0o700); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(machine, "secret-data")
	if err := os.WriteFile(canary, []byte("machine-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	userPaths, err := platform.NewPaths(filepath.Join(t.TempDir(), "user")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := platform.NewPrivateFS(userPaths.Root)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := openTUILogging(context.Background(), userPaths, "tui-control-token", fs, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close() })
	if _, statErr := os.Stat(userPaths.ControlToken); !os.IsNotExist(statErr) {
		t.Fatal("logging factory created control credential")
	}
	if _, statErr := os.Stat(filepath.Join(machine, "control.token")); !os.IsNotExist(statErr) {
		t.Fatal("logging factory created machine credential")
	}
	got, err := os.ReadFile(canary)
	if err != nil || string(got) != "machine-secret" {
		t.Fatalf("machine data tree was mutated: %s err=%v", got, err)
	}
	if resources.Runtime == nil || resources.PrivateFS != fs {
		t.Fatalf("user-tree logger resources=%+v", resources)
	}
	if _, statErr := os.Stat(userPaths.TUILog); statErr != nil {
		t.Fatalf("user TUI log missing: %v", statErr)
	}
}

func TestOpenTUILogging_UnavailableUserTreeIsMemoryOnly(t *testing.T) {
	machine := filepath.Join(t.TempDir(), "machine")
	resources, err := openTUILogging(context.Background(), platform.Paths{}, "tui-control-token", nil, io.Discard)
	if err == nil {
		t.Fatal("unavailable user tree did not fail closed")
	}
	if resources.Runtime != nil || resources.PrivateFS != nil || resources.Health == nil || resources.Health.Available() {
		t.Fatalf("memory-only resources=%+v", resources)
	}
	if _, statErr := os.Stat(machine); !os.IsNotExist(statErr) {
		t.Fatalf("memory-only factory created machine tree: %v", statErr)
	}
}

func TestOpenTUILogging_InitFailuresCloseOnce(t *testing.T) {
	for _, test := range []struct {
		name string
		open func(*testing.T) (tui.LoggingResources, error)
	}{
		{
			name: "nil fs",
			open: func(t *testing.T) (tui.LoggingResources, error) {
				return openTUILogging(context.Background(), absoluteTempPaths(t), "token", nil, io.Discard)
			},
		},
		{
			name: "canceled context",
			open: func(t *testing.T) (tui.LoggingResources, error) {
				paths := absoluteTempPaths(t)
				fs, err := platform.NewPrivateFS(paths.Root)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return openTUILogging(ctx, paths, "token", fs, io.Discard)
			},
		},
		{
			name: "closed fs",
			open: func(t *testing.T) (tui.LoggingResources, error) {
				paths := absoluteTempPaths(t)
				fs, err := platform.NewPrivateFS(paths.Root)
				if err != nil {
					t.Fatal(err)
				}
				if err := fs.Close(); err != nil {
					t.Fatal(err)
				}
				return openTUILogging(context.Background(), paths, "token", fs, io.Discard)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources, err := test.open(t)
			if err == nil {
				t.Fatal("expected initialization failure")
			}
			copied := resources
			if closeErr := resources.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if closeErr := copied.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if closeErr := resources.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}

func TestExportAssembledLogs_OnlineFullExportSameWindow(t *testing.T) {
	fs, userLogs := tempUserLogs(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	request := logging.ExportRequest{Now: now, Range: logging.ExportRange{Kind: logging.RangeLast24Hours}}
	local := &recordingSnapshotSource{}
	var machineWindow logging.SnapshotWindow
	var gotScope string
	var gotIDs []logging.SourceID
	set := &fakeSnapshotSet{}
	_, err := exportAssembledLogs(context.Background(), request, assembledExportOptions{
		Scope:       logging.ExportScopeMachineAndCurrentUser,
		UserLogs:    userLogs,
		Resources:   tui.NewLoggingResources(nil, logging.NewRedactor(), fs),
		LocalSource: local,
		OpenMachineSnapshot: func(_ context.Context, window logging.SnapshotWindow) (logging.SnapshotSet, error) {
			machineWindow = window
			return set, nil
		},
		Assemble: func(ctx context.Context, got logging.ExportRequest, scope string, sources []logging.NamedSource, finish func(context.Context) error) (logging.ExportResult, error) {
			gotScope = scope
			window := snapshotWindowFromRequest(got)
			for _, source := range sources {
				gotIDs = append(gotIDs, source.ID)
				if source.Source == nil {
					continue
				}
				reader, openErr := source.Source.Open(ctx, window)
				if openErr != nil {
					return logging.ExportResult{}, openErr
				}
				_ = reader.Close()
			}
			if finish != nil {
				if finishErr := finish(ctx); finishErr != nil {
					return logging.ExportResult{}, finishErr
				}
			}
			return logging.ExportResult{Path: "full.zip"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotScope != logging.ExportScopeMachineAndCurrentUser {
		t.Fatalf("scope=%q", gotScope)
	}
	if !slices.Equal(gotIDs, []logging.SourceID{logging.DaemonSource, logging.MihomoSource, logging.TUISource}) {
		t.Fatalf("sources=%v", gotIDs)
	}
	if len(local.windows) != 1 || !sameSnapshotWindow(machineWindow, local.windows[0]) {
		t.Fatalf("machine=%+v local=%+v", machineWindow, local.windows)
	}
	if set.closed.Load() != 1 {
		t.Fatalf("snapshot set closes=%d", set.closed.Load())
	}
}

func TestExportAssembledLogs_OfflineExplicitTUIOnly(t *testing.T) {
	fs, userLogs := tempUserLogs(t)
	local := &recordingSnapshotSource{}
	openedMachine := false
	var gotScope string
	var gotIDs []logging.SourceID
	_, err := exportAssembledLogs(context.Background(), logging.ExportRequest{Now: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC), Range: logging.ExportRange{Kind: logging.RangeAll}}, assembledExportOptions{
		Scope:       logging.ExportScopeCurrentUserOnly,
		UserLogs:    userLogs,
		Resources:   tui.NewLoggingResources(nil, logging.NewRedactor(), fs),
		LocalSource: local,
		OpenMachineSnapshot: func(context.Context, logging.SnapshotWindow) (logging.SnapshotSet, error) {
			openedMachine = true
			return nil, errors.New("machine snapshot should not be used")
		},
		Assemble: func(_ context.Context, _ logging.ExportRequest, scope string, sources []logging.NamedSource, _ func(context.Context) error) (logging.ExportResult, error) {
			gotScope = scope
			for _, source := range sources {
				gotIDs = append(gotIDs, source.ID)
			}
			return logging.ExportResult{Path: "tui-only.zip"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if openedMachine || gotScope != logging.ExportScopeCurrentUserOnly || !slices.Equal(gotIDs, []logging.SourceID{logging.TUISource}) {
		t.Fatalf("openedMachine=%v scope=%q sources=%v", openedMachine, gotScope, gotIDs)
	}
}

func TestExportAssembledLogs_DefaultDoesNotDegrade(t *testing.T) {
	fs, userLogs := tempUserLogs(t)
	assembled := false
	_, err := exportAssembledLogs(context.Background(), logging.ExportRequest{Now: time.Now(), Range: logging.ExportRange{Kind: logging.RangeAll}}, assembledExportOptions{
		Scope:     logging.ExportScopeMachineAndCurrentUser,
		UserLogs:  userLogs,
		Resources: tui.NewLoggingResources(nil, logging.NewRedactor(), fs),
		Assemble: func(context.Context, logging.ExportRequest, string, []logging.NamedSource, func(context.Context) error) (logging.ExportResult, error) {
			assembled = true
			return logging.ExportResult{Path: "degraded.zip"}, nil
		},
	})
	if assembled || !errors.Is(err, ui.ErrMachineLogsUnavailable) {
		t.Fatalf("err=%v assembled=%v", err, assembled)
	}
}

func TestExportAssembledLogs_UnavailableUserTreeIsMemoryOnly(t *testing.T) {
	_, err := exportAssembledLogs(context.Background(), logging.ExportRequest{}, assembledExportOptions{
		Scope: logging.ExportScopeCurrentUserOnly,
		Assemble: func(context.Context, logging.ExportRequest, string, []logging.NamedSource, func(context.Context) error) (logging.ExportResult, error) {
			t.Fatal("memory-only export assembled a zip")
			return logging.ExportResult{}, nil
		},
	})
	if !errors.Is(err, ui.ErrLocalLogStorageUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildSystemExportLogs_PromptsForExplicitTUIOnly(t *testing.T) {
	_, userLogs := tempUserLogs(t)
	options := buildSystemExportLogs(userLogs, nil)(tui.NewLoggingResources(nil, logging.NewRedactor(), nil))
	if !options.SourcesPrompt || options.MachineAvailable == nil || options.MachineAvailable() {
		t.Fatalf("system export options=%+v", options)
	}
	if options.DefaultDir != userLogs.LogExportDir {
		t.Fatalf("default export dir=%q", options.DefaultDir)
	}
}

func tempUserLogs(t *testing.T) (*platform.PrivateFS, platform.Paths) {
	t.Helper()
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "user")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs, paths
}

type recordingSnapshotSource struct {
	windows []logging.SnapshotWindow
}

func (s *recordingSnapshotSource) Open(_ context.Context, window logging.SnapshotWindow) (logging.SourceReader, error) {
	s.windows = append(s.windows, window)
	return emptySourceReader{}, nil
}

type emptySourceReader struct{}

func (emptySourceReader) Next(context.Context) ([]byte, bool, error) { return nil, false, io.EOF }

func (emptySourceReader) Finish(context.Context) (logging.SourceStats, error) {
	return logging.SourceStats{Files: []string{}}, nil
}

func (emptySourceReader) Close() error { return nil }

type fakeSnapshotSet struct{ closed atomic.Int32 }

func (s *fakeSnapshotSet) Source(logging.SourceID) (logging.SourceReader, error) {
	return emptySourceReader{}, nil
}

func (s *fakeSnapshotSet) Finish(context.Context) error { return nil }

func (s *fakeSnapshotSet) Close() error {
	s.closed.Add(1)
	return nil
}

func sameSnapshotWindow(a, b logging.SnapshotWindow) bool {
	if !a.To.Equal(b.To) {
		return false
	}
	if a.From == nil || b.From == nil {
		return a.From == nil && b.From == nil
	}
	return a.From.Equal(*b.From)
}
