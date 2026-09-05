package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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

func TestRunNilPrivateFSContinuesWithoutCreatingDataRootLogging(t *testing.T) {
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	called := false
	err = Run(context.Background(), Options{
		OpenLogging: func(context.Context) (LoggingResources, error) {
			called = true
			return LoggingResources{Redactor: logging.NewRedactor("tui-bootstrap-token")}, errors.New("private fs unavailable")
		},
		ErrorOutput: &warnings,
		Input:       strings.NewReader("q"),
		Output:      io.Discard,
	})
	if err != nil || !called {
		t.Fatalf("Run error=%v factory called=%v", err, called)
	}
	if _, statErr := os.Stat(paths.Root); !os.IsNotExist(statErr) {
		t.Fatalf("nil PrivateFS created data root: %v", statErr)
	}
	if got, want := warnings.String(), "Warning: TUI file logging is unavailable\n"; got != want {
		t.Fatalf("warnings=%q want=%q", got, want)
	}
}

func TestLoggingResourcesCloseReleasesPrivateFSIdempotently(t *testing.T) {
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

func TestLoggingResourcesFallbackCloseStateIsPersistent(t *testing.T) {
	resources := &LoggingResources{}
	closeSessionCalls := 0
	closeSession := func() { closeSessionCalls++ }

	if err := resources.closeWithSession(closeSession); err != nil {
		t.Fatal(err)
	}
	if err := resources.closeWithSession(closeSession); err != nil {
		t.Fatal(err)
	}
	if closeSessionCalls != 1 {
		t.Fatalf("session close calls=%d want=1", closeSessionCalls)
	}
}

func TestLoggingResourcesCopiesShareCloseState(t *testing.T) {
	for _, test := range []struct {
		name             string
		runtime          io.Closer
		privateFS        io.Closer
		wantRuntimeClose int
		wantPrivateClose int
	}{
		{name: "zero resources"},
		{name: "partial runtime", runtime: &countingCloser{}, wantRuntimeClose: 1},
		{name: "partial private fs", privateFS: &countingCloser{}, wantPrivateClose: 1},
		{name: "runtime and private fs", runtime: &countingCloser{}, privateFS: &countingCloser{}, wantRuntimeClose: 1, wantPrivateClose: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources := LoggingResources{closeState: newLoggingResourcesCloseState(test.runtime, test.privateFS)}
			copied := resources

			if err := resources.Close(); err != nil {
				t.Fatal(err)
			}
			if err := copied.Close(); err != nil {
				t.Fatal(err)
			}

			if closer, ok := test.runtime.(*countingCloser); ok && closer.calls != test.wantRuntimeClose {
				t.Fatalf("runtime close calls=%d want=%d", closer.calls, test.wantRuntimeClose)
			}
			if closer, ok := test.privateFS.(*countingCloser); ok && closer.calls != test.wantPrivateClose {
				t.Fatalf("private fs close calls=%d want=%d", closer.calls, test.wantPrivateClose)
			}
		})
	}
}

func TestModelSetLocalLoggingHealthKeepsFactoryHealth(t *testing.T) {
	health := testLoggingHealth{available: true}
	model := NewModel()
	model.SetLocalLoggingHealth(health)
	if model.loggingHealth == nil || !model.loggingHealth.Available() {
		t.Fatal("model did not retain logging health")
	}
}

func TestTUILoggingBootstrapFailureIsRateLimited(t *testing.T) {
	var warnings bytes.Buffer
	now := time.Unix(1, 0)
	reporter := newTUILoggingFailureReporter(&warnings, nil, func() time.Time { return now })
	reporter.report(tuiLoggingBootstrapFailure, errors.New("open failed"))
	reporter.report(tuiLoggingBootstrapFailure, errors.New("open failed"))
	if got, want := warnings.String(), "Warning: TUI file logging is unavailable\n"; got != want {
		t.Fatalf("warnings=%q want=%q", got, want)
	}
	now = now.Add(tuiLoggingFailureWindow)
	reporter.report(tuiLoggingBootstrapFailure, errors.New("open failed"))
	if got, want := warnings.String(), "Warning: TUI file logging is unavailable\nWarning: TUI file logging is unavailable\n"; got != want {
		t.Fatalf("warnings after window=%q want=%q", got, want)
	}
}

func TestTUILoggingCleanupFailureNeverLeaksDetails(t *testing.T) {
	path := filepath.Join("C:", "Users", "operator", "secret-data")
	for _, redactor := range []*logging.Redactor{nil, logging.NewRedactor("tui-bootstrap-token")} {
		var warnings bytes.Buffer
		reporter := newTUILoggingFailureReporter(&warnings, redactor, nil)
		reporter.report(tuiLoggingCleanupFailure, errors.New("close "+path+" token=tui-bootstrap-token"))
		if got, want := warnings.String(), "Warning: TUI file logging cleanup failed\n"; got != want {
			t.Fatalf("warnings=%q want=%q", got, want)
		}
	}
}

