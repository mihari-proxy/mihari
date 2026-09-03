package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestFinishRunRelaunchesOnlyWhenRequested(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "service restart failed"
	var warnings bytes.Buffer
	calls := 0
	err := finishRun(model, nil, &warnings, func() error {
		calls++
		if !strings.Contains(warnings.String(), "service restart failed") {
			t.Fatal("relaunch ran before warning was written")
		}
		return nil
	}, func(tea.Model) error { return nil })
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestFinishRunCleansUpSessionBeforeRelaunch(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "final model marker"
	cleaned := false

	err := finishRun(model, nil, io.Discard, func() error {
		if !cleaned {
			t.Fatal("relaunch ran before control session cleanup")
		}
		return nil
	}, func(final tea.Model) error {
		got, ok := final.(Model)
		if !ok || got.RelaunchWarning() != "final model marker" {
			t.Fatalf("cleanup model=%T did not receive final model", final)
		}
		cleaned = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("control session cleanup did not run")
	}
}

func TestFinishRunNormalExitDoesNotRelaunch(t *testing.T) {
	calls := 0
	cleanups := 0
	if err := finishRun(NewModel(), nil, io.Discard, func() error { calls++; return nil }, func(tea.Model) error { cleanups++; return nil }); err != nil || calls != 0 || cleanups != 1 {
		t.Fatalf("calls=%d cleanups=%d err=%v", calls, cleanups, err)
	}
}

func TestFinishRunProgramErrorPreventsRelaunch(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	runErr := errors.New("program failed")
	calls := 0
	cleanups := 0
	err := finishRun(model, runErr, io.Discard, func() error { calls++; return nil }, func(tea.Model) error { cleanups++; return nil })
	if !errors.Is(err, runErr) || calls != 0 || cleanups != 1 {
		t.Fatalf("calls=%d cleanups=%d err=%v", calls, cleanups, err)
	}
}

func TestFinishRunReturnsRelaunchError(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	relaunchErr := errors.New("start replacement failed")
	err := finishRun(model, nil, io.Discard, func() error { return relaunchErr }, func(tea.Model) error { return nil })
	if !errors.Is(err, relaunchErr) {
		t.Fatalf("err=%v", err)
	}
}

type failingWriter struct{ err error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestFinishRunWarningWriteFailureStillRelaunches(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "service restart failed"
	writeErr := errors.New("terminal is closed")
	calls := 0

	err := finishRun(model, nil, failingWriter{err: writeErr}, func() error {
		calls++
		return nil
	}, func(tea.Model) error { return nil })
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestFinishRunPreservesWarningAndRelaunchErrors(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "service restart failed"
	writeErr := errors.New("terminal is closed")
	relaunchErr := errors.New("start replacement failed")

	err := finishRun(model, nil, failingWriter{err: writeErr}, func() error { return relaunchErr }, func(tea.Model) error { return nil })
	if !errors.Is(err, writeErr) || !errors.Is(err, relaunchErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadingModel_ViewUsesAlternateScreen(t *testing.T) {
	view := (loadingModel{}).View()
	if !view.AltScreen {
		t.Fatal("loading view must use the alternate screen")
	}
}

func TestLoadingModel_QuitKeysStopProgram(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		_, command := (loadingModel{}).Update(key)
		if command == nil {
			t.Fatalf("key=%q did not return a quit command", key.String())
		}
		if message := command(); message != tea.Quit() {
			t.Fatalf("key=%q message=%#v", key.String(), message)
		}
	}
}

func TestRunFactoryClosesPartialResourcesLogging(t *testing.T) {
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var warnings bytes.Buffer
	called := false
	err = Run(ctx, Options{
		OpenLogging: func(got context.Context) (LoggingResources, error) {
			if got != ctx {
				t.Fatal("factory did not receive Run context")
			}
			called = true
			return LoggingResources{PrivateFS: fs, Redactor: logging.NewRedactor("tui-bootstrap-token")}, errors.New("open https://example.invalid/?token=tui-bootstrap-token")
		},
		ErrorOutput: &warnings,
		Input:       strings.NewReader("q"),
		Output:      io.Discard,
	})
	if !called {
		t.Fatal("logging factory was not called")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v want context cancellation", err)
	}
	if err := fs.EnsureDir(paths.LogDir); err == nil {
		t.Fatal("partial logging PrivateFS was not closed")
	}
	if got := warnings.String(); got != "Warning: TUI file logging is unavailable\n" {
		t.Fatalf("warnings=%q", got)
	}
}

func TestLoggingResourcesCloseClosesRuntimeBeforePrivateFSAndIsIdempotent(t *testing.T) {
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := logging.Open(context.Background(), logging.RuntimeOptions{
		BasePath: paths.TUILog, Component: "tui", Config: logging.BootstrapConfig(), PrivateFS: fs,
	})
	if err != nil {
		t.Fatal(err)
	}
	resources := &LoggingResources{Runtime: runtime, PrivateFS: fs}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir(paths.LogDir); err == nil {
		t.Fatal("PrivateFS remains open after resources Close")
	}
}

func TestModelSetLoggingHealthKeepsFactoryHealth(t *testing.T) {
	health := testLoggingHealth{available: true}
	model := NewModel()
	model.SetLoggingHealth(health)
	if model.loggingHealth == nil || !model.loggingHealth.Available() {
		t.Fatal("model did not retain logging health")
	}
}

type testLoggingHealth struct{ available bool }

func (h testLoggingHealth) Available() bool { return h.available }
