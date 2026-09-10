package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localDiagnosticsResources(t *testing.T) (LoggingResources, string) {
	t.Helper()
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	redactor := logging.NewRedactor("local-task-secret")
	runtime, err := logging.Open(context.Background(), logging.RuntimeOptions{BasePath: paths.TUILog, Component: "tui", Config: logging.BootstrapConfig(), PrivateFS: fs, Redactor: redactor})
	if err != nil {
		// Best-effort fallback: preserve the logging open error as the test failure.
		_ = fs.Close()
		t.Fatal(err)
	}
	resources := NewLoggingResources(runtime, redactor, fs)
	t.Cleanup(func() {
		if err := resources.Close(); err != nil {
			t.Error(err)
		}
	})
	return resources, paths.TUILog
}

func TestRun_LocalInstallationFailureUsesOwnedReporterBeforeClose(t *testing.T) {
	resources, path := localDiagnosticsResources(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Input: strings.NewReader(""), Output: io.Discard, ErrorOutput: io.Discard, OpenLogging: func(context.Context) (LoggingResources, error) { return resources, nil }, Installation: InstallationActions{Diagnostics: ui.LocalTaskDiagnostics{NewID: func() (string, error) { return "run-installation", nil }}, Inspect: func(ctx context.Context) (protocol.InstallationStatus, error) {
			close(started)
			<-ctx.Done()
			return protocol.InstallationStatus{}, io.ErrUnexpectedEOF
		}}})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("installation inspection did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run return=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not join installation")
	}
	assertLocalDiagnosticFile(t, path, "installation.inspect.failed", "run-installation")
}

func TestRun_AttachedExportUsesOwnedReporterBeforeCleanup(t *testing.T) {
	resources, path := localDiagnosticsResources(t)
	model := NewModel()
	local := ui.LocalTaskDiagnostics{Reporter: logging.NewDiagnosticReporter(resources.Runtime.Logger(), resources.Redactor)}
	export := attachRunExportLogs(context.Background(), &model, resources, func(LoggingResources) ui.ExportLogsOptions {
		return ui.ExportLogsOptions{Diagnostics: ui.LocalTaskDiagnostics{NewID: func() (string, error) { return "run-export", nil }}, DefaultDir: t.TempDir(), Export: func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
			return logging.ExportResult{}, &os.PathError{Op: "open", Path: "/private/local-task-secret", Err: os.ErrPermission}
		}}
	}, local)
	export.Open()
	export.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	waiter, consumed := export.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if waiter == nil || !consumed {
		t.Fatal("export did not start")
	}
	_ = waiter()
	if err := newRunCleanup(&resources, nil, nil, export, nil, nil)(model); err != nil {
		t.Fatal(err)
	}
	assertLocalDiagnosticFile(t, path, "logs.export.failed", "run-export")
}

func assertLocalDiagnosticFile(t *testing.T, path, event, id string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] == event {
			count++
			if record["operation_id"] != id || record["level"] != "ERROR" {
				t.Fatalf("wrong owned diagnostic: %+v", record)
			}
		}
	}
	if count != 1 {
		t.Fatalf("owner failure count=%d want 1; logs=%s", count, raw)
	}
	if strings.Contains(string(raw), "local-task-secret") || strings.Contains(string(raw), "/private/") {
		t.Fatal("local diagnostic leaked path/secret")
	}
}
