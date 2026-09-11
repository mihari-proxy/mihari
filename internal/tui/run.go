package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/app"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Options contains the control client and terminal streams used by the TUI.
type Options struct {
	Installation              InstallationActions
	SplitLogging              bool
	UserLogDir, UserExportDir string
	Client                    *controlclient.Client
	Service                   systempage.ServiceController
	Uninstaller               Uninstaller
	SelfUpdater               systempage.SelfUpdater
	SelfUpdateChannel         func(context.Context) (string, error)
	CurrentVersion            string
	BinaryPath                string
	Elevated                  func() bool
	Relaunch                  func() error
	Input                     io.Reader
	Output                    io.Writer
	OpenLogging               LoggingFactory
	BuildExportLogs           func(LoggingResources) ui.ExportLogsOptions
	ErrorOutput               io.Writer
}

// Uninstaller is the local complete-uninstall use case owned by app assembly.
type Uninstaller interface {
	Preview(context.Context) ([]app.UninstallTarget, error)
	Run(context.Context, func(string)) error
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
	// The logger may already be closed; failure of this last independent
	// warning outlet cannot safely be reported through it.
	_, _ = fmt.Fprintf(r.out, "Warning: %s\n", message)
}

func inspectInstallationWithOfflineFallback(
	ctx context.Context,
	daemon, offline func(context.Context) (protocol.InstallationStatus, error),
) (protocol.InstallationStatus, error) {
	status, err := daemon(ctx)
	if err == nil || offline == nil {
		return status, err
	}
	var apiErr protocol.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != protocol.CodeDaemonUnavailable {
		return status, err
	}
	return offline(ctx)
}

// Run starts the full-screen Mihari terminal interface and blocks until it exits.
func Run(ctx context.Context, options Options) (resultErr error) {
	actions := options.Installation
	if options.Client != nil {
		offline := actions.Inspect
		actions.Inspect = func(ctx context.Context) (protocol.InstallationStatus, error) {
			return inspectInstallationWithOfflineFallback(ctx, options.Client.GetInstallationStatus, offline)
		}
	}
	if actions.Elevated == nil {
		actions.Elevated = options.Elevated
	}
	if actions.Binary == "" {
		actions.Binary = options.BinaryPath
	}
	installationWorker, actions := newInstallationWorker(actions)
	defer installationWorker.shutdown()
	var preparedWorker *runPreparedUpdater
	if options.SelfUpdater != nil {
		preparedWorker = newRunPreparedUpdater(options.SelfUpdater)
		options.SelfUpdater = preparedWorker
		defer func() { resultErr = errors.Join(resultErr, preparedWorker.close()) }()
	}
	resources := LoggingResources{}
	var openErr error
	if options.OpenLogging != nil {
		opened, err := options.OpenLogging(ctx)
		resources = opened
		openErr = err
	}
	if resources.Redactor == nil {
		resources.Redactor = logging.NewRedactor()
	}
	diagnosticReporter := logging.NewDiagnosticReporter(resources.Runtime.Logger(), resources.Redactor)
	localDiagnostics := ui.LocalTaskDiagnostics{Reporter: diagnosticReporter}
	actions.Diagnostics.Reporter = diagnosticReporter
	installationWorker.diagnostics = actions.Diagnostics
	if preparedWorker != nil {
		preparedWorker.diagnostics = localDiagnostics
	}
	if options.Client != nil {
		if err := options.Client.SetRedactor(resources.Redactor); err != nil {
			return errors.Join(err, resources.Close())
		}
		if err := options.Client.SetDiagnosticReporter(diagnosticReporter); err != nil {
			return errors.Join(err, resources.Close())
		}
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
	applier := newRunLoggingApplier(ctx, resources.Runtime, localDiagnostics)

	var controlSession *session.Session
	var events <-chan session.Event
	cancelSession := func() {}
	if options.Client != nil {
		sessionCtx, cancel := context.WithCancel(ctx)
		cancelSession = cancel
		controlSession = session.New(options.Client, session.Options{Reporter: diagnosticReporter})
		events = controlSession.Start(sessionCtx)
	}
	model := newRunModel(ctx, options.Client, events, health, applier)
	model.setInstallationActions(actions)
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		userDir, exportDir := options.UserLogDir, options.UserExportDir
		if options.SplitLogging && resources.PrivateFS == nil {
			userDir, exportDir = "", ""
		}
		page.SetLoggingLayout(options.SplitLogging, userDir, exportDir)
		page.SetLocalTaskDiagnostics(localDiagnostics)
	}
	if options.Service != nil {
		model.SetServiceController(options.Service)
	}
	if options.Uninstaller != nil {
		if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
			page.SetUninstaller(options.Uninstaller)
		}
	}
	if preparedWorker != nil {
		model.discardPrepared = preparedWorker.discard
	}
	model.SetSelfUpdater(options.SelfUpdater, options.CurrentVersion, options.BinaryPath, options.Elevated)
	model.SetSelfUpdateChannel(options.SelfUpdateChannel)
	exportLogs := attachRunExportLogs(ctx, &model, resources, options.BuildExportLogs, localDiagnostics)
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	)
	final, err := program.Run()
	cleanup := newRunCleanup(&resources, cancelSession, func() {
		if controlSession != nil {
			controlSession.Close()
		}
	}, exportLogs, applier, reporter)
	closeResources := cleanup
	cleanup = func(final tea.Model) error { installationWorker.shutdown(); return closeResources(final) }
	if preparedWorker != nil {
		closeResources := cleanup
		cleanup = func(final tea.Model) error { preparedWorker.shutdown(); return closeResources(final) }
	}
	if finalModel, ok := final.(Model); ok && finalModel.preparedInstallation != nil {
		if preparedWorker != nil {
			cleanup = installationCleanup(preparedWorker.close, cleanup)
		}
		return finishInstallationRun(ctx, final, err, options.Output, cleanup, actions.Execute)
	}
	if finalModel, ok := final.(*Model); ok && finalModel != nil && finalModel.preparedInstallation != nil {
		if preparedWorker != nil {
			cleanup = installationCleanup(preparedWorker.close, cleanup)
		}
		return finishInstallationRun(ctx, final, err, options.Output, cleanup, actions.Execute)
	}
	if finalModel, ok := final.(Model); ok && finalModel.preparedUninstall {
		return finishCompleteUninstallRun(ctx, final, err, options.Output, cleanup, options.Uninstaller)
	}
	if preparedWorker != nil {
		return finishPreparedRun(ctx, final, err, options.Output, options.Relaunch, cleanup, preparedWorker.ApplyPrepared)
	}
	return finishRun(final, err, options.Output, options.Relaunch, cleanup)
}