func TestFinishRunClosesLifecycleExactlyOnceInEveryExitPath(t *testing.T) {
	programErr := errors.New("bubble tea failed")
	for _, test := range []struct {
		name           string
		final          tea.Model
		runErr         error
		wantCloseErr   error
		wantRelaunches int
	}{
		{name: "normal exit", final: NewModel()},
		{name: "bubble tea error", final: NewModel(), runErr: programErr, wantCloseErr: programErr},
		{name: "relaunch", final: requestedRelaunchModel(), wantRelaunches: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var order []string
			runtime := &orderedCloser{name: "runtime", order: &order}
			fs := &orderedCloser{name: "fs", order: &order}
			cleanup := func(tea.Model) error {
				return closeTUILifecycle(
					func() { order = append(order, "session") },
					func() { order = append(order, "applier") },
					runtime,
					fs,
				)
			}
			relaunches := 0
			err := finishRun(test.final, test.runErr, io.Discard, func() error {
				relaunches++
				order = append(order, "relaunch")
				if !slices.Equal(order, []string{"session", "applier", "runtime", "fs", "relaunch"}) {
					t.Fatalf("relaunch order=%q", order)
				}
				return nil
			}, cleanup)
			if !errors.Is(err, test.wantCloseErr) || relaunches != test.wantRelaunches {
				t.Fatalf("err=%v relaunches=%d", err, relaunches)
			}
			if want := []string{"session", "applier", "runtime", "fs"}; !slices.Equal(order[:4], want) {
				t.Fatalf("close order=%q want=%q", order, want)
			}
			if runtime.calls != 1 || fs.calls != 1 {
				t.Fatalf("runtime=%d fs=%d", runtime.calls, fs.calls)
			}
		})
	}
}

func TestCloseTUILifecycleWaitsForLoggingWorkerBeforeResources(t *testing.T) {
	local := &cancelAwareLocalLogging{started: make(chan struct{}), done: make(chan struct{})}
	applier := newLoggingApplier(context.Background(), local)
	if !applier.Submit(logging.BootstrapConfig()) {
		t.Fatal("Submit rejected")
	}
	select {
	case <-local.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Apply did not start")
	}
	var order []string
	runtime := &workerAwareCloser{name: "runtime", workerDone: local.done, order: &order, t: t}
	fs := &workerAwareCloser{name: "fs", workerDone: local.done, order: &order, t: t}
	err := closeTUILifecycle(
		func() { order = append(order, "session") },
		applier.CloseAndWait,
		runtime,
		fs,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"session", "runtime", "fs"}; !slices.Equal(order, want) {
		t.Fatalf("order=%q want=%q", order, want)
	}
}

func TestNewRunModelInjectsHealthAfterClientModelReplacement(t *testing.T) {
	health := testLoggingHealth{available: true}
	applier := &recordingLoggingApplier{}
	client := controlclient.New("unused", "")
	model := newRunModel(context.Background(), client, nil, health, applier)
	if model.loggingHealth == nil || !model.loggingHealth.Available() {
		t.Fatal("final client model lost logging health")
	}
	if model.loggingApply != applier {
		t.Fatal("final client model lost logging applier")
	}
	if got := applier.last(); got != logging.BootstrapConfig() {
		t.Fatalf("initial local config=%+v want bootstrap", got)
	}
}

func TestAttachRunExportLogsBuildsOnceOnFinalClientModelWithProgramContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resources := NewLoggingResources(nil, logging.NewRedactor("token"), nil)
	model := newRunModel(ctx, controlclient.New("unused", ""), nil, &resources, newRunLoggingApplier(ctx, nil))
	calls := 0
	observedCancel := make(chan struct{})
	exportLogs := attachRunExportLogs(ctx, &model, resources, func(got LoggingResources) ui.ExportLogsOptions {
		calls++
		if got.Redactor != resources.Redactor {
			t.Fatal("factory did not receive opened resources")
		}
		return ui.ExportLogsOptions{Context: context.Background(), DefaultDir: t.TempDir(), Export: func(got context.Context, _ logging.ExportRequest) (logging.ExportResult, error) {
			<-got.Done()
			close(observedCancel)
			return logging.ExportResult{}, got.Err()
		}}
	})
	if calls != 1 || exportLogs == nil || model.exportLogs != exportLogs {
		t.Fatalf("calls=%d export=%p root=%p", calls, exportLogs, model.exportLogs)
	}
	t.Cleanup(exportLogs.CancelAndWait)
	if exportLogs.Closed() == false {
		t.Fatal("new export overlay must start closed")
	}
	exportLogs.Open()
	exportLogs.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if waiter, consumed := exportLogs.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); waiter == nil || !consumed {
		t.Fatal("attached exporter did not submit")
	}
	cancel()
	select {
	case <-observedCancel:
	case <-time.After(time.Second):
		t.Fatal("exporter did not independently receive the Program context cancellation")
	}
}

