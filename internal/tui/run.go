package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Options contains the control client and terminal streams used by the TUI.
type Options struct {
	Client          *controlclient.Client
	Service         systempage.ServiceController
	SelfUpdater     systempage.SelfUpdater
	CurrentVersion  string
	BinaryPath      string
	Elevated        func() bool
	Relaunch        func() error
	Input           io.Reader
	Output          io.Writer
	OpenLogging     LoggingFactory
	BuildExportLogs func(LoggingResources) ui.ExportLogsOptions
	ErrorOutput     io.Writer
}

// LocalLoggingHealth reports whether the local TUI file logger is available.
type LocalLoggingHealth interface {
	Available() bool
}

// LoggingResources owns the TUI file logging runtime and the shared local data-root capability.
type LoggingResources struct {
	Runtime   *logging.Runtime
	Redactor  *logging.Redactor
	PrivateFS *platform.PrivateFS
	Health    LocalLoggingHealth

	closeState *loggingResourcesCloseState
}

type loggingResourcesCloseState struct {
	once      sync.Once
	err       error
	runtime   io.Closer
	privateFS io.Closer
}

type loggingResourcesHealth struct {
	runtime *logging.Runtime
}

// NewLoggingResources creates TUI logging resources whose close state and
// health remain shared when the value is copied by a LoggingFactory caller.
func NewLoggingResources(runtime *logging.Runtime, redactor *logging.Redactor, privateFS *platform.PrivateFS) LoggingResources {
	var runtimeCloser io.Closer
	if runtime != nil {
		runtimeCloser = runtime
	}
	var privateFSCloser io.Closer
	if privateFS != nil {
		privateFSCloser = privateFS
	}
	return LoggingResources{
		Runtime:    runtime,
		Redactor:   redactor,
		PrivateFS:  privateFS,
		Health:     &loggingResourcesHealth{runtime: runtime},
		closeState: newLoggingResourcesCloseState(runtimeCloser, privateFSCloser),
	}
}

func newLoggingResourcesCloseState(runtime io.Closer, privateFS io.Closer) *loggingResourcesCloseState {
	return &loggingResourcesCloseState{runtime: runtime, privateFS: privateFS}
}

func (h *loggingResourcesHealth) Available() bool {
	return h != nil && h.runtime != nil
}

// Available reports whether the TUI file logging runtime opened successfully.
func (r *LoggingResources) Available() bool {
	return r != nil && r.Runtime != nil
}

// Close releases the runtime before the shared data-root capability. It is safe to call repeatedly.
func (r *LoggingResources) Close() error {
	return r.closeWithLifecycle(nil, nil)
}

func (r *LoggingResources) closeWithSession(closeSession func()) error {
	return r.closeWithLifecycle(closeSession, nil)
}

func (r *LoggingResources) closeWithLifecycle(closeSession, closeApplier func()) error {
	if r == nil {
		if closeSession != nil {
			closeSession()
		}
		if closeApplier != nil {
			closeApplier()
		}
		return nil
	}
	state := r.closeState
	if state == nil {
		var runtimeCloser io.Closer
		if r.Runtime != nil {
			runtimeCloser = r.Runtime
		}
		var privateFSCloser io.Closer
		if r.PrivateFS != nil {
			privateFSCloser = r.PrivateFS
		}
		state = newLoggingResourcesCloseState(runtimeCloser, privateFSCloser)
		r.closeState = state
	}
	state.once.Do(func() {
		state.err = closeTUILifecycle(closeSession, closeApplier, state.runtime, state.privateFS)
	})
	return state.err
}

func closeTUILifecycle(closeSession, closeApplier func(), runtime io.Closer, privateFS io.Closer) error {
	if closeSession != nil {
		closeSession()
	}
	if closeApplier != nil {
		closeApplier()
	}
	var errs []error
	for _, closer := range []io.Closer{runtime, privateFS} {
		if closer != nil {
			errs = append(errs, closer.Close())
		}
	}
	return errors.Join(errs...)
}

// LoggingFactory opens TUI-local file logging using the Run context.
type LoggingFactory func(context.Context) (LoggingResources, error)

type tuiLoggingFailureKind uint8

const (
	tuiLoggingBootstrapFailure tuiLoggingFailureKind = iota
	tuiLoggingCleanupFailure
	tuiLoggingFailureWindow = time.Second
)

// tuiLoggingFailureReporter emits rate-limited, stable local logging warnings.
// It intentionally never includes the underlying error because it may contain
// sensitive data or an absolute local path.
type tuiLoggingFailureReporter struct {
	out      io.Writer
	redactor *logging.Redactor
	now      func() time.Time

	mu   sync.Mutex
	last map[tuiLoggingFailureKind]time.Time
}

func newTUILoggingFailureReporter(out io.Writer, redactor *logging.Redactor, now func() time.Time) *tuiLoggingFailureReporter {
	if now == nil {
		now = time.Now
	}
	return &tuiLoggingFailureReporter{out: out, redactor: redactor, now: now, last: make(map[tuiLoggingFailureKind]time.Time)}
}