func attachRunExportLogs(ctx context.Context, model *Model, resources LoggingResources, build func(LoggingResources) ui.ExportLogsOptions, diagnostics ui.LocalTaskDiagnostics) *ui.ExportLogsModel {
	if model == nil || build == nil {
		return nil
	}
	options := build(resources)
	options.Context = ctx
	options.Diagnostics.Reporter = diagnostics.Reporter
	exportLogs := ui.NewExportLogsModel(options)
	model.exportLogs = exportLogs
	return exportLogs
}

type runShutdownHooks struct {
	CancelExport  func()
	CloseResponse func()
	WaitWorkers   func()
	Logging       io.Closer
	UserFS        io.Closer
}

func executeRunShutdown(hooks runShutdownHooks, observe func(string)) error {
	if observe == nil {
		observe = func(string) {}
	}
	observe("cancel-export")
	if hooks.CancelExport != nil {
		hooks.CancelExport()
	}
	observe("close-response")
	if hooks.CloseResponse != nil {
		hooks.CloseResponse()
	}
	observe("wait-workers")
	if hooks.WaitWorkers != nil {
		hooks.WaitWorkers()
	}
	var errs []error
	observe("close-logging")
	if hooks.Logging != nil {
		errs = append(errs, hooks.Logging.Close())
	}
	observe("close-user-fs")
	if hooks.UserFS != nil {
		errs = append(errs, hooks.UserFS.Close())
	}
	return errors.Join(errs...)
}

func closeResponseAndJoin(closeResponse func()) {
	if closeResponse == nil {
		return
	}
	// Closing the response terminates transport IO. Its owner must finish before
	// workers, logging and the retained filesystem capability can be released.
	closeResponse()
}

func newRunCleanup(resources *LoggingResources, cancelSession, closeSession func(), exportLogs *ui.ExportLogsModel, applier loggingApplier, reporter *tuiLoggingFailureReporter) func(tea.Model) error {
	var once sync.Once
	var closeErr error
	return func(tea.Model) error {
		once.Do(func() {
			var runtimeCloser, fsCloser io.Closer
			if resources != nil {
				state := resources.closeState
				if state == nil {
					if resources.Runtime != nil {
						runtimeCloser = resources.Runtime
					}
					if resources.PrivateFS != nil {
						fsCloser = resources.PrivateFS
					}
					state = newLoggingResourcesCloseState(runtimeCloser, fsCloser)
					resources.closeState = state
				} else {
					runtimeCloser, fsCloser = state.runtime, state.privateFS
				}
				state.once.Do(func() {
					state.err = executeRunShutdown(runShutdownHooks{
						CancelExport: func() {
							if exportLogs != nil {
								exportLogs.Cancel()
							}
							if cancelSession != nil {
								cancelSession()
							}
							if applier != nil {
								applier.Cancel()
							}
						},
						CloseResponse: func() {
							closeResponseAndJoin(func() {
								if exportLogs != nil {
									exportLogs.CloseResponse()
								}
							})
						},
						WaitWorkers: func() {
							if exportLogs != nil {
								exportLogs.Wait()
							}
							if closeSession != nil {
								closeSession()
							}
							if applier != nil {
								applier.CloseAndWait()
							}
						},
						Logging: runtimeCloser,
						UserFS:  fsCloser,
					}, nil)
				})
				closeErr = state.err
			} else {
				closeErr = executeRunShutdown(runShutdownHooks{
					CancelExport: func() {
						if exportLogs != nil {
							exportLogs.Cancel()
						}
						if cancelSession != nil {
							cancelSession()
						}
						if applier != nil {
							applier.Cancel()
						}
					},
					CloseResponse: func() {
						closeResponseAndJoin(func() {
							if exportLogs != nil {
								exportLogs.CloseResponse()
							}
						})
					},
					WaitWorkers: func() {
						if exportLogs != nil {
							exportLogs.Wait()
						}
						if closeSession != nil {
							closeSession()
						}
						if applier != nil {
							applier.CloseAndWait()
						}
					},
				}, nil)
			}
			if reporter != nil && closeErr != nil {
				reporter.report(tuiLoggingCleanupFailure, closeErr)
			}
		})
		return closeErr
	}
}

func newRunLoggingApplier(ctx context.Context, runtime *logging.Runtime, diagnostics ui.LocalTaskDiagnostics) loggingApplier {
	if runtime == nil {
		return newLoggingApplier(ctx, nil)
	}
	return newLoggingApplierWithDiagnostics(ctx, runtime, diagnostics)
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