func TestNewRunLoggingApplierHandlesTypedNilRuntime(t *testing.T) {
	applier := newRunLoggingApplier(context.Background(), nil)
	if !applier.Submit(logging.BootstrapConfig()) {
		t.Fatal("typed-nil runtime applier rejected Submit")
	}
	applier.CloseAndWait()
}

func TestRunCleanupOwnsExportBeforeApplierAndResourcesWhenWaiterCmdIsUnexecuted(t *testing.T) {
	started := make(chan struct{})
	exportDone := make(chan struct{})
	exportModel := ui.NewExportLogsModel(ui.ExportLogsOptions{
		Context: context.Background(), Now: func() time.Time { return time.Unix(1, 0) }, DefaultDir: t.TempDir(),
		Export: func(ctx context.Context, _ logging.ExportRequest) (logging.ExportResult, error) {
			close(started)
			<-ctx.Done()
			close(exportDone)
			return logging.ExportResult{}, ctx.Err()
		},
	})
	exportModel.Open()
	exportModel.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	waiter, consumed := exportModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if waiter == nil || !consumed {
		t.Fatal("submit did not synchronously own export")
	}
	<-started

	var order []string
	resources := NewLoggingResources(nil, nil, nil)
	resources.closeState = newLoggingResourcesCloseState(
		&workerAwareCloser{name: "runtime", workerDone: exportDone, order: &order, t: t},
		&workerAwareCloser{name: "fs", workerDone: exportDone, order: &order, t: t},
	)
	applier := &orderedLoggingApplier{order: &order, workerDone: exportDone, t: t}
	cleanup := newRunCleanup(&resources, func() { order = append(order, "session") }, exportModel, applier, nil)
	if err := cleanup(nil); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(loadingModel{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"session", "applier", "runtime", "fs"}; !slices.Equal(order, want) {
		t.Fatalf("order=%q want=%q", order, want)
	}
	// The result channel is buffered: a waiter first scheduled after cleanup still completes.
	if message := waiter(); message == nil {
		t.Fatal("late waiter returned nil")
	}
}

func TestFinishRunAlwaysInvokesStableCleanupForNilUnexpectedAndErrors(t *testing.T) {
	runErr := errors.New("program failed")
	for _, test := range []struct {
		name  string
		final tea.Model
		err   error
	}{
		{name: "nil final", final: nil},
		{name: "unexpected final", final: loadingModel{}},
		{name: "program error", final: nil, err: runErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			err := finishRun(test.final, test.err, io.Discard, nil, func(tea.Model) error { calls++; return nil })
			if calls != 1 || !errors.Is(err, test.err) {
				t.Fatalf("cleanup calls=%d err=%v", calls, err)
			}
		})
	}
}

type orderedLoggingApplier struct {
	order      *[]string
	workerDone <-chan struct{}
	t          *testing.T
}

func (a *orderedLoggingApplier) Submit(logging.Config) bool { return true }
func (a *orderedLoggingApplier) CloseAndWait() {
	if a.workerDone != nil {
		select {
		case <-a.workerDone:
		default:
			a.t.Fatal("applier closed before exporter finished")
		}
	}
	*a.order = append(*a.order, "applier")
}

func requestedRelaunchModel() Model {
	model := NewModel()
	model.relaunchRequested = true
	return model
}

type orderedCloser struct {
	name  string
	order *[]string
	calls int
}

type cancelAwareLocalLogging struct {
	started chan struct{}
	done    chan struct{}
}

func (l *cancelAwareLocalLogging) Apply(ctx context.Context, _ logging.Config) {
	close(l.started)
	<-ctx.Done()
	close(l.done)
}

type workerAwareCloser struct {
	name       string
	workerDone <-chan struct{}
	order      *[]string
	t          *testing.T
}

func (c *workerAwareCloser) Close() error {
	c.t.Helper()
	select {
	case <-c.workerDone:
	default:
		c.t.Fatal("resource closed before logging worker exited")
	}
	*c.order = append(*c.order, c.name)
	return nil
}

type countingCloser struct{ calls int }

func (c *countingCloser) Close() error {
	c.calls++
	return nil
}

func (c *orderedCloser) Close() error {
	c.calls++
	*c.order = append(*c.order, c.name)
	return nil
}

type testLoggingHealth struct{ available bool }

func (h testLoggingHealth) Available() bool { return h.available }