func (r *tuiLoggingFailureReporter) report(kind tuiLoggingFailureKind, err error) {
	if r == nil || r.out == nil || err == nil {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.last[kind]; ok && now.Sub(previous) < tuiLoggingFailureWindow {
		return
	}
	r.last[kind] = now
	message := "TUI file logging is unavailable"
	if kind == tuiLoggingCleanupFailure {
		message = "TUI file logging cleanup failed"
	}
	if r.redactor != nil {
		message = r.redactor.String(message)
	}
	_, _ = fmt.Fprintf(r.out, "Warning: %s\n", message)
}

// Run starts the full-screen Mihari terminal interface and blocks until it exits.
func Run(ctx context.Context, options Options) error {
	resources := LoggingResources{}
	var openErr error
	if options.OpenLogging != nil {
		opened, err := options.OpenLogging(ctx)
		resources = opened
		openErr = err
	}
	reporter := newTUILoggingFailureReporter(options.ErrorOutput, resources.Redactor, nil)
	if openErr != nil {
		reporter.report(tuiLoggingBootstrapFailure, openErr)
	}
	health := resources.Health
	if health == nil {
		health = &resources
	}
	if resources.Runtime != nil && resources.Runtime.Logger() != nil {
		resources.Runtime.Logger().Info("tui started")
	}
	applier := newRunLoggingApplier(ctx, resources.Runtime)

	var controlSession *session.Session
	var events <-chan session.Event
	if options.Client != nil {
		controlSession = session.New(options.Client, session.Options{})
		events = controlSession.Start(ctx)
	}
	model := newRunModel(ctx, options.Client, events, health, applier)
	if options.Service != nil {
		model.SetServiceController(options.Service)
	}
	model.SetSelfUpdater(options.SelfUpdater, options.CurrentVersion, options.BinaryPath, options.Elevated)
	exportLogs := attachRunExportLogs(ctx, &model, resources, options.BuildExportLogs)
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	)
	final, err := program.Run()
	cleanup := newRunCleanup(&resources, func() {
		if controlSession != nil {
			controlSession.Close()
		}
	}, exportLogs, applier, reporter)
	return finishRun(final, err, options.Output, options.Relaunch, cleanup)
}

func attachRunExportLogs(ctx context.Context, model *Model, resources LoggingResources, build func(LoggingResources) ui.ExportLogsOptions) *ui.ExportLogsModel {
	if model == nil || build == nil {
		return nil
	}
	options := build(resources)
	options.Context = ctx
	exportLogs := ui.NewExportLogsModel(options)
	model.exportLogs = exportLogs
	return exportLogs
}

func newRunCleanup(resources *LoggingResources, closeSession func(), exportLogs *ui.ExportLogsModel, applier loggingApplier, reporter *tuiLoggingFailureReporter) func(tea.Model) error {
	var once sync.Once
	var closeErr error
	return func(tea.Model) error {
		once.Do(func() {
			closeErr = resources.closeWithLifecycle(closeSession, func() {
				if exportLogs != nil {
					exportLogs.CancelAndWait()
				}
				if applier != nil {
					applier.CloseAndWait()
				}
			})
			if reporter != nil && closeErr != nil {
				reporter.report(tuiLoggingCleanupFailure, closeErr)
			}
		})
		return closeErr
	}
}

func newRunLoggingApplier(ctx context.Context, runtime *logging.Runtime) loggingApplier {
	if runtime == nil {
		return newLoggingApplier(ctx, nil)
	}
	return newLoggingApplier(ctx, runtime)
}

func newRunModel(ctx context.Context, client *controlclient.Client, events <-chan session.Event, health LocalLoggingHealth, applier loggingApplier) Model {
	model := NewModel()
	if client != nil {
		model = newModelWithClientContext(ctx, events, client)
	}
	model.SetLoggingApplier(applier)
	model.SetLocalLoggingHealth(health)
	return model
}

func finishRun(final tea.Model, runErr error, warningWriter io.Writer, relaunch func() error, cleanup func(tea.Model) error) error {
	var cleanupErr error
	if cleanup != nil {
		cleanupErr = cleanup(final)
	}
	if runErr != nil {
		return errors.Join(runErr, cleanupErr)
	}
	var requested bool
	var warning string
	switch model := final.(type) {
	case Model:
		requested = model.RelaunchRequested()
		warning = model.RelaunchWarning()
	case *Model:
		requested = model.RelaunchRequested()
		warning = model.RelaunchWarning()
	}
	if !requested {
		return cleanupErr
	}
	var warningErr error
	if warning != "" && warningWriter != nil {
		if _, err := fmt.Fprintf(warningWriter, "Warning: %s\n", warning); err != nil {
			warningErr = fmt.Errorf("write Mihari update warning: %w", err)
		}
	}
	if relaunch == nil {
		return errors.Join(cleanupErr, warningErr, fmt.Errorf("relaunch updated Mihari: relaunch is unavailable"))
	}
	if err := relaunch(); err != nil {
		return errors.Join(cleanupErr, warningErr, fmt.Errorf("relaunch updated Mihari: %w", err))
	}
	return cleanupErr
}

// loadingModel is a minimal AltScreen sample used by run_test.go only.
// Production Run() constructs the full Root Shell via NewModel / newModelWithClientContext.
type loadingModel struct{}

func (loadingModel) Init() tea.Cmd { return nil }

func (model loadingModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return model, tea.Quit
		}
	}
	return model, nil
}

func (loadingModel) View() tea.View {
	view := tea.NewView(ui.Connecting + "\n")
	view.AltScreen = true
	return view
}
