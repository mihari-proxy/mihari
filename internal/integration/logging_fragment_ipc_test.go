package integration

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestLoggingFragment_NativeIPCAndZIPPreserveOriginal(t *testing.T) {
	root := t.TempDir()
	paths := platform.NewPaths(root)
	exportPaths := logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog}
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Error(err)
		}
	})
	runtime, err := logging.Open(context.Background(), logging.RuntimeOptions{
		BasePath: paths.DaemonLog, Component: "daemon", PrivateFS: fs,
		Config: logging.Config{Level: slog.LevelInfo, MaxSizeBytes: 1 << 20, MaxFiles: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	original := strings.Repeat("\x01", (256<<10)-32) + "https://fixture/?token=原文"
	runtime.Logger().Error("large original cause", "cause", original)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := transporttest.Endpoint(t)
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	server := controlserver.New(controlserver.Options{
		Token: "fixture-control-token", Store: state.NewStore(state.Snapshot{}),
		SnapshotSource: logging.NewMachineSnapshotSource(logging.MachineSnapshotOptions{PrivateFS: fs, Paths: exportPaths}),
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("snapshot server failed to join")
		}
	})
	client := controlclient.New(endpoint, "fixture-control-token")
	now := time.Now()
	set, err := client.OpenMachineSnapshot(ctx, logging.SnapshotWindow{To: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(); err != nil {
			t.Error(err)
		}
	})
	result, err := logging.Assemble(ctx, logging.ExportRequest{
		Now: now, Range: logging.ExportRange{Kind: logging.RangeAll}, OutputPath: filepath.Join(t.TempDir(), "ipc.zip"),
		PrivateFS: fs, Paths: exportPaths,
	}, logging.ExportScopeMachineAndCurrentUser, logging.NamedSourcesFromSet(set), set.Finish)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := archive.Close(); err != nil {
			t.Error(err)
		}
	}()
	var fragments []string
	identity := ""
	for _, file := range archive.File {
		if file.Name != "daemon/mihari-daemon.log" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(reader)
		for {
			var record struct {
				ID    string `json:"record_id"`
				Index int    `json:"fragment_index"`
				Count int    `json:"fragment_count"`
				Cause string `json:"cause"`
			}
			if err := decoder.Decode(&record); err == io.EOF {
				break
			} else if err != nil {
				_ = reader.Close()
				t.Fatal(err)
			}
			if record.ID == "" || record.Count < 2 || record.Index < 1 || record.Index > record.Count {
				_ = reader.Close()
				t.Fatal("fragment metadata lost over IPC")
			}
			if fragments == nil {
				identity = record.ID
				fragments = make([]string, record.Count)
			}
			if record.ID != identity || fragments[record.Index-1] != "" {
				_ = reader.Close()
				t.Fatal("fragment identity/ordering corrupted")
			}
			fragments[record.Index-1] = record.Cause
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(fragments, "") != original {
		t.Fatal("native IPC or ZIP lost original 256 KiB content")
	}
}
